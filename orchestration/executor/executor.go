// Package executor dispatches deployment commands (DownloadBundle, Install, lifecycle hooks)
// and manages archive cleanup.
package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gurre/codedeploy-agent-go/logic/appspec"
	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
	"github.com/gurre/codedeploy-agent-go/state/deployment"
)

// BundleDownloader downloads deployment bundles from various sources.
type BundleDownloader interface {
	DownloadS3(ctx context.Context, bucket, key, version, etag, destPath string) error
	DownloadGitHub(ctx context.Context, account, repo, commit, bundleType, token, destPath string) error
}

// ArchiveUnpacker extracts bundle archives.
type ArchiveUnpacker interface {
	Unpack(archivePath, destDir, bundleType string) error
}

// HookRunner executes lifecycle hook scripts.
type HookRunner interface {
	Run(ctx context.Context, args HookRunArgs) (HookResult, error)
	IsNoop(args HookRunArgs) (bool, error)
}

// HookRunArgs mirrors hookrunner.RunArgs to avoid import cycles.
type HookRunArgs struct {
	LifecycleEvent      lifecycle.Event
	DeploymentID        string
	ApplicationName     string
	DeploymentGroupName string
	DeploymentGroupID   string
	DeploymentCreator   string
	DeploymentType      string
	AppSpecPath         string
	DeploymentRootDir   string
	LastSuccessfulDir   string
	MostRecentDir       string
	RevisionEnvs        map[string]string
}

// HookResult holds the hook execution result.
type HookResult struct {
	IsNoop bool
	Log    string
}

// Installer handles the Install command.
type Installer interface {
	Install(deploymentGroupID, archiveDir, instructionsDir string, spec appspec.Spec, fileExistsBehavior string) error
}

// FileOperator for local file operations during DownloadBundle.
type FileOperator interface {
	MkdirAll(path string) error
	RemoveAll(path string) error
}

// Executor dispatches deployment commands.
type Executor struct {
	downloader   BundleDownloader
	unpacker     ArchiveUnpacker
	hookRunner   HookRunner
	installer    Installer
	fileOp       FileOperator
	rootDir      string
	hookMapping  map[string][]lifecycle.Event
	logger       *slog.Logger
	maxRevisions int
}

// NewExecutor creates a command executor.
//
//	exec := executor.NewExecutor(dl, unpacker, hookRunner, inst, fileOp, rootDir, hookMapping, 5, logger)
func NewExecutor(
	dl BundleDownloader,
	unpacker ArchiveUnpacker,
	hookRunner HookRunner,
	installer Installer,
	fileOp FileOperator,
	rootDir string,
	hookMapping map[string][]lifecycle.Event,
	maxRevisions int,
	logger *slog.Logger,
) *Executor {
	if maxRevisions < 1 {
		logger.Error("invalid max_revisions, using default", "value", maxRevisions)
		maxRevisions = 5
	}
	return &Executor{
		downloader:   dl,
		unpacker:     unpacker,
		hookRunner:   hookRunner,
		installer:    installer,
		fileOp:       fileOp,
		rootDir:      rootDir,
		hookMapping:  hookMapping,
		maxRevisions: maxRevisions,
		logger:       logger,
	}
}

// Execute dispatches a command by name with the given deployment spec.
func (e *Executor) Execute(ctx context.Context, commandName string, spec deployspec.Spec) (string, error) {
	layout := deployment.NewLayout(e.rootDir, spec.DeploymentGroupID, spec.DeploymentID)

	if err := e.fileOp.MkdirAll(layout.DeploymentRootDir()); err != nil {
		return "", fmt.Errorf("executor: mkdir deployment root: %w", err)
	}

	switch commandName {
	case "DownloadBundle":
		return "", e.downloadBundle(ctx, spec, layout)
	case "Install":
		return "", e.install(ctx, spec, layout)
	default:
		return e.executeHooks(ctx, commandName, spec, layout)
	}
}

// IsNoop checks whether a command would be a no-op.
func (e *Executor) IsNoop(commandName string, spec deployspec.Spec) bool {
	if commandName == "DownloadBundle" || commandName == "Install" {
		return false
	}
	events, ok := e.hookMapping[commandName]
	if !ok {
		return true
	}
	layout := deployment.NewLayout(e.rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	for _, event := range events {
		noop, _ := e.hookRunner.IsNoop(e.buildHookArgs(event, spec, layout))
		if !noop {
			return false
		}
	}
	return true
}

func (e *Executor) downloadBundle(ctx context.Context, spec deployspec.Spec, layout deployment.Layout) error {
	e.cleanupOldArchives(spec)

	// Create per-deployment logs directory
	if err := e.fileOp.MkdirAll(layout.LogsDir()); err != nil {
		return fmt.Errorf("executor: mkdir deployment logs dir: %w", err)
	}

	// Create shared deployment-logs directory and append log entry
	logsDir := deployment.DeploymentLogsDir(e.rootDir)
	if err := e.fileOp.MkdirAll(logsDir); err != nil {
		return fmt.Errorf("executor: mkdir deployment-logs: %w", err)
	}
	e.appendDeploymentLog(spec)

	switch spec.Source {
	case deployspec.RevisionS3:
		if err := e.downloader.DownloadS3(ctx, spec.Bucket, spec.Key, spec.Version, spec.ETag, layout.BundleFile()); err != nil {
			return err
		}
	case deployspec.RevisionGitHub:
		if err := e.downloader.DownloadGitHub(ctx, spec.Account, spec.Repository, spec.CommitID, spec.BundleType, spec.ExternalAuthToken, layout.BundleFile()); err != nil {
			return err
		}
	case deployspec.RevisionLocalFile:
		// Symlink local file to bundle location
		if err := os.Symlink(spec.LocalLocation, layout.BundleFile()); err != nil {
			return fmt.Errorf("executor: symlink local file: %w", err)
		}
	case deployspec.RevisionLocalDirectory:
		if err := copyDir(spec.LocalLocation, layout.ArchiveDir()); err != nil {
			return fmt.Errorf("executor: copy local directory: %w", err)
		}
	default:
		return fmt.Errorf("executor: unknown revision source %q", spec.Source)
	}

	// Unpack if not a directory bundle
	if spec.BundleType != "directory" {
		_ = e.fileOp.RemoveAll(layout.ArchiveDir())
		if err := e.unpacker.Unpack(layout.BundleFile(), layout.ArchiveDir(), spec.BundleType); err != nil {
			return fmt.Errorf("executor: unpack: %w", err)
		}
	}

	// Ensure instructions directory exists
	instructionsDir := deployment.InstructionsDir(e.rootDir)
	if err := e.fileOp.MkdirAll(instructionsDir); err != nil {
		return err
	}

	// Update most recent pointer
	return e.updatePointer(deployment.MostRecentFile(e.rootDir, spec.DeploymentGroupID), layout.DeploymentRootDir())
}

func (e *Executor) install(_ context.Context, spec deployspec.Spec, layout deployment.Layout) error {
	instructionsDir := deployment.InstructionsDir(e.rootDir)
	if err := e.fileOp.MkdirAll(instructionsDir); err != nil {
		return err
	}

	// Parse appspec
	specPath, err := appspec.FindAppSpecFile(layout.ArchiveDir(), spec.AppSpecPath)
	if err != nil {
		return err
	}
	appSpec, err := appspec.ParseFile(specPath)
	if err != nil {
		return err
	}

	if err := e.installer.Install(spec.DeploymentGroupID, layout.ArchiveDir(), instructionsDir, appSpec, spec.FileExistsBehavior); err != nil {
		return err
	}

	// Update last successful pointer
	return e.updatePointer(deployment.LastSuccessfulFile(e.rootDir, spec.DeploymentGroupID), layout.DeploymentRootDir())
}

func (e *Executor) executeHooks(ctx context.Context, commandName string, spec deployspec.Spec, layout deployment.Layout) (string, error) {
	events, ok := e.hookMapping[commandName]
	if !ok {
		return "", nil
	}

	var allLog string
	for _, event := range events {
		args := e.buildHookArgs(event, spec, layout)
		result, err := e.hookRunner.Run(ctx, args)
		allLog += result.Log
		e.appendScriptLog(layout, result.Log)
		if err != nil {
			return allLog, err
		}
	}
	return allLog, nil
}

func (e *Executor) buildHookArgs(event lifecycle.Event, spec deployspec.Spec, layout deployment.Layout) HookRunArgs {
	return HookRunArgs{
		LifecycleEvent:      event,
		DeploymentID:        spec.DeploymentID,
		ApplicationName:     spec.ApplicationName,
		DeploymentGroupName: spec.DeploymentGroupName,
		DeploymentGroupID:   spec.DeploymentGroupID,
		DeploymentCreator:   spec.DeploymentCreator,
		DeploymentType:      spec.DeploymentType,
		AppSpecPath:         spec.AppSpecPath,
		DeploymentRootDir:   layout.DeploymentRootDir(),
		LastSuccessfulDir:   readPointer(deployment.LastSuccessfulFile(e.rootDir, spec.DeploymentGroupID)),
		MostRecentDir:       readPointer(deployment.MostRecentFile(e.rootDir, spec.DeploymentGroupID)),
		RevisionEnvs:        revisionEnvs(spec),
	}
}

func (e *Executor) cleanupOldArchives(spec deployspec.Spec) {
	groupDir := filepath.Join(e.rootDir, spec.DeploymentGroupID)
	entries, err := os.ReadDir(groupDir)
	if err != nil {
		return
	}

	currentRoot := filepath.Join(groupDir, spec.DeploymentID)
	lastSuccess := readPointer(deployment.LastSuccessfulFile(e.rootDir, spec.DeploymentGroupID))

	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		full := filepath.Join(groupDir, entry.Name())
		if full == currentRoot {
			continue
		}
		candidates = append(candidates, full)
	}

	extra := len(candidates) - e.maxRevisions + 1
	if extra <= 0 {
		return
	}

	// Remove last successful from cleanup candidates
	filtered := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c != lastSuccess {
			filtered = append(filtered, c)
		}
	}

	// Collect modification times to avoid repeated os.Stat during sort
	modTimes := make(map[string]time.Time, len(filtered))
	for _, path := range filtered {
		if info, err := os.Stat(path); err == nil {
			modTimes[path] = info.ModTime()
		}
	}

	// Sort by modification time, oldest first
	sort.Slice(filtered, func(i, j int) bool {
		return modTimes[filtered[i]].Before(modTimes[filtered[j]])
	})

	for i := range min(extra, len(filtered)) {
		e.logger.Info("removing old archive", "path", filtered[i])
		_ = os.RemoveAll(filtered[i])
	}
}

func (e *Executor) updatePointer(path, value string) error {
	return os.WriteFile(path, []byte(value), 0o644)
}

func readPointer(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func revisionEnvs(spec deployspec.Spec) map[string]string {
	switch spec.Source {
	case deployspec.RevisionS3:
		env := make(map[string]string, 4)
		env["BUNDLE_BUCKET"] = spec.Bucket
		env["BUNDLE_KEY"] = spec.Key
		env["BUNDLE_VERSION"] = spec.Version
		env["BUNDLE_ETAG"] = spec.ETag
		return env
	case deployspec.RevisionGitHub:
		env := make(map[string]string, 1)
		env["BUNDLE_COMMIT"] = spec.CommitID
		return env
	default:
		return nil
	}
}

func (e *Executor) appendDeploymentLog(spec deployspec.Spec) {
	logFile := deployment.DeploymentLogFile(e.rootDir)
	entry := fmt.Sprintf("[%s]%s  %s  %s  %s\n",
		time.Now().UTC().Format(time.RFC3339),
		spec.DeploymentID,
		spec.DeploymentGroupID,
		spec.ApplicationName,
		spec.Source,
	)
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.logger.Warn("failed to open deployment log", "error", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(entry); err != nil {
		e.logger.Warn("failed to write deployment log entry", "error", err)
	}
}

func (e *Executor) appendScriptLog(layout deployment.Layout, log string) {
	if log == "" {
		return
	}
	logFile := layout.ScriptLogFile()
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		e.logger.Warn("failed to open script log", "error", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(log); err != nil {
		e.logger.Warn("failed to write script log", "error", err)
	}
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
