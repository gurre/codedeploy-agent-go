// Package logfile provides a size-based rotating file writer for slog.
// It rotates when the current file exceeds maxBytes, keeping up to maxFiles
// old copies named {name}.1, {name}.2, etc. (newest = .1).
// This matches the Ruby CodeDeploy agent's size-based log rotation strategy.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter is a goroutine-safe io.WriteCloser that rotates log files
// by size. Old files are shifted numerically (newest = .1, oldest = .N)
// and the oldest is removed when maxFiles is exceeded.
type RotatingWriter struct {
	dir      string
	name     string
	maxBytes int64
	maxFiles int
	mu       sync.Mutex
	file     *os.File
	size     int64
}

// NewRotatingWriter creates a RotatingWriter. Call Open before first Write.
//
//	w := logfile.NewRotatingWriter("/var/log/agent", "agent.log", 64<<20, 8)
//	if err := w.Open(); err != nil { ... }
//	defer w.Close()
func NewRotatingWriter(dir, name string, maxBytes int64, maxFiles int) *RotatingWriter {
	return &RotatingWriter{
		dir:      dir,
		name:     name,
		maxBytes: maxBytes,
		maxFiles: maxFiles,
	}
}

// Open creates the log directory if needed and opens the current log file
// for appending. It checks the file's current size so rotation picks up
// where a previous process left off.
func (w *RotatingWriter) Open() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("logfile: mkdir %s: %w", w.dir, err)
	}

	path := filepath.Join(w.dir, w.name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("logfile: open %s: %w", path, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logfile: stat %s: %w", path, err)
	}

	w.file = f
	w.size = info.Size()
	return nil
}

// Write implements io.Writer. It rotates the file when the next write would
// exceed maxBytes, then writes p to the new file.
// A single write larger than maxBytes is accepted without splitting — the
// fresh file will momentarily exceed maxBytes and the next write triggers
// another rotation. In practice slog lines are ~200 bytes vs 64MB limit.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, fmt.Errorf("logfile: writer not opened")
	}

	if w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the underlying file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// rotate shifts existing files: name.N-1 -> name.N, ..., name -> name.1,
// removes the oldest if it exceeds maxFiles, and opens a fresh file.
// Caller must hold w.mu.
func (w *RotatingWriter) rotate() error {
	_ = w.file.Close()
	w.file = nil

	base := filepath.Join(w.dir, w.name)

	// Remove the oldest file if it exists
	oldest := fmt.Sprintf("%s.%d", base, w.maxFiles)
	_ = os.Remove(oldest)

	// Shift files: name.N-1 -> name.N, ..., name.1 -> name.2
	for i := w.maxFiles - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", base, i)
		dst := fmt.Sprintf("%s.%d", base, i+1)
		_ = os.Rename(src, dst)
	}

	// Current file becomes name.1
	_ = os.Rename(base, base+".1")

	// Open fresh file
	f, err := os.OpenFile(base, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("logfile: open new %s: %w", base, err)
	}

	w.file = f
	w.size = 0
	return nil
}
