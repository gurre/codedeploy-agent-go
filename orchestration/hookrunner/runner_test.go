package hookrunner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
)

// fakeScriptRunner implements ScriptRunner for testing without executing real processes.
type fakeScriptRunner struct {
	calls    []string
	exitCode int
	timedOut bool
}

func (f *fakeScriptRunner) Run(_ context.Context, scriptPath string, _ map[string]string, _ int) (ScriptResult, error) {
	f.calls = append(f.calls, scriptPath)
	return ScriptResult{
		ExitCode: f.exitCode,
		Stdout:   "ok\n",
		TimedOut: f.timedOut,
	}, nil
}

func setupDeployment(t *testing.T, appspecContent string) string {
	t.Helper()
	dir := t.TempDir()
	archiveDir := filepath.Join(dir, "deployment-archive")
	if err := os.MkdirAll(filepath.Join(archiveDir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "appspec.yml"), []byte(appspecContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "scripts/install.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunExecutesScripts verifies that hook runner finds and executes the
// scripts defined in the appspec for the given lifecycle event.
func TestRunExecutesScripts(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  BeforeInstall:
    - location: scripts/install.sh
      timeout: 60
`
	deployDir := setupDeployment(t, appspec)
	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	result, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-123",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   deployDir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsNoop {
		t.Error("should not be noop")
	}
	if len(sr.calls) != 1 {
		t.Errorf("expected 1 script call, got %d", len(sr.calls))
	}
}

// TestRunNoopForMissingEvent verifies that a lifecycle event with no scripts
// in the appspec returns IsNoop=true, allowing the poller to skip execution.
func TestRunNoopForMissingEvent(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  BeforeInstall:
    - location: scripts/install.sh
`
	deployDir := setupDeployment(t, appspec)
	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	result, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.AfterInstall,
		DeploymentID:        "d-123",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   deployDir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsNoop {
		t.Error("should be noop for event without scripts")
	}
	if len(sr.calls) != 0 {
		t.Errorf("no scripts should be called for noop event")
	}
}

// TestRunFailedScript verifies that a non-zero exit code from a script
// returns an error, which the poller uses to report failure.
func TestRunFailedScript(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  BeforeInstall:
    - location: scripts/install.sh
`
	deployDir := setupDeployment(t, appspec)
	sr := &fakeScriptRunner{exitCode: 1}
	runner := NewRunner(sr, slog.Default())

	_, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-123",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   deployDir,
	})
	if err == nil {
		t.Fatal("expected error for failed script")
	}
}

// TestRunTimedOutScript verifies that a timed-out script returns an error.
func TestRunTimedOutScript(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  BeforeInstall:
    - location: scripts/install.sh
      timeout: 1
`
	deployDir := setupDeployment(t, appspec)
	sr := &fakeScriptRunner{timedOut: true}
	runner := NewRunner(sr, slog.Default())

	_, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-123",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   deployDir,
	})
	if err == nil {
		t.Fatal("expected error for timed out script")
	}
}

// TestIsNoop verifies the noop check without running scripts.
func TestIsNoop(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  BeforeInstall:
    - location: scripts/install.sh
`
	deployDir := setupDeployment(t, appspec)
	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	args := RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-1",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   deployDir,
	}

	noop, err := runner.IsNoop(args)
	if err != nil {
		t.Fatal(err)
	}
	if noop {
		t.Error("BeforeInstall should not be noop")
	}

	args.LifecycleEvent = lifecycle.ValidateService
	noop, err = runner.IsNoop(args)
	if err != nil {
		t.Fatal(err)
	}
	if !noop {
		t.Error("ValidateService should be noop (no scripts defined)")
	}
}

// TestRun_LastSuccessful_Fallback verifies that when the lifecycle event maps
// to LastSuccessful (e.g. ApplicationStop) but LastSuccessfulDir is empty,
// selectDeploymentRoot falls back to DeploymentRootDir. This fallback is needed
// because the very first deployment has no last-successful record yet.
func TestRun_LastSuccessful_Fallback(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  ApplicationStop:
    - location: scripts/install.sh
      timeout: 60
`
	// The current deployment root has the appspec and scripts.
	currentDir := setupDeployment(t, appspec)
	// Create a second temp dir to serve as a plausible but unused last-successful dir.
	// We leave LastSuccessfulDir empty to trigger the fallback.
	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	result, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.ApplicationStop,
		DeploymentID:        "d-fallback-ls",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   currentDir,
		LastSuccessfulDir:   "", // empty triggers fallback
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsNoop {
		t.Error("should not be noop; the fallback dir has the appspec with ApplicationStop hooks")
	}
	if len(sr.calls) != 1 {
		t.Errorf("expected 1 script call from fallback dir, got %d", len(sr.calls))
	}
}

// TestRun_MostRecent_Fallback verifies that when the lifecycle event maps to
// MostRecent (BeforeBlockTraffic with rollback+BLUE_GREEN) but MostRecentDir
// is empty, selectDeploymentRoot falls back to DeploymentRootDir. This covers
// the MostRecent branch in selectDeploymentRoot with an empty pointer.
func TestRun_MostRecent_Fallback(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  BeforeBlockTraffic:
    - location: scripts/install.sh
      timeout: 60
`
	currentDir := setupDeployment(t, appspec)
	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	result, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.BeforeBlockTraffic,
		DeploymentID:        "d-fallback-mr",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "codeDeployRollback",
		DeploymentType:      "BLUE_GREEN",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   currentDir,
		MostRecentDir:       "", // empty triggers fallback
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsNoop {
		t.Error("should not be noop; the fallback dir has the appspec with BeforeBlockTraffic hooks")
	}
	if len(sr.calls) != 1 {
		t.Errorf("expected 1 script call from fallback dir, got %d", len(sr.calls))
	}
}

// TestRun_ArchiveNotFound verifies that when neither the selected archive dir
// nor the fallback deployment-archive directory exists, Run returns a noop
// result with no error. This guards against crashes when deployments are cleaned
// up or directories are missing.
func TestRun_ArchiveNotFound(t *testing.T) {
	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	// Use a non-existent path so neither archive dir nor fallback exists.
	result, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-missing",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   "/tmp/nonexistent-deployment-root-" + t.Name(),
	})
	if err != nil {
		t.Fatalf("expected no error for missing archive, got: %v", err)
	}
	if !result.IsNoop {
		t.Error("should be noop when archive dir does not exist")
	}
	if len(sr.calls) != 0 {
		t.Error("no scripts should be called when archive does not exist")
	}
}

// TestIsNoop_NoAppspec verifies that when the archive directory exists but
// contains no appspec.yml, IsNoop returns true. This ensures the runner does
// not crash on a deployment with a missing appspec and correctly reports noop.
func TestIsNoop_NoAppspec(t *testing.T) {
	// Create a deployment root with an empty archive directory (no appspec).
	dir := t.TempDir()
	archiveDir := filepath.Join(dir, "deployment-archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	noop, err := runner.IsNoop(RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-no-appspec",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   dir,
	})
	if err != nil {
		t.Fatalf("IsNoop: %v", err)
	}
	if !noop {
		t.Error("should be noop when no appspec.yml exists in the archive dir")
	}
}

// TestRun_FirstDeployment_ApplicationStopIsNoopWithoutArchive verifies that
// ApplicationStop returns noop when the deployment root dir exists but has no
// deployment-archive/ subdirectory. On a first deployment, DownloadBundle has
// not yet run, so no archive exists. selectDeploymentRoot (runner.go:145-175)
// checks os.Stat on the archive path and returns "" when it's absent, causing
// Run to return noop. This validates Ruby Scenario 1 where first deployments
// skip ApplicationStop entirely.
func TestRun_FirstDeployment_ApplicationStopIsNoopWithoutArchive(t *testing.T) {
	// Create a deployment root WITHOUT a deployment-archive/ subdirectory.
	rootDir := t.TempDir()

	sr := &fakeScriptRunner{}
	runner := NewRunner(sr, slog.Default())

	result, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.ApplicationStop,
		DeploymentID:        "d-first",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   rootDir,
		LastSuccessfulDir:   "", // no previous deployment
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsNoop {
		t.Error("ApplicationStop should be noop when deployment root exists but has no deployment-archive subdir")
	}
	if len(sr.calls) != 0 {
		t.Errorf("no scripts should be called, got %d calls", len(sr.calls))
	}
}

// errScriptRunner is a ScriptRunner that returns a configured error.
// Used to test that Run propagates ScriptRunner errors correctly.
type errScriptRunner struct {
	err error
}

func (e *errScriptRunner) Run(_ context.Context, _ string, _ map[string]string, _ int) (ScriptResult, error) {
	return ScriptResult{}, e.err
}

// TestRun_ScriptError verifies that when the ScriptRunner returns an error,
// Run propagates it. This covers the error path in the script execution loop,
// ensuring failures from the process runner (e.g. binary not found, permission
// denied) are not silently swallowed.
func TestRun_ScriptError(t *testing.T) {
	appspec := `
version: 0.0
os: linux
hooks:
  BeforeInstall:
    - location: scripts/install.sh
      timeout: 60
`
	deployDir := setupDeployment(t, appspec)
	scriptErr := fmt.Errorf("process runner: exec failed")
	sr := &errScriptRunner{err: scriptErr}
	runner := NewRunner(sr, slog.Default())

	_, err := runner.Run(context.Background(), RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-script-err",
		ApplicationName:     "app",
		DeploymentGroupName: "grp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   deployDir,
	})
	if err == nil {
		t.Fatal("expected error from ScriptRunner to propagate through Run")
	}
}
