// Package tracker manages file-based deployment command tracking for crash recovery.
// When the agent restarts, it uses tracking files to fail in-progress lifecycle
// events and report them to the CodeDeploy service.
package tracker

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const staleTTL = 24 * time.Hour

// FileTracker manages ongoing deployment tracking files on disk.
type FileTracker struct {
	rootDir     string
	trackingDir string
	logger      *slog.Logger
}

// NewFileTracker creates a tracker rooted at the given directory.
//
//	t := tracker.NewFileTracker("/opt/codedeploy-agent/deployment-root", "ongoing-deployment", slog.Default())
func NewFileTracker(rootDir, trackingSubdir string, logger *slog.Logger) *FileTracker {
	return &FileTracker{
		rootDir:     rootDir,
		trackingDir: filepath.Join(rootDir, trackingSubdir),
		logger:      logger,
	}
}

// Create writes a tracking file for a deployment in progress.
// The file content is the host command identifier, used for crash recovery.
func (t *FileTracker) Create(deploymentID, hostCommandIdentifier string) error {
	if err := os.MkdirAll(t.trackingDir, 0o755); err != nil {
		return fmt.Errorf("tracker: mkdir: %w", err)
	}

	path := filepath.Join(t.trackingDir, deploymentID)
	if err := os.WriteFile(path, []byte(hostCommandIdentifier), 0o644); err != nil {
		return fmt.Errorf("tracker: write %s: %w", path, err)
	}
	return nil
}

// Delete removes the tracking file for a deployment.
func (t *FileTracker) Delete(deploymentID string) {
	path := filepath.Join(t.trackingDir, deploymentID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.logger.Warn("tracker: remove failed", "path", path, "error", err)
	}
}

// InProgressCommand returns the host command identifier for any non-stale
// in-progress deployment. Returns empty string if none found.
// Stale tracking files (older than 24h) are cleaned up automatically.
func (t *FileTracker) InProgressCommand() string {
	entries, err := os.ReadDir(t.trackingDir)
	if err != nil {
		return ""
	}

	var mostRecentID string
	var mostRecentTime time.Time

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(t.trackingDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Remove stale tracking files
		if time.Since(info.ModTime()) > staleTTL {
			t.logger.Info("removing stale tracking file", "path", path)
			_ = os.Remove(path)
			continue
		}

		if info.ModTime().After(mostRecentTime) {
			mostRecentTime = info.ModTime()
			mostRecentID = entry.Name()
		}
	}

	if mostRecentID == "" {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(t.trackingDir, mostRecentID))
	if err != nil {
		return ""
	}
	return string(data)
}

// CleanAll removes the entire tracking directory and its contents.
func (t *FileTracker) CleanAll() {
	_ = os.RemoveAll(t.trackingDir)
}
