package tracker

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCreateAndRead verifies the basic create→read round-trip of tracking files.
// This is the primary path used during deployment command execution.
func TestCreateAndRead(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	if err := ft.Create("d-123", "hci-abc"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	hci := ft.InProgressCommand()
	if hci != "hci-abc" {
		t.Errorf("InProgressCommand = %q, want hci-abc", hci)
	}
}

// TestDeleteRemovesTracking verifies that Delete removes the tracking file,
// so no in-progress command is reported after cleanup.
func TestDeleteRemovesTracking(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	if err := ft.Create("d-123", "hci-abc"); err != nil {
		t.Fatal(err)
	}
	ft.Delete("d-123")

	hci := ft.InProgressCommand()
	if hci != "" {
		t.Errorf("InProgressCommand after delete = %q, want empty", hci)
	}
}

// TestStaleFileIsRemoved verifies that tracking files older than 24 hours
// are automatically cleaned up. This prevents stale deployments from blocking
// new deployments indefinitely.
func TestStaleFileIsRemoved(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	trackingDir := filepath.Join(dir, "ongoing")
	if err := os.MkdirAll(trackingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a file and backdate it beyond the 24h TTL
	path := filepath.Join(trackingDir, "d-old")
	if err := os.WriteFile(path, []byte("hci-stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-(staleTTL + time.Hour))
	if err := os.Chtimes(path, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	hci := ft.InProgressCommand()
	if hci != "" {
		t.Errorf("stale file should be ignored, got %q", hci)
	}

	// Verify the stale file was removed
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("stale file should have been removed")
	}
}

// TestMostRecentTakesPrecedence verifies that when multiple deployments are
// tracked, the most recently modified one is returned. This handles the case
// where the agent crashed during concurrent deployments.
func TestMostRecentTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	if err := ft.Create("d-first", "hci-first"); err != nil {
		t.Fatal(err)
	}

	// Ensure second file is newer
	time.Sleep(10 * time.Millisecond)

	if err := ft.Create("d-second", "hci-second"); err != nil {
		t.Fatal(err)
	}

	hci := ft.InProgressCommand()
	if hci != "hci-second" {
		t.Errorf("InProgressCommand = %q, want hci-second", hci)
	}
}

// TestCleanAll verifies that CleanAll removes the entire tracking directory.
func TestCleanAll(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	if err := ft.Create("d-123", "hci-abc"); err != nil {
		t.Fatal(err)
	}

	ft.CleanAll()

	trackingDir := filepath.Join(dir, "ongoing")
	if _, err := os.Stat(trackingDir); !os.IsNotExist(err) {
		t.Error("tracking directory should be removed")
	}
}

// TestEmptyDirectoryReturnsEmpty verifies that an empty tracking directory
// returns no in-progress command.
func TestEmptyDirectoryReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	hci := ft.InProgressCommand()
	if hci != "" {
		t.Errorf("empty dir should return empty, got %q", hci)
	}
}

// TestDeleteNonExistentIsNoOp verifies that deleting a tracking file that
// doesn't exist does not return an error or panic. The poller calls Delete
// after every command regardless of prior state.
func TestDeleteNonExistentIsNoOp(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())
	// Should not panic or log errors
	ft.Delete("d-nonexistent")
}

// TestCreateOverwritesExisting verifies that creating a tracking file for an
// already-tracked deployment overwrites the previous host command identifier.
// This handles re-entrancy when the same deployment is re-processed.
func TestCreateOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	if err := ft.Create("d-same", "hci-first"); err != nil {
		t.Fatal(err)
	}
	if err := ft.Create("d-same", "hci-second"); err != nil {
		t.Fatal(err)
	}

	hci := ft.InProgressCommand()
	if hci != "hci-second" {
		t.Errorf("InProgressCommand = %q, want hci-second", hci)
	}
}

// TestInProgressCommandSkipsSubdirectories verifies that subdirectories within
// the tracking directory are ignored (only regular files are tracking entries).
func TestInProgressCommandSkipsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	ft := NewFileTracker(dir, "ongoing", slog.Default())

	trackingDir := filepath.Join(dir, "ongoing")
	if err := os.MkdirAll(filepath.Join(trackingDir, "not-a-deployment"), 0o755); err != nil {
		t.Fatal(err)
	}

	hci := ft.InProgressCommand()
	if hci != "" {
		t.Errorf("should skip subdirectories, got %q", hci)
	}
}
