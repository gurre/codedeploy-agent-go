// Package hookrunner resolves deployment roots, parses appspec files, and
// delegates lifecycle hook script execution to a ScriptRunner.
package hookrunner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/gurre/codedeploy-agent-go/logic/appspec"
	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
)

// ScriptRunner executes a script and returns the result.
type ScriptRunner interface {
	Run(ctx context.Context, scriptPath string, env map[string]string, timeoutSeconds int) (ScriptResult, error)
}

// ScriptResult holds the outcome of a script execution.
type ScriptResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
}

// HookResult holds the outcome of executing all scripts for a lifecycle event.
type HookResult struct {
	IsNoop bool
	Log    string
}

// Runner executes lifecycle hook scripts for a deployment.
type Runner struct {
	scriptRunner ScriptRunner
	logger       *slog.Logger
}

// NewRunner creates a hook runner.
//
//	r := hookrunner.NewRunner(scriptRunner, slog.Default())
func NewRunner(sr ScriptRunner, logger *slog.Logger) *Runner {
	return &Runner{
		scriptRunner: sr,
		logger:       logger,
	}
}

// RunArgs holds the arguments for running a lifecycle event's hooks.
type RunArgs struct {
	LifecycleEvent      lifecycle.Event
	DeploymentID        string
	ApplicationName     string
	DeploymentGroupName string
	DeploymentGroupID   string
	DeploymentCreator   string
	DeploymentType      string
	AppSpecPath         string
	// DeploymentRootDir is the current deployment's root directory.
	DeploymentRootDir string
	// LastSuccessfulDir is the last successfully installed deployment's root.
	LastSuccessfulDir string
	// MostRecentDir is the most recently downloaded deployment's root.
	MostRecentDir string
	// RevisionEnvs are extra environment variables from the revision source.
	RevisionEnvs map[string]string
}

// Run executes all hook scripts for a lifecycle event.
// Returns a HookResult with IsNoop=true if no scripts are defined for the event.
func (r *Runner) Run(ctx context.Context, args RunArgs) (HookResult, error) {
	archiveDir := selectDeploymentRoot(args)
	if archiveDir == "" {
		return HookResult{IsNoop: true}, nil
	}

	specPath, err := appspec.FindAppSpecFile(archiveDir, args.AppSpecPath)
	if err != nil {
		return HookResult{}, err
	}

	spec, err := appspec.ParseFile(specPath)
	if err != nil {
		return HookResult{}, err
	}

	eventName := string(args.LifecycleEvent)
	scripts, ok := spec.Hooks[eventName]
	if !ok || len(scripts) == 0 {
		return HookResult{IsNoop: true}, nil
	}

	env := buildEnv(args)
	var logOutput string

	for _, script := range scripts {
		scriptPath := filepath.Join(archiveDir, script.Location)

		r.logger.Info("executing hook script", "event", eventName, "script", script.Location)

		result, err := r.scriptRunner.Run(ctx, scriptPath, env, script.Timeout)
		if err != nil {
			return HookResult{}, fmt.Errorf("hookrunner: %s: %w", script.Location, err)
		}

		logOutput += formatScriptLog(script.Location, result.Stdout, result.Stderr)

		if result.TimedOut {
			return HookResult{Log: logOutput}, fmt.Errorf(
				"script at %s timed out after %d seconds", script.Location, script.Timeout)
		}

		if result.ExitCode != 0 {
			return HookResult{Log: logOutput}, fmt.Errorf(
				"script at %s failed with exit code %d", script.Location, result.ExitCode)
		}
	}

	return HookResult{Log: logOutput}, nil
}

// IsNoop checks whether a lifecycle event has any scripts to run.
func (r *Runner) IsNoop(args RunArgs) (bool, error) {
	archiveDir := selectDeploymentRoot(args)
	if archiveDir == "" {
		return true, nil
	}

	specPath, err := appspec.FindAppSpecFile(archiveDir, args.AppSpecPath)
	if err != nil {
		return true, nil // No appspec means no scripts
	}

	spec, err := appspec.ParseFile(specPath)
	if err != nil {
		return true, nil
	}

	scripts := spec.Hooks[string(args.LifecycleEvent)]
	return len(scripts) == 0, nil
}

func selectDeploymentRoot(args RunArgs) string {
	root := lifecycle.SelectDeploymentRoot(
		args.LifecycleEvent, args.DeploymentCreator, args.DeploymentType)

	var dir string
	switch root {
	case lifecycle.Current:
		dir = args.DeploymentRootDir
	case lifecycle.LastSuccessful:
		dir = args.LastSuccessfulDir
		if dir == "" {
			dir = args.DeploymentRootDir
		}
	case lifecycle.MostRecent:
		dir = args.MostRecentDir
		if dir == "" {
			dir = args.DeploymentRootDir
		}
	}

	archiveDir := filepath.Join(dir, "deployment-archive")
	if _, err := os.Stat(archiveDir); os.IsNotExist(err) {
		// Fall back to current deployment if archive doesn't exist
		fallback := filepath.Join(args.DeploymentRootDir, "deployment-archive")
		if _, err := os.Stat(fallback); os.IsNotExist(err) {
			return ""
		}
		return fallback
	}
	return archiveDir
}

func buildEnv(args RunArgs) map[string]string {
	env := make(map[string]string, 5+len(args.RevisionEnvs))
	env["LIFECYCLE_EVENT"] = string(args.LifecycleEvent)
	env["DEPLOYMENT_ID"] = args.DeploymentID
	env["APPLICATION_NAME"] = args.ApplicationName
	env["DEPLOYMENT_GROUP_NAME"] = args.DeploymentGroupName
	env["DEPLOYMENT_GROUP_ID"] = args.DeploymentGroupID
	for k, v := range args.RevisionEnvs {
		env[k] = v
	}
	return env
}

func formatScriptLog(scriptName, stdout, stderr string) string {
	return fmt.Sprintf("Script - %s\n%s%s", scriptName, stdout, stderr)
}
