package executor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gurre/codedeploy-agent-go/logic/appspec"
	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
	"github.com/gurre/codedeploy-agent-go/state/deployment"
)

const testAppspec = `version: 0.0
os: linux
files:
  - source: /
    destination: /opt/app
`

// newTestExecutor wires up an Executor with the given mocks and temp dir.
// It creates the instructions directory so pointer files can be written.
func newTestExecutor(t *testing.T, dl BundleDownloader, unpacker ArchiveUnpacker, hookRunner HookRunner, inst Installer, fileOp FileOperator, rootDir string) *Executor {
	t.Helper()
	instrDir := deployment.InstructionsDir(rootDir)
	if err := os.MkdirAll(instrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return NewExecutor(dl, unpacker, hookRunner, inst, fileOp, rootDir, lifecycle.DefaultHookMapping(), 5, slog.Default())
}

// s3Spec returns a deployspec.Spec configured for an S3 source.
func s3Spec() deployspec.Spec {
	return deployspec.Spec{
		DeploymentID:        "d-100",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionS3,
		Bucket:              "my-bucket",
		Key:                 "app.tar",
		Version:             "v1",
		ETag:                "abc123",
		BundleType:          "tar",
		FileExistsBehavior:  "DISALLOW",
	}
}

// localDirSpec returns a deployspec.Spec for a local directory source.
func localDirSpec(localPath string) deployspec.Spec {
	return deployspec.Spec{
		DeploymentID:        "d-200",
		DeploymentGroupID:   "dg-2",
		DeploymentGroupName: "staging",
		ApplicationName:     "localapp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionLocalDirectory,
		LocalLocation:       localPath,
		BundleType:          "directory",
		FileExistsBehavior:  "OVERWRITE",
	}
}

// writeAppspec creates an appspec.yml in the given directory.
func writeAppspec(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "appspec.yml"), []byte(testAppspec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExecute_DownloadBundle_S3 verifies that an S3 download command calls
// the downloader with the correct bucket/key/version/etag, unpacks the bundle
// into the archive directory, and writes the most-recent pointer file.
// This test exists because DownloadBundle is the critical first step of every
// S3-sourced deployment; incorrect arguments or missing unpack would break all
// subsequent lifecycle phases.
func TestExecute_DownloadBundle_S3(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "DownloadBundle", spec)
	if err != nil {
		t.Fatalf("Execute DownloadBundle: %v", err)
	}

	// Verify downloader was called with correct S3 parameters
	if len(dl.s3Calls) != 1 {
		t.Fatalf("expected 1 S3 download call, got %d", len(dl.s3Calls))
	}
	call := dl.s3Calls[0]
	if call.bucket != spec.Bucket || call.key != spec.Key || call.version != spec.Version || call.etag != spec.ETag {
		t.Errorf("S3 call mismatch: got bucket=%q key=%q version=%q etag=%q", call.bucket, call.key, call.version, call.etag)
	}

	// Verify unpacker was called
	if len(unpacker.calls) != 1 {
		t.Fatalf("expected 1 unpack call, got %d", len(unpacker.calls))
	}
	if unpacker.calls[0].bundleType != "tar" {
		t.Errorf("expected bundleType=tar, got %q", unpacker.calls[0].bundleType)
	}

	// Verify most-recent pointer file was written
	pointerPath := deployment.MostRecentFile(rootDir, spec.DeploymentGroupID)
	data, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("reading most-recent pointer: %v", err)
	}
	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	if string(data) != layout.DeploymentRootDir() {
		t.Errorf("most-recent pointer = %q, want %q", string(data), layout.DeploymentRootDir())
	}
}

// TestExecute_DownloadBundle_LocalDirectory verifies that a local directory
// source copies the source tree into the archive directory and does NOT call
// the unpacker (since the bundle is already a directory). The most-recent
// pointer must still be written.
// This test exists because local directory deployments skip both download and
// unpack, so we need to ensure the copy path works and the pointer is updated.
func TestExecute_DownloadBundle_LocalDirectory(t *testing.T) {
	rootDir := t.TempDir()

	// Create a source directory with a marker file
	srcDir := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "marker.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := localDirSpec(srcDir)
	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "DownloadBundle", spec)
	if err != nil {
		t.Fatalf("Execute DownloadBundle LocalDirectory: %v", err)
	}

	// Downloader should not have been called
	if len(dl.s3Calls) != 0 || len(dl.githubCalls) != 0 {
		t.Error("downloader should not be called for local directory")
	}

	// Unpacker should not have been called (bundle type is "directory")
	if len(unpacker.calls) != 0 {
		t.Error("unpacker should not be called for directory bundle")
	}

	// Verify the marker file was copied
	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	markerPath := filepath.Join(layout.ArchiveDir(), "marker.txt")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading copied marker file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("marker content = %q, want %q", string(data), "hello")
	}

	// Verify most-recent pointer
	pointerPath := deployment.MostRecentFile(rootDir, spec.DeploymentGroupID)
	pointerData, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("reading most-recent pointer: %v", err)
	}
	if string(pointerData) != layout.DeploymentRootDir() {
		t.Errorf("most-recent pointer = %q, want %q", string(pointerData), layout.DeploymentRootDir())
	}
}

// TestExecute_Install verifies that the Install command parses the appspec
// from disk, calls the installer with the correct arguments, and writes the
// last-successful pointer.
// This test exists because Install is the command that applies file mappings
// to the target host; a failure to find or parse the appspec, or to call the
// installer correctly, means files never arrive on disk.
func TestExecute_Install(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	// Create the archive directory with a valid appspec.yml on disk
	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	writeAppspec(t, layout.ArchiveDir())

	_, err := exec.Execute(context.Background(), "Install", spec)
	if err != nil {
		t.Fatalf("Execute Install: %v", err)
	}

	// Verify installer was called once with correct deployment group ID
	if len(inst.calls) != 1 {
		t.Fatalf("expected 1 install call, got %d", len(inst.calls))
	}
	ic := inst.calls[0]
	if ic.deploymentGroupID != spec.DeploymentGroupID {
		t.Errorf("install deploymentGroupID = %q, want %q", ic.deploymentGroupID, spec.DeploymentGroupID)
	}
	if ic.archiveDir != layout.ArchiveDir() {
		t.Errorf("install archiveDir = %q, want %q", ic.archiveDir, layout.ArchiveDir())
	}
	if ic.fileExistsBehavior != spec.FileExistsBehavior {
		t.Errorf("install fileExistsBehavior = %q, want %q", ic.fileExistsBehavior, spec.FileExistsBehavior)
	}

	// Verify the parsed appspec has the expected file mapping
	if len(ic.spec.Files) != 1 {
		t.Fatalf("expected 1 file mapping in parsed appspec, got %d", len(ic.spec.Files))
	}
	if ic.spec.Files[0].Destination != "/opt/app" {
		t.Errorf("file destination = %q, want %q", ic.spec.Files[0].Destination, "/opt/app")
	}

	// Verify last-successful pointer was written
	pointerPath := deployment.LastSuccessfulFile(rootDir, spec.DeploymentGroupID)
	data, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("reading last-successful pointer: %v", err)
	}
	if string(data) != layout.DeploymentRootDir() {
		t.Errorf("last-successful pointer = %q, want %q", string(data), layout.DeploymentRootDir())
	}
}

// TestExecute_HookDispatch verifies that a command name found in the hook
// mapping dispatches each mapped lifecycle event to the hook runner in order.
// This test exists because the executor is the bridge between command names
// (from the poller/service) and lifecycle events (from the appspec); incorrect
// mapping or ordering would run hooks at the wrong deployment stage.
func TestExecute_HookDispatch(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "BeforeInstall", spec)
	if err != nil {
		t.Fatalf("Execute BeforeInstall: %v", err)
	}

	// The default hook mapping maps "BeforeInstall" to [BeforeInstall]
	if len(hookRunner.runCalls) != 1 {
		t.Fatalf("expected 1 hook run call, got %d", len(hookRunner.runCalls))
	}
	if hookRunner.runCalls[0].LifecycleEvent != lifecycle.BeforeInstall {
		t.Errorf("lifecycle event = %q, want %q", hookRunner.runCalls[0].LifecycleEvent, lifecycle.BeforeInstall)
	}
	if hookRunner.runCalls[0].DeploymentID != spec.DeploymentID {
		t.Errorf("deployment ID = %q, want %q", hookRunner.runCalls[0].DeploymentID, spec.DeploymentID)
	}
}

// TestExecute_UnknownCommand verifies that an unrecognized command name is
// treated as a no-op (returns no error and no output).
// This test exists because the CodeDeploy service may introduce new commands
// in the future; the agent must not crash on unknown commands.
func TestExecute_UnknownCommand(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	output, err := exec.Execute(context.Background(), "SomeUnknownCommand", spec)
	if err != nil {
		t.Fatalf("Execute unknown command: %v", err)
	}
	if output != "" {
		t.Errorf("expected empty output for unknown command, got %q", output)
	}
	if len(hookRunner.runCalls) != 0 {
		t.Error("hook runner should not be called for unknown command")
	}
}

// TestIsNoop_DownloadBundle_AlwaysFalse verifies that DownloadBundle and
// Install always return false from IsNoop, regardless of the spec contents.
// This test exists because these two commands always perform work (downloading
// and installing); skipping them would leave the deployment in an incomplete
// state.
func TestIsNoop_DownloadBundle_AlwaysFalse(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	if exec.IsNoop("DownloadBundle", spec) {
		t.Error("DownloadBundle must never be noop")
	}
	if exec.IsNoop("Install", spec) {
		t.Error("Install must never be noop")
	}
}

// TestIsNoop_HookWithScripts verifies that IsNoop returns false when the
// hook runner reports that the event has scripts to run.
// This test exists because the poller uses IsNoop to decide whether to skip
// a command; returning true incorrectly would cause hook scripts to be dropped.
func TestIsNoop_HookWithScripts(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	// This hook runner reports non-noop for BeforeInstall
	hookRunner := &fakeHookRunner{
		noopResults: map[lifecycle.Event]bool{
			lifecycle.BeforeInstall: false,
		},
	}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	if exec.IsNoop("BeforeInstall", spec) {
		t.Error("BeforeInstall with scripts must not be noop")
	}

	// Verify that an event whose hook runner says noop=true returns true
	hookRunner.noopResults[lifecycle.BeforeInstall] = true
	if !exec.IsNoop("BeforeInstall", spec) {
		t.Error("BeforeInstall without scripts should be noop")
	}
}

// TestCleanupOldArchives verifies that cleanup retains at most maxRevisions
// deployment directories within a group, always preserves the last-successful
// directory, and removes the oldest directories first.
// This test exists because unbounded archive growth would fill the disk;
// preserving last-successful is required for rollback hooks.
func TestCleanupOldArchives(t *testing.T) {
	rootDir := t.TempDir()
	groupID := "dg-cleanup"
	groupDir := filepath.Join(rootDir, groupID)

	// Create the instructions directory so pointer files can be written
	instrDir := deployment.InstructionsDir(rootDir)
	if err := os.MkdirAll(instrDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create 6 deployment directories with staggered modification times.
	// With maxRevisions=3 and the current deployment excluded from candidates,
	// extra = len(candidates) - maxRevisions + 1.
	// 6 dirs exist, current ("d-current") is excluded => 6 candidates.
	// extra = 6 - 3 + 1 = 4. After filtering out last-successful ("d-protected"),
	// 5 remain in sorted order: d-oldest, d-old, d-mid, d-recent, d-newer.
	// The 4 oldest non-protected get removed, leaving d-newer and d-protected.
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	dirs := []string{"d-oldest", "d-old", "d-protected", "d-mid", "d-recent", "d-newer"}
	for i, name := range dirs {
		dirPath := filepath.Join(groupDir, name)
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			t.Fatal(err)
		}
		modTime := baseTime.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(dirPath, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	// Mark "d-protected" as last-successful so it must survive cleanup
	lastSuccessfulPath := deployment.LastSuccessfulFile(rootDir, groupID)
	protectedDir := filepath.Join(groupDir, "d-protected")
	if err := os.WriteFile(lastSuccessfulPath, []byte(protectedDir), 0o644); err != nil {
		t.Fatal(err)
	}

	// The spec triggers cleanup for a new deployment "d-current"
	spec := deployspec.Spec{
		DeploymentID:        "d-current",
		DeploymentGroupID:   groupID,
		DeploymentGroupName: "cleanup-grp",
		ApplicationName:     "cleanupapp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionS3,
		Bucket:              "b",
		Key:                 "k",
		Version:             "v",
		ETag:                "e",
		BundleType:          "tar",
		FileExistsBehavior:  "DISALLOW",
	}

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := NewExecutor(dl, unpacker, hookRunner, inst, fileOp, rootDir, lifecycle.DefaultHookMapping(), 3, slog.Default())

	// Invoke cleanupOldArchives directly
	exec.cleanupOldArchives(spec)

	// Verify which directories remain
	entries, err := os.ReadDir(groupDir)
	if err != nil {
		t.Fatalf("reading group dir: %v", err)
	}

	remaining := make(map[string]bool, len(entries))
	for _, e := range entries {
		remaining[e.Name()] = true
	}

	// d-protected must survive (last-successful pointer)
	if !remaining["d-protected"] {
		t.Error("d-protected (last-successful) should be preserved")
	}

	// d-newer must survive (newest, within the retained set)
	if !remaining["d-newer"] {
		t.Error("d-newer should be preserved as the newest directory")
	}

	// d-oldest, d-old, d-mid, d-recent should all be removed (oldest non-protected)
	for _, name := range []string{"d-oldest", "d-old", "d-mid", "d-recent"} {
		if remaining[name] {
			t.Errorf("%s should have been removed", name)
		}
	}
}

// TestExecute_DownloadBundle_CreatesDeploymentLogsDir verifies that the
// DownloadBundle command creates the shared deployment-logs/ directory and
// writes an entry to codedeploy-agent-deployments.log. The Ruby feature tests
// (common_steps.rb:62-70) assert these exist after every deployment.
func TestExecute_DownloadBundle_CreatesDeploymentLogsDir(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "DownloadBundle", spec)
	if err != nil {
		t.Fatalf("Execute DownloadBundle: %v", err)
	}

	// Verify deployment-logs directory exists
	logsDir := deployment.DeploymentLogsDir(rootDir)
	if _, err := os.Stat(logsDir); err != nil {
		t.Errorf("deployment-logs dir should exist: %v", err)
	}

	// Verify log file exists and contains the deployment ID
	logFile := deployment.DeploymentLogFile(rootDir)
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading deployment log file: %v", err)
	}
	if !strings.Contains(string(data), spec.DeploymentID) {
		t.Errorf("deployment log should contain %q, got %q", spec.DeploymentID, string(data))
	}
}

// TestExecute_DownloadBundle_CreatesPerDeploymentLogsDir verifies that each
// deployment gets its own logs/ subdirectory. The Ruby feature tests
// (common_steps.rb:78) assert this exists for each deployment.
func TestExecute_DownloadBundle_CreatesPerDeploymentLogsDir(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "DownloadBundle", spec)
	if err != nil {
		t.Fatalf("Execute DownloadBundle: %v", err)
	}

	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	logsDir := layout.LogsDir()
	if _, err := os.Stat(logsDir); err != nil {
		t.Errorf("per-deployment logs dir should exist: %v", err)
	}
}

// TestExecute_Hook_WritesScriptLog verifies that hook execution output is
// written to the per-deployment script log file.
func TestExecute_Hook_WritesScriptLog(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	// Create per-deployment logs directory (normally done by DownloadBundle)
	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	if err := os.MkdirAll(layout.LogsDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := exec.Execute(context.Background(), "BeforeInstall", spec)
	if err != nil {
		t.Fatalf("Execute BeforeInstall: %v", err)
	}

	logFile := layout.ScriptLogFile()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading script log: %v", err)
	}
	if string(data) != "ok\n" {
		t.Errorf("script log content = %q, want %q", string(data), "ok\n")
	}
}

// TestExecute_SecondDeployment_LastSuccessfulDirPointsToFirst verifies that
// when a second deployment runs ApplicationStop, the hook runner receives the
// first deployment's root dir as LastSuccessfulDir. The Install command for the
// first deployment writes the last-successful pointer (executor.go:227), and
// buildHookArgs (executor.go:249-264) reads it back via readPointer. This
// validates Ruby Scenario 2 where the second deployment runs ApplicationStop
// against the first deployment's archive.
func TestExecute_SecondDeployment_LastSuccessfulDirPointsToFirst(t *testing.T) {
	rootDir := t.TempDir()

	firstSpec := deployspec.Spec{
		DeploymentID:        "d-first",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionS3,
		Bucket:              "b",
		Key:                 "k",
		Version:             "v1",
		ETag:                "e1",
		BundleType:          "tar",
		FileExistsBehavior:  "OVERWRITE",
	}

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	// Execute DownloadBundle + Install for the first deployment
	if _, err := exec.Execute(context.Background(), "DownloadBundle", firstSpec); err != nil {
		t.Fatalf("DownloadBundle d-first: %v", err)
	}
	firstLayout := deployment.NewLayout(rootDir, firstSpec.DeploymentGroupID, firstSpec.DeploymentID)
	writeAppspec(t, firstLayout.ArchiveDir())
	if _, err := exec.Execute(context.Background(), "Install", firstSpec); err != nil {
		t.Fatalf("Install d-first: %v", err)
	}

	// Now execute ApplicationStop for a second deployment in the same group
	secondSpec := deployspec.Spec{
		DeploymentID:        "d-second",
		DeploymentGroupID:   "dg-1",
		DeploymentGroupName: "prod",
		ApplicationName:     "myapp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionS3,
		Bucket:              "b",
		Key:                 "k",
		Version:             "v2",
		ETag:                "e2",
		BundleType:          "tar",
		FileExistsBehavior:  "OVERWRITE",
	}

	if _, err := exec.Execute(context.Background(), "ApplicationStop", secondSpec); err != nil {
		t.Fatalf("ApplicationStop d-second: %v", err)
	}

	// The hookRunner should have been called for ApplicationStop with
	// LastSuccessfulDir pointing to the first deployment's root.
	var stopCall *HookRunArgs
	for i := range hookRunner.runCalls {
		if hookRunner.runCalls[i].LifecycleEvent == lifecycle.ApplicationStop {
			stopCall = &hookRunner.runCalls[i]
			break
		}
	}
	if stopCall == nil {
		t.Fatal("expected ApplicationStop hook call, got none")
	}
	if stopCall.LastSuccessfulDir != firstLayout.DeploymentRootDir() {
		t.Errorf("LastSuccessfulDir = %q, want %q", stopCall.LastSuccessfulDir, firstLayout.DeploymentRootDir())
	}
}

// TestExecute_LifecycleEventsDispatchInOrder verifies that executing all 9
// lifecycle command names dispatches them to the hook runner in the canonical
// order defined by lifecycle.DefaultOrderedEvents(). This is a property test:
// the ordering invariant must be preserved through the executor dispatch layer.
// Validates Ruby Scenarios 1 & 2 where scripts execute in exact lifecycle order.
func TestExecute_LifecycleEventsDispatchInOrder(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	// Execute all 9 lifecycle commands in order
	orderedEvents := lifecycle.DefaultOrderedEvents()
	for _, event := range orderedEvents {
		commandName := string(event)
		if _, err := exec.Execute(context.Background(), commandName, spec); err != nil {
			t.Fatalf("Execute %s: %v", commandName, err)
		}
	}

	if len(hookRunner.runCalls) != len(orderedEvents) {
		t.Fatalf("expected %d hook calls, got %d", len(orderedEvents), len(hookRunner.runCalls))
	}
	for i, call := range hookRunner.runCalls {
		if call.LifecycleEvent != orderedEvents[i] {
			t.Errorf("call[%d].LifecycleEvent = %q, want %q", i, call.LifecycleEvent, orderedEvents[i])
		}
	}
}

// TestExecute_DownloadBundle_LocalDirectory_ProducesRegularFiles verifies that
// a local directory deployment copies files as regular files, not symlinks.
// copyDir (executor.go:386-402) uses os.ReadFile + os.WriteFile, producing
// independent file copies. This validates Ruby Scenarios 1 & 3 where local
// directory deployments must produce independent file copies, and guards against
// regression if copyDir were ever changed to use symlinks.
func TestExecute_DownloadBundle_LocalDirectory_ProducesRegularFiles(t *testing.T) {
	rootDir := t.TempDir()

	srcDir := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "app.bin"), []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := localDirSpec(srcDir)
	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	if _, err := exec.Execute(context.Background(), "DownloadBundle", spec); err != nil {
		t.Fatalf("Execute DownloadBundle LocalDirectory: %v", err)
	}

	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	copiedFile := filepath.Join(layout.ArchiveDir(), "app.bin")

	info, err := os.Lstat(copiedFile)
	if err != nil {
		t.Fatalf("Lstat copied file: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("copied file should be regular, got mode %v", info.Mode())
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("copied file should not be a symlink")
	}
}

// ---------------------------------------------------------------------------
// Mock types (placed at bottom per project convention)
// ---------------------------------------------------------------------------

// s3Call records the parameters of a single DownloadS3 invocation.
type s3Call struct {
	bucket  string
	key     string
	version string
	etag    string
	dest    string
}

// githubCall records the parameters of a single DownloadGitHub invocation.
type githubCall struct {
	account    string
	repo       string
	commit     string
	bundleType string
	token      string
	dest       string
}

// fakeBundleDownloader records download calls and creates empty bundle files.
type fakeBundleDownloader struct {
	s3Calls     []s3Call
	githubCalls []githubCall
}

func (f *fakeBundleDownloader) DownloadS3(_ context.Context, bucket, key, version, etag, destPath string) error {
	f.s3Calls = append(f.s3Calls, s3Call{
		bucket:  bucket,
		key:     key,
		version: version,
		etag:    etag,
		dest:    destPath,
	})
	// Create the bundle file so downstream code finds it
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destPath, []byte("fake-bundle"), 0o644)
}

func (f *fakeBundleDownloader) DownloadGitHub(_ context.Context, account, repo, commit, bundleType, token, destPath string) error {
	f.githubCalls = append(f.githubCalls, githubCall{
		account:    account,
		repo:       repo,
		commit:     commit,
		bundleType: bundleType,
		token:      token,
		dest:       destPath,
	})
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destPath, []byte("fake-bundle"), 0o644)
}

// unpackCall records the parameters of a single Unpack invocation.
type unpackCall struct {
	archivePath string
	destDir     string
	bundleType  string
}

// fakeArchiveUnpacker records unpack calls and creates the destination directory.
type fakeArchiveUnpacker struct {
	calls []unpackCall
}

func (f *fakeArchiveUnpacker) Unpack(archivePath, destDir, bundleType string) error {
	f.calls = append(f.calls, unpackCall{
		archivePath: archivePath,
		destDir:     destDir,
		bundleType:  bundleType,
	})
	// Create the archive directory so downstream code finds it
	return os.MkdirAll(destDir, 0o755)
}

// fakeHookRunner records Run calls and returns configurable IsNoop results.
type fakeHookRunner struct {
	runCalls    []HookRunArgs
	noopResults map[lifecycle.Event]bool
}

func (f *fakeHookRunner) Run(_ context.Context, args HookRunArgs) (HookResult, error) {
	f.runCalls = append(f.runCalls, args)
	return HookResult{Log: "ok\n"}, nil
}

func (f *fakeHookRunner) IsNoop(args HookRunArgs) (bool, error) {
	if f.noopResults == nil {
		return true, nil
	}
	noop, ok := f.noopResults[args.LifecycleEvent]
	if !ok {
		return true, nil
	}
	return noop, nil
}

// installCall records the parameters of a single Install invocation.
type installCall struct {
	deploymentGroupID  string
	archiveDir         string
	instructionsDir    string
	spec               appspec.Spec
	fileExistsBehavior string
}

// fakeInstaller records install calls without performing real file operations.
type fakeInstaller struct {
	calls []installCall
}

func (f *fakeInstaller) Install(deploymentGroupID, archiveDir, instructionsDir string, spec appspec.Spec, fileExistsBehavior string) error {
	f.calls = append(f.calls, installCall{
		deploymentGroupID:  deploymentGroupID,
		archiveDir:         archiveDir,
		instructionsDir:    instructionsDir,
		spec:               spec,
		fileExistsBehavior: fileExistsBehavior,
	})
	return nil
}

// realFileOperator performs actual filesystem operations so that paths created
// by MkdirAll are available to the code under test.
type realFileOperator struct{}

func (r *realFileOperator) MkdirAll(path string) error  { return os.MkdirAll(path, 0o755) }
func (r *realFileOperator) RemoveAll(path string) error { return os.RemoveAll(path) }

// TestNewExecutor_InvalidMaxRevisions verifies that maxRevisions < 1 is
// corrected to the default of 5. This guards against misconfiguration that
// would cause cleanup to remove all revisions.
func TestNewExecutor_InvalidMaxRevisions(t *testing.T) {
	exec := NewExecutor(nil, nil, nil, nil, nil, "/tmp", nil, 0, slog.Default())
	if exec.maxRevisions != 5 {
		t.Errorf("maxRevisions = %d, want 5 (default)", exec.maxRevisions)
	}
	exec = NewExecutor(nil, nil, nil, nil, nil, "/tmp", nil, -1, slog.Default())
	if exec.maxRevisions != 5 {
		t.Errorf("maxRevisions = %d, want 5 (default)", exec.maxRevisions)
	}
}

// TestExecute_DownloadBundle_LocalFile verifies that a local file source
// creates a symlink from the local path to the bundle file location. This
// avoids copying large archives unnecessarily for local deployments.
func TestExecute_DownloadBundle_LocalFile(t *testing.T) {
	rootDir := t.TempDir()

	// Create a local archive file
	localFile := filepath.Join(t.TempDir(), "app.tar")
	if err := os.WriteFile(localFile, []byte("archive-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := deployspec.Spec{
		DeploymentID:        "d-400",
		DeploymentGroupID:   "dg-4",
		DeploymentGroupName: "local",
		ApplicationName:     "localapp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionLocalFile,
		LocalLocation:       localFile,
		BundleType:          "tar",
		FileExistsBehavior:  "OVERWRITE",
	}

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "DownloadBundle", spec)
	if err != nil {
		t.Fatalf("Execute DownloadBundle LocalFile: %v", err)
	}

	// Verify symlink was created
	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	linkTarget, err := os.Readlink(layout.BundleFile())
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if linkTarget != localFile {
		t.Errorf("symlink target = %q, want %q", linkTarget, localFile)
	}

	// Verify downloader was NOT called
	if len(dl.s3Calls) != 0 || len(dl.githubCalls) != 0 {
		t.Error("downloader should not be called for local file source")
	}

	// Verify unpacker was called (local file still needs unpacking)
	if len(unpacker.calls) != 1 {
		t.Fatalf("expected 1 unpack call, got %d", len(unpacker.calls))
	}
}

// TestExecute_DownloadBundle_UnknownSource verifies that an unrecognized
// revision source returns a clear error.
func TestExecute_DownloadBundle_UnknownSource(t *testing.T) {
	rootDir := t.TempDir()

	spec := deployspec.Spec{
		DeploymentID:        "d-500",
		DeploymentGroupID:   "dg-5",
		DeploymentGroupName: "unknown",
		ApplicationName:     "app",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              "Nonexistent Source",
		BundleType:          "tar",
		FileExistsBehavior:  "DISALLOW",
	}

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "DownloadBundle", spec)
	if err == nil {
		t.Fatal("expected error for unknown revision source")
	}
	if !strings.Contains(err.Error(), "unknown revision source") {
		t.Errorf("error = %v, want mention of unknown revision source", err)
	}
}

// TestExecute_Install_MissingAppspec verifies that Install returns an error
// when the appspec file is not found in the archive directory.
func TestExecute_Install_MissingAppspec(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	// Create archive directory without appspec
	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	if err := os.MkdirAll(layout.ArchiveDir(), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := exec.Execute(context.Background(), "Install", spec)
	if err == nil {
		t.Fatal("expected error for missing appspec")
	}
}

// Compile-time interface satisfaction checks.
var (
	_ BundleDownloader = (*fakeBundleDownloader)(nil)
	_ ArchiveUnpacker  = (*fakeArchiveUnpacker)(nil)
	_ HookRunner       = (*fakeHookRunner)(nil)
	_ Installer        = (*fakeInstaller)(nil)
	_ FileOperator     = (*realFileOperator)(nil)
)

// Ensure lifecycle and deployspec imports are exercised (avoid unused import errors).
var (
	_ = lifecycle.DefaultHookMapping
	_ = strings.TrimSpace
)

// githubSpec returns a deployspec.Spec configured for a GitHub source.
func githubSpec() deployspec.Spec {
	return deployspec.Spec{
		DeploymentID:        "d-300",
		DeploymentGroupID:   "dg-3",
		DeploymentGroupName: "staging",
		ApplicationName:     "ghapp",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionGitHub,
		Account:             "octocat",
		Repository:          "hello-world",
		CommitID:            "abc123def",
		BundleType:          "tar",
		ExternalAuthToken:   "ghp_token123",
		FileExistsBehavior:  "OVERWRITE",
	}
}

// TestExecute_DownloadBundle_GitHub verifies that a GitHub download command
// calls the downloader with the correct account, repo, commit, bundleType,
// and token. This covers the RevisionGitHub branch in downloadBundle which
// is the only path that exercises DownloadGitHub on the BundleDownloader.
func TestExecute_DownloadBundle_GitHub(t *testing.T) {
	rootDir := t.TempDir()
	spec := githubSpec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	_, err := exec.Execute(context.Background(), "DownloadBundle", spec)
	if err != nil {
		t.Fatalf("Execute DownloadBundle GitHub: %v", err)
	}

	// Verify downloader was called with correct GitHub parameters
	if len(dl.githubCalls) != 1 {
		t.Fatalf("expected 1 GitHub download call, got %d", len(dl.githubCalls))
	}
	call := dl.githubCalls[0]
	if call.account != spec.Account {
		t.Errorf("account = %q, want %q", call.account, spec.Account)
	}
	if call.repo != spec.Repository {
		t.Errorf("repo = %q, want %q", call.repo, spec.Repository)
	}
	if call.commit != spec.CommitID {
		t.Errorf("commit = %q, want %q", call.commit, spec.CommitID)
	}
	if call.bundleType != spec.BundleType {
		t.Errorf("bundleType = %q, want %q", call.bundleType, spec.BundleType)
	}
	if call.token != spec.ExternalAuthToken {
		t.Errorf("token = %q, want %q", call.token, spec.ExternalAuthToken)
	}

	// Verify S3 downloader was NOT called
	if len(dl.s3Calls) != 0 {
		t.Error("S3 downloader should not be called for GitHub source")
	}

	// Verify unpacker was called (GitHub bundles are archives, not directories)
	if len(unpacker.calls) != 1 {
		t.Fatalf("expected 1 unpack call, got %d", len(unpacker.calls))
	}

	// Verify most-recent pointer was written
	pointerPath := deployment.MostRecentFile(rootDir, spec.DeploymentGroupID)
	data, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("reading most-recent pointer: %v", err)
	}
	layout := deployment.NewLayout(rootDir, spec.DeploymentGroupID, spec.DeploymentID)
	if string(data) != layout.DeploymentRootDir() {
		t.Errorf("most-recent pointer = %q, want %q", string(data), layout.DeploymentRootDir())
	}
}

// TestRevisionEnvs_GitHub verifies that revisionEnvs returns a map with
// BUNDLE_COMMIT when the source is RevisionGitHub. The executor passes these
// environment variables to hook scripts, so the correct key/value mapping
// is essential for scripts that need the commit SHA.
func TestRevisionEnvs_GitHub(t *testing.T) {
	spec := githubSpec()
	envs := revisionEnvs(spec)

	if envs == nil {
		t.Fatal("revisionEnvs returned nil for GitHub source")
	}
	if commit, ok := envs["BUNDLE_COMMIT"]; !ok {
		t.Error("missing BUNDLE_COMMIT key")
	} else if commit != spec.CommitID {
		t.Errorf("BUNDLE_COMMIT = %q, want %q", commit, spec.CommitID)
	}
	if len(envs) != 1 {
		t.Errorf("expected 1 env var for GitHub, got %d", len(envs))
	}
}

// TestRevisionEnvs_LocalDirectory verifies that revisionEnvs returns nil when
// the source is RevisionLocalDirectory. Local deployments have no remote
// coordinates to pass as environment variables. Returning nil (not an empty
// map) avoids unnecessary allocation in buildEnv.
func TestRevisionEnvs_LocalDirectory(t *testing.T) {
	spec := localDirSpec("/some/path")
	envs := revisionEnvs(spec)

	if envs != nil {
		t.Errorf("expected nil envs for LocalDirectory, got %v", envs)
	}
}

// TestIsNoop_UnknownCommand verifies that IsNoop returns true for a command
// name that is not present in the hook mapping. Unknown commands produce no
// work, so signaling noop lets the poller skip acknowledgement overhead.
func TestIsNoop_UnknownCommand(t *testing.T) {
	rootDir := t.TempDir()
	spec := s3Spec()

	dl := &fakeBundleDownloader{}
	unpacker := &fakeArchiveUnpacker{}
	hookRunner := &fakeHookRunner{}
	inst := &fakeInstaller{}
	fileOp := &realFileOperator{}

	exec := newTestExecutor(t, dl, unpacker, hookRunner, inst, fileOp, rootDir)

	if !exec.IsNoop("CompletelyUnknownCommand", spec) {
		t.Error("unknown command should be noop")
	}
}
