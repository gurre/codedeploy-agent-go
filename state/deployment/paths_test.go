package deployment

import (
	"path/filepath"
	"testing"
)

const testRoot = "/opt/codedeploy-agent/deployment-root"

// TestLayoutPathsAreConsistent verifies that all Layout methods produce paths
// rooted under DeploymentRootDir. This is a structural invariant: deployment
// artifacts must never leak outside their deployment directory.
func TestLayoutPathsAreConsistent(t *testing.T) {
	l := NewLayout(testRoot, "dg-123", "d-456")
	root := l.DeploymentRootDir()

	paths := []struct {
		name string
		path string
	}{
		{"ArchiveDir", l.ArchiveDir()},
		{"BundleFile", l.BundleFile()},
		{"ScriptLogFile", l.ScriptLogFile()},
		{"LogsDir", l.LogsDir()},
	}

	for _, tc := range paths {
		rel, err := filepath.Rel(root, tc.path)
		if err != nil || rel[:2] == ".." {
			t.Errorf("%s (%q) is not under DeploymentRootDir (%q)", tc.name, tc.path, root)
		}
	}
}

// TestLayoutDeploymentRootDir verifies the exact path format for deployment root.
func TestLayoutDeploymentRootDir(t *testing.T) {
	l := NewLayout(testRoot, "dg-123", "d-456")
	want := testRoot + "/dg-123/d-456"
	if got := l.DeploymentRootDir(); got != want {
		t.Errorf("DeploymentRootDir() = %q, want %q", got, want)
	}
}

// TestInstructionsDirIsShared verifies that instruction paths are shared
// across deployments (not per-deployment-group or per-deployment).
func TestInstructionsDirIsShared(t *testing.T) {
	dir := InstructionsDir(testRoot)
	want := testRoot + "/deployment-instructions"
	if dir != want {
		t.Errorf("InstructionsDir() = %q, want %q", dir, want)
	}
}

// TestCleanupAndInstallFilesIncludeGroupID verifies that cleanup/install file
// names are scoped by deployment group ID to prevent cross-group conflicts.
func TestCleanupAndInstallFilesIncludeGroupID(t *testing.T) {
	cleanup := CleanupFile(testRoot, "dg-ABC")
	if cleanup != testRoot+"/deployment-instructions/dg-ABC-cleanup" {
		t.Errorf("CleanupFile = %q", cleanup)
	}

	install := InstallFile(testRoot, "dg-ABC")
	if install != testRoot+"/deployment-instructions/dg-ABC-install.json" {
		t.Errorf("InstallFile = %q", install)
	}
}

// TestLastSuccessfulAndMostRecentFiles verifies the tracking file paths.
func TestLastSuccessfulAndMostRecentFiles(t *testing.T) {
	ls := LastSuccessfulFile(testRoot, "dg-1")
	want := testRoot + "/deployment-instructions/dg-1_last_successful_install"
	if ls != want {
		t.Errorf("LastSuccessfulFile = %q, want %q", ls, want)
	}

	mr := MostRecentFile(testRoot, "dg-1")
	want = testRoot + "/deployment-instructions/dg-1_most_recent_install"
	if mr != want {
		t.Errorf("MostRecentFile = %q, want %q", mr, want)
	}
}

// TestOngoingDeploymentDir verifies the tracking directory path.
func TestOngoingDeploymentDir(t *testing.T) {
	dir := OngoingDeploymentDir(testRoot, "ongoing-deployment")
	want := testRoot + "/ongoing-deployment"
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
}

// TestDeploymentLogsDirAndFile verifies that the shared deployment-logs paths
// are under the root directory. The Ruby feature tests assert these exist.
func TestDeploymentLogsDirAndFile(t *testing.T) {
	dir := DeploymentLogsDir(testRoot)
	want := testRoot + "/deployment-logs"
	if dir != want {
		t.Errorf("DeploymentLogsDir = %q, want %q", dir, want)
	}

	file := DeploymentLogFile(testRoot)
	want = testRoot + "/deployment-logs/codedeploy-agent-deployments.log"
	if file != want {
		t.Errorf("DeploymentLogFile = %q, want %q", file, want)
	}
}

// TestLogsDir verifies the per-deployment logs directory path.
func TestLogsDir(t *testing.T) {
	l := NewLayout(testRoot, "dg-123", "d-456")
	want := testRoot + "/dg-123/d-456/logs"
	if got := l.LogsDir(); got != want {
		t.Errorf("LogsDir() = %q, want %q", got, want)
	}
}

// TestGroupDir verifies the deployment group directory path, which contains
// all deployment directories for a given group.
func TestGroupDir(t *testing.T) {
	l := NewLayout(testRoot, "dg-ABC", "d-789")
	want := testRoot + "/dg-ABC"
	if got := l.GroupDir(); got != want {
		t.Errorf("GroupDir() = %q, want %q", got, want)
	}
}

// TestOngoingDeploymentFile verifies the tracking file path for a specific
// deployment, used by the crash recovery system to detect in-progress commands.
func TestOngoingDeploymentFile(t *testing.T) {
	got := OngoingDeploymentFile(testRoot, "ongoing-deployment", "d-456")
	want := testRoot + "/ongoing-deployment/d-456"
	if got != want {
		t.Errorf("OngoingDeploymentFile() = %q, want %q", got, want)
	}
}
