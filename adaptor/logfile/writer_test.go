package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestRotation_TriggersAtSizeBoundary verifies that when a write would exceed
// maxBytes the writer rotates: the current file becomes .1 and a fresh file
// is opened. This is the core invariant of size-based rotation.
func TestRotation_TriggersAtSizeBoundary(t *testing.T) {
	dir := t.TempDir()
	name := "test.log"
	maxBytes := int64(100)

	w := NewRotatingWriter(dir, name, maxBytes, 3)
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Write enough to trigger one rotation
	data := make([]byte, 60)
	for i := range data {
		data[i] = 'A'
	}

	// First write: 60 bytes, under limit
	if _, err := w.Write(data); err != nil {
		t.Fatalf("Write 1: %v", err)
	}

	// Second write: 60 more bytes, would exceed 100 → rotation
	if _, err := w.Write(data); err != nil {
		t.Fatalf("Write 2: %v", err)
	}

	// Current file should have 60 bytes (post-rotation write)
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("Stat current: %v", err)
	}
	if info.Size() != 60 {
		t.Errorf("current file size = %d, want 60", info.Size())
	}

	// Rotated file .1 should have 60 bytes (pre-rotation content)
	info, err = os.Stat(filepath.Join(dir, name+".1"))
	if err != nil {
		t.Fatalf("Stat .1: %v", err)
	}
	if info.Size() != 60 {
		t.Errorf(".1 file size = %d, want 60", info.Size())
	}
}

// TestRotation_PrunesOldestFile verifies that once maxFiles rotated copies
// exist, the oldest is removed. Without pruning, disk usage grows unbounded.
func TestRotation_PrunesOldestFile(t *testing.T) {
	dir := t.TempDir()
	name := "test.log"
	maxBytes := int64(10)
	maxFiles := 2

	w := NewRotatingWriter(dir, name, maxBytes, maxFiles)
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Trigger 3 rotations to exceed maxFiles=2
	chunk := make([]byte, 11)
	for i := range 4 {
		for j := range chunk {
			chunk[j] = byte('A' + i)
		}
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// .1 and .2 should exist
	for _, suffix := range []string{".1", ".2"} {
		if _, err := os.Stat(filepath.Join(dir, name+suffix)); err != nil {
			t.Errorf("expected %s to exist: %v", name+suffix, err)
		}
	}

	// .3 should NOT exist (pruned)
	if _, err := os.Stat(filepath.Join(dir, name+".3")); !os.IsNotExist(err) {
		t.Errorf("expected %s.3 to not exist, got err=%v", name, err)
	}
}

// TestRotation_FileOrderPreservesNewestFirst verifies that after multiple
// rotations, .1 contains the most recent rotated content and higher numbers
// contain older content. This ensures operators can find the latest logs at .1.
func TestRotation_FileOrderPreservesNewestFirst(t *testing.T) {
	dir := t.TempDir()
	name := "test.log"
	maxBytes := int64(5)
	maxFiles := 3

	w := NewRotatingWriter(dir, name, maxBytes, maxFiles)
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Write distinct content that triggers rotation each time
	for i := range 4 {
		content := fmt.Sprintf("gen%d\n", i)
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	// Current file has most recent write
	current, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("ReadFile current: %v", err)
	}
	if string(current) != "gen3\n" {
		t.Errorf("current = %q, want %q", current, "gen3\n")
	}

	// .1 has second-most-recent
	f1, err := os.ReadFile(filepath.Join(dir, name+".1"))
	if err != nil {
		t.Fatalf("ReadFile .1: %v", err)
	}
	if string(f1) != "gen2\n" {
		t.Errorf(".1 = %q, want %q", f1, "gen2\n")
	}

	// .2 has third-most-recent
	f2, err := os.ReadFile(filepath.Join(dir, name+".2"))
	if err != nil {
		t.Fatalf("ReadFile .2: %v", err)
	}
	if string(f2) != "gen1\n" {
		t.Errorf(".2 = %q, want %q", f2, "gen1\n")
	}
}

// TestConcurrentWrites verifies that concurrent goroutines can write
// without data races or panics. The rotating writer uses a mutex to
// serialize access, which is tested here under contention.
func TestConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	name := "test.log"
	maxBytes := int64(100)

	w := NewRotatingWriter(dir, name, maxBytes, 3)
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_, _ = w.Write([]byte("concurrent log line\n"))
			}
		}()
	}
	wg.Wait()

	// No assertion on content — the test passes if no race detector violation
	// and no panic occurs. The -race flag catches data races.
}

// TestOpen_CreatesDirectory verifies that Open creates the log directory
// if it doesn't exist. The agent may start before the log directory is
// provisioned by systemd or cloud-init.
func TestOpen_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logdir")
	w := NewRotatingWriter(dir, "test.log", 1024, 3)
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

// TestOpen_AppendsToExistingFile verifies that Open resumes appending to
// an existing file and tracks its size correctly. This ensures that a
// restart doesn't reset the size counter and delay rotation.
func TestOpen_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	name := "test.log"
	path := filepath.Join(dir, name)

	// Pre-create file with 50 bytes
	if err := os.WriteFile(path, make([]byte, 50), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w := NewRotatingWriter(dir, name, 100, 3)
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Write 60 bytes — should trigger rotation since 50+60 > 100
	if _, err := w.Write(make([]byte, 60)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// .1 should exist with the original 50 bytes
	info, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("Stat .1: %v", err)
	}
	if info.Size() != 50 {
		t.Errorf(".1 size = %d, want 50", info.Size())
	}
}

// TestWrite_BeforeOpen verifies that Write returns an error if called
// before Open. This guards against use-before-init programming errors.
func TestWrite_BeforeOpen(t *testing.T) {
	w := NewRotatingWriter(t.TempDir(), "test.log", 1024, 3)
	_, err := w.Write([]byte("data"))
	if err == nil {
		t.Error("expected error when writing before Open")
	}
}

// TestClose_Idempotent verifies that calling Close multiple times does not
// return an error. The agent may call Close in defer chains where double-close
// is possible.
func TestClose_Idempotent(t *testing.T) {
	dir := t.TempDir()
	w := NewRotatingWriter(dir, "test.log", 1024, 3)
	if err := w.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
}
