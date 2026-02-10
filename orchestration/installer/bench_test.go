package installer

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/gurre/codedeploy-agent-go/logic/appspec"
)

// BenchmarkInstall measures the full install cycle with 50 source files,
// a mock FileOperator, and real temp directories. This stresses instruction
// generation, file walking, and cleanup file writing.
func BenchmarkInstall(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	archiveDir := b.TempDir()
	instructionsDir := b.TempDir()

	// Create 50 source files across 5 subdirectories
	for d := range 5 {
		subdir := filepath.Join(archiveDir, "src", "dir"+string(rune('a'+d)))
		_ = os.MkdirAll(subdir, 0o755)
		for f := range 10 {
			path := filepath.Join(subdir, "file"+string(rune('0'+f))+".txt")
			_ = os.WriteFile(path, []byte("content"), 0o644)
		}
	}

	// Create destination directory structure so os.Stat checks pass
	destBase := b.TempDir()
	for d := range 5 {
		_ = os.MkdirAll(filepath.Join(destBase, "dir"+string(rune('a'+d))), 0o755)
	}

	spec := appspec.Spec{
		Version: 0.0,
		OS:      "linux",
		Files: []appspec.FileMapping{
			{Source: "src", Destination: destBase},
		},
	}

	fop := &benchFileOperator{}
	inst := NewInstaller(fop, logger)

	b.ResetTimer()
	for range b.N {
		_ = inst.Install("dg-bench", archiveDir, instructionsDir, spec, "OVERWRITE")
	}
}

// benchFileOperator performs real mkdir and records copy calls without actual I/O.
type benchFileOperator struct{}

func (f *benchFileOperator) Copy(_, _ string) error              { return nil }
func (f *benchFileOperator) Mkdir(path string) error             { return os.MkdirAll(path, 0o755) }
func (f *benchFileOperator) MkdirAll(path string) error          { return os.MkdirAll(path, 0o755) }
func (f *benchFileOperator) Chmod(_ string, _ os.FileMode) error { return nil }
func (f *benchFileOperator) Chown(_, _, _ string) error          { return nil }
func (f *benchFileOperator) SetACL(_ string, _ []string) error   { return nil }
func (f *benchFileOperator) SetContext(_, _, _, _ string) error  { return nil }
func (f *benchFileOperator) RemoveContext(_ string) error        { return nil }
func (f *benchFileOperator) Remove(_ string) error               { return nil }
