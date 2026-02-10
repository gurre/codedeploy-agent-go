// Package localcli wires the simplified codedeploy-local CLI flow.
// Unlike the full agent, it runs a single deployment synchronously without
// polling or service communication.
package localcli

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/gurre/codedeploy-agent-go/adaptor/archive"
	"github.com/gurre/codedeploy-agent-go/adaptor/configloader"
	"github.com/gurre/codedeploy-agent-go/adaptor/filesystem"
	"github.com/gurre/codedeploy-agent-go/adaptor/githubdownload"
	"github.com/gurre/codedeploy-agent-go/adaptor/s3download"
	"github.com/gurre/codedeploy-agent-go/adaptor/scriptrunner"
	"github.com/gurre/codedeploy-agent-go/logic/appspec"
	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
	"github.com/gurre/codedeploy-agent-go/orchestration/executor"
	"github.com/gurre/codedeploy-agent-go/orchestration/hookrunner"
	"github.com/gurre/codedeploy-agent-go/orchestration/installer"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// Options holds the CLI arguments for a local deployment.
type Options struct {
	BundleLocation      string
	BundleType          string
	FileExistsBehavior  string
	DeploymentGroup     string
	DeploymentGroupName string
	ApplicationName     string
	Events              []string
	ConfigFile          string
	AppSpecFilename     string
}

// DefaultOptions returns options with the same defaults as the Ruby CLI.
//
//	opts := localcli.DefaultOptions()
//	opts.BundleLocation = "/path/to/app"
func DefaultOptions() Options {
	return Options{
		BundleType:          "directory",
		FileExistsBehavior:  "DISALLOW",
		DeploymentGroup:     "default-local-deployment-group",
		DeploymentGroupName: "LocalFleet",
		AppSpecFilename:     "appspec.yml",
	}
}

// defaultOrderedEvents is the full lifecycle event sequence for local deployments.
var defaultOrderedEvents = []string{
	"BeforeBlockTraffic",
	"AfterBlockTraffic",
	"ApplicationStop",
	"DownloadBundle",
	"BeforeInstall",
	"Install",
	"AfterInstall",
	"ApplicationStart",
	"ValidateService",
	"BeforeAllowTraffic",
	"AfterAllowTraffic",
}

// Run executes a local deployment with the given options.
// It validates inputs, builds a deployment spec, and runs each lifecycle
// event sequentially. Returns an error if any step fails.
//
//	err := localcli.Run(ctx, opts)
func Run(ctx context.Context, opts Options) error {
	logger := slog.Default()

	if err := validate(opts); err != nil {
		return err
	}

	// Resolve bundle location to absolute path for local sources
	if !isRemoteLocation(opts.BundleLocation) {
		abs, err := filepath.Abs(opts.BundleLocation)
		if err != nil {
			return fmt.Errorf("localcli: resolve path: %w", err)
		}
		opts.BundleLocation = abs
	}

	// Load config for rootDir and max_revisions
	rootDir := "/opt/codedeploy-agent/deployment-root"
	maxRevisions := 5
	if opts.ConfigFile != "" {
		cfg, err := configloader.LoadAgent(opts.ConfigFile)
		if err != nil {
			return fmt.Errorf("localcli: load config: %w", err)
		}
		rootDir = cfg.RootDir
		maxRevisions = cfg.MaxRevisions
	}

	if opts.ApplicationName == "" {
		opts.ApplicationName = opts.BundleLocation
	}

	// Build deployment spec
	spec, err := buildSpec(opts)
	if err != nil {
		return fmt.Errorf("localcli: build spec: %w", err)
	}

	// Determine events to execute
	events := resolveEvents(opts.Events)

	logger.Info("local deployment starting",
		"deploymentID", spec.DeploymentID,
		"bundleLocation", opts.BundleLocation,
		"bundleType", opts.BundleType,
		"events", events)

	// Build executor with custom events merged into hook mapping
	exec, err := buildExecutor(ctx, rootDir, maxRevisions, opts.Events, logger)
	if err != nil {
		return fmt.Errorf("localcli: build executor: %w", err)
	}

	// Execute each event sequentially
	for _, event := range events {
		logger.Info("executing lifecycle event", "event", event)
		if _, err := exec.Execute(ctx, event, spec); err != nil {
			return fmt.Errorf("localcli: %s failed: %w", event, err)
		}
	}

	logger.Info("local deployment succeeded", "deploymentID", spec.DeploymentID)
	return nil
}

func validate(opts Options) error {
	validTypes := map[string]bool{"tar": true, "tgz": true, "zip": true, "directory": true}
	if !validTypes[opts.BundleType] {
		return fmt.Errorf("localcli: invalid bundle type %q (must be tar, tgz, zip, or directory)", opts.BundleType)
	}

	validFEB := map[string]bool{"DISALLOW": true, "OVERWRITE": true, "RETAIN": true}
	if !validFEB[strings.ToUpper(opts.FileExistsBehavior)] {
		return fmt.Errorf("localcli: invalid file-exists-behavior %q", opts.FileExistsBehavior)
	}

	if opts.BundleLocation == "" {
		return fmt.Errorf("localcli: bundle location required")
	}

	// Validate local paths exist
	if !isRemoteLocation(opts.BundleLocation) {
		info, err := os.Stat(opts.BundleLocation)
		if err != nil {
			return fmt.Errorf("localcli: bundle location %q: %w", opts.BundleLocation, err)
		}
		if opts.BundleType == "directory" && !info.IsDir() {
			return fmt.Errorf("localcli: bundle type is directory but %q is a file", opts.BundleLocation)
		}
		if opts.BundleType != "directory" && info.IsDir() {
			return fmt.Errorf("localcli: bundle type is %s but %q is a directory", opts.BundleType, opts.BundleLocation)
		}
		// Check appspec exists for directory bundles. When a custom appspec
		// filename is set (e.g. --appspec-filename), only that file is checked.
		// Otherwise fall back to the standard appspec.yml / appspec.yaml pair.
		if opts.BundleType == "directory" {
			if opts.AppSpecFilename != "" && opts.AppSpecFilename != "appspec.yml" && opts.AppSpecFilename != "appspec.yaml" {
				if _, err := os.Stat(filepath.Join(opts.BundleLocation, opts.AppSpecFilename)); err != nil {
					return fmt.Errorf("localcli: %s not found in %q", opts.AppSpecFilename, opts.BundleLocation)
				}
			} else {
				_, errYml := os.Stat(filepath.Join(opts.BundleLocation, "appspec.yml"))
				_, errYaml := os.Stat(filepath.Join(opts.BundleLocation, "appspec.yaml"))
				if errYml != nil && errYaml != nil {
					return fmt.Errorf("localcli: appspec.yml/appspec.yaml not found in %q", opts.BundleLocation)
				}
			}
		}
	}

	return nil
}

func isRemoteLocation(location string) bool {
	return strings.HasPrefix(location, "s3://") ||
		strings.HasPrefix(location, "https://") ||
		strings.Contains(location, "/") && strings.Contains(location, "github.com")
}

func buildSpec(opts Options) (deployspec.Spec, error) {
	deploymentID := "d-" + randomAlphanumeric(9) + "-local"

	// Merge custom events into AllPossibleLifecycleEvents
	allEvents := mergeCustomEvents(defaultOrderedEvents, opts.Events)

	spec := deployspec.Spec{
		DeploymentID:               deploymentID,
		DeploymentGroupID:          opts.DeploymentGroup,
		DeploymentGroupName:        opts.DeploymentGroupName,
		ApplicationName:            opts.ApplicationName,
		DeploymentCreator:          "user",
		DeploymentType:             "IN_PLACE",
		AppSpecPath:                opts.AppSpecFilename,
		FileExistsBehavior:         strings.ToUpper(opts.FileExistsBehavior),
		AllPossibleLifecycleEvents: allEvents,
	}

	// Determine revision source
	switch {
	case strings.HasPrefix(opts.BundleLocation, "s3://"):
		bucket, key, versionID, etag, err := parseS3URL(opts.BundleLocation)
		if err != nil {
			return deployspec.Spec{}, err
		}
		spec.Source = deployspec.RevisionS3
		spec.Bucket = bucket
		spec.Key = key
		spec.Version = versionID
		spec.ETag = etag
		spec.BundleType = opts.BundleType
	case opts.BundleType == "directory":
		spec.Source = deployspec.RevisionLocalDirectory
		spec.LocalLocation = opts.BundleLocation
		spec.BundleType = "directory"
	default:
		spec.Source = deployspec.RevisionLocalFile
		spec.LocalLocation = opts.BundleLocation
		spec.BundleType = opts.BundleType
	}

	return spec, nil
}

// mergeCustomEvents appends user events that are not already in the default
// set. This matches the Ruby agent's deployer.rb:120-122 behavior.
func mergeCustomEvents(defaults, userEvents []string) []string {
	if len(userEvents) == 0 {
		return defaults
	}
	existing := make(map[string]bool, len(defaults))
	for _, e := range defaults {
		existing[e] = true
	}
	result := make([]string, len(defaults))
	copy(result, defaults)
	for _, e := range userEvents {
		if !existing[e] {
			result = append(result, e)
			existing[e] = true
		}
	}
	return result
}

// parseS3URL extracts bucket, key, versionId, and etag from an s3://bucket/key URL.
// Query parameters versionId and etag are optional.
//
//	bucket, key, ver, etag, err := parseS3URL("s3://my-bucket/path/to/app.tar?versionId=v1&etag=abc")
func parseS3URL(raw string) (bucket, key, versionID, etag string, err error) {
	if !strings.HasPrefix(raw, "s3://") {
		return "", "", "", "", fmt.Errorf("localcli: not an S3 URL: %q", raw)
	}
	// Strip the s3:// prefix and parse as a path
	trimmed := strings.TrimPrefix(raw, "s3://")

	// Split query string
	var path, query string
	if idx := strings.IndexByte(trimmed, '?'); idx >= 0 {
		path = trimmed[:idx]
		query = trimmed[idx+1:]
	} else {
		path = trimmed
	}

	// Split bucket from key at first slash
	slashIdx := strings.IndexByte(path, '/')
	if slashIdx < 0 || slashIdx == 0 {
		return "", "", "", "", fmt.Errorf("localcli: S3 URL missing bucket or key: %q", raw)
	}
	bucket = path[:slashIdx]
	key = path[slashIdx+1:]
	if key == "" {
		return "", "", "", "", fmt.Errorf("localcli: S3 URL has empty key: %q", raw)
	}

	// Parse query parameters
	if query != "" {
		for _, param := range strings.Split(query, "&") {
			kv := strings.SplitN(param, "=", 2)
			if len(kv) != 2 {
				continue
			}
			switch kv[0] {
			case "versionId":
				versionID = kv[1]
			case "etag":
				etag = kv[1]
			}
		}
	}

	return bucket, key, versionID, etag, nil
}

// resolveEvents builds the final event list from user-specified events.
// DownloadBundle and Install are always prepended (in that order) before user
// events, matching the Ruby agent's deployer.rb ordering. When no events are
// specified, the full default lifecycle is returned.
func resolveEvents(userEvents []string) []string {
	if len(userEvents) == 0 {
		return defaultOrderedEvents
	}

	// Filter out DownloadBundle and Install from user events — they will be
	// prepended in the correct position regardless.
	filtered := make([]string, 0, len(userEvents))
	for _, e := range userEvents {
		if e != "DownloadBundle" && e != "Install" {
			filtered = append(filtered, e)
		}
	}

	result := make([]string, 0, len(filtered)+2)
	result = append(result, "DownloadBundle", "Install")
	result = append(result, filtered...)
	return result
}

func buildExecutor(ctx context.Context, rootDir string, maxRevisions int, customEvents []string, logger *slog.Logger) (*executor.Executor, error) {
	unpacker := archive.NewUnpacker()
	fileOp := filesystem.NewOperator()
	sr := scriptrunner.NewRunner(logger)

	// Build S3 downloader (may not be needed for local, but wire it for S3 sources)
	var s3dl *s3download.Downloader
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err == nil {
		s3dl = s3download.NewDownloader(awsCfg, awsCfg.Region, "", false, nil, logger)
	}

	ghDl := githubdownload.NewDownloader(nil, logger) // nil transport = default
	hookMapping := lifecycle.DefaultHookMapping()

	// Merge custom events into hook mapping. Each custom event maps to itself,
	// matching Ruby deployer.rb:124-127 behavior.
	for _, e := range customEvents {
		if _, exists := hookMapping[e]; !exists {
			hookMapping[e] = []lifecycle.Event{lifecycle.Event(e)}
		}
	}

	dl := &localDownloaderBridge{s3: s3dl, gh: ghDl}
	fileOpBridge := &localFileOperatorBridge{op: fileOp}
	hookBridge := &localHookRunnerBridge{runner: hookrunner.NewRunner(&localScriptRunnerBridge{sr: sr}, logger)}
	instBridge := &localInstallerBridge{inst: installer.NewInstaller(&localFileOpInstallerBridge{op: fileOp}, logger)}

	return executor.NewExecutor(
		dl, unpacker, hookBridge, instBridge, fileOpBridge,
		rootDir, hookMapping, maxRevisions, logger,
	), nil
}

func randomAlphanumeric(n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[idx.Int64()]
	}
	return string(b)
}

// Bridge types for local CLI wiring (same pattern as entrypoint/agent).

type localDownloaderBridge struct {
	s3 *s3download.Downloader
	gh *githubdownload.Downloader
}

func (d *localDownloaderBridge) DownloadS3(ctx context.Context, bucket, key, version, etag, dest string) error {
	if d.s3 == nil {
		return fmt.Errorf("localcli: S3 downloader not configured (AWS credentials required)")
	}
	return d.s3.Download(ctx, bucket, key, version, etag, dest)
}

func (d *localDownloaderBridge) DownloadGitHub(ctx context.Context, account, repo, commit, bundleType, token, dest string) error {
	return d.gh.Download(ctx, account, repo, commit, bundleType, token, dest)
}

type localFileOperatorBridge struct {
	op *filesystem.Operator
}

func (f *localFileOperatorBridge) MkdirAll(path string) error  { return f.op.MkdirAll(path) }
func (f *localFileOperatorBridge) RemoveAll(path string) error { return f.op.RemoveAll(path) }

type localScriptRunnerBridge struct {
	sr *scriptrunner.Runner
}

func (s *localScriptRunnerBridge) Run(ctx context.Context, scriptPath string, env map[string]string, timeoutSeconds int) (hookrunner.ScriptResult, error) {
	result, err := s.sr.Run(ctx, scriptPath, env, timeoutSeconds)
	if err != nil {
		return hookrunner.ScriptResult{}, err
	}
	return hookrunner.ScriptResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		TimedOut: result.TimedOut,
	}, nil
}

type localHookRunnerBridge struct {
	runner *hookrunner.Runner
}

func (h *localHookRunnerBridge) Run(ctx context.Context, args executor.HookRunArgs) (executor.HookResult, error) {
	result, err := h.runner.Run(ctx, hookrunner.RunArgs{
		LifecycleEvent:      args.LifecycleEvent,
		DeploymentID:        args.DeploymentID,
		ApplicationName:     args.ApplicationName,
		DeploymentGroupName: args.DeploymentGroupName,
		DeploymentGroupID:   args.DeploymentGroupID,
		DeploymentCreator:   args.DeploymentCreator,
		DeploymentType:      args.DeploymentType,
		AppSpecPath:         args.AppSpecPath,
		DeploymentRootDir:   args.DeploymentRootDir,
		LastSuccessfulDir:   args.LastSuccessfulDir,
		MostRecentDir:       args.MostRecentDir,
		RevisionEnvs:        args.RevisionEnvs,
	})
	if err != nil {
		return executor.HookResult{Log: result.Log}, err
	}
	return executor.HookResult{IsNoop: result.IsNoop, Log: result.Log}, nil
}

func (h *localHookRunnerBridge) IsNoop(args executor.HookRunArgs) (bool, error) {
	return h.runner.IsNoop(hookrunner.RunArgs{
		LifecycleEvent:      args.LifecycleEvent,
		DeploymentID:        args.DeploymentID,
		ApplicationName:     args.ApplicationName,
		DeploymentGroupName: args.DeploymentGroupName,
		DeploymentGroupID:   args.DeploymentGroupID,
		DeploymentCreator:   args.DeploymentCreator,
		DeploymentType:      args.DeploymentType,
		AppSpecPath:         args.AppSpecPath,
		DeploymentRootDir:   args.DeploymentRootDir,
		LastSuccessfulDir:   args.LastSuccessfulDir,
		MostRecentDir:       args.MostRecentDir,
		RevisionEnvs:        args.RevisionEnvs,
	})
}

type localFileOpInstallerBridge struct {
	op *filesystem.Operator
}

func (f *localFileOpInstallerBridge) Copy(source, destination string) error {
	return f.op.Copy(source, destination)
}
func (f *localFileOpInstallerBridge) Mkdir(path string) error    { return f.op.Mkdir(path) }
func (f *localFileOpInstallerBridge) MkdirAll(path string) error { return f.op.MkdirAll(path) }
func (f *localFileOpInstallerBridge) Chmod(path string, mode os.FileMode) error {
	return f.op.Chmod(path, mode)
}
func (f *localFileOpInstallerBridge) Chown(path, owner, group string) error {
	return f.op.Chown(path, owner, group)
}
func (f *localFileOpInstallerBridge) SetACL(path string, acl []string) error {
	return f.op.SetACL(path, acl)
}
func (f *localFileOpInstallerBridge) SetContext(path string, seUser, seType, seRange string) error {
	return f.op.SetContext(path, seUser, seType, seRange)
}
func (f *localFileOpInstallerBridge) RemoveContext(path string) error {
	return f.op.RemoveContext(path)
}
func (f *localFileOpInstallerBridge) Remove(path string) error { return f.op.Remove(path) }

type localInstallerBridge struct {
	inst *installer.Installer
}

func (i *localInstallerBridge) Install(deploymentGroupID, archiveDir, instructionsDir string, spec appspec.Spec, fileExistsBehavior string) error {
	return i.inst.Install(deploymentGroupID, archiveDir, instructionsDir, spec, fileExistsBehavior)
}
