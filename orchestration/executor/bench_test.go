package executor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gurre/codedeploy-agent-go/logic/deployspec"
	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
)

// BenchmarkExecuteHook measures hook dispatch with a mock HookRunner that
// avoids disk I/O, isolating the dispatch overhead (lookup, arg building,
// log accumulation).
func BenchmarkExecuteHook(b *testing.B) {
	rootDir := b.TempDir()
	hookMapping := lifecycle.DefaultHookMapping()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	hr := &benchHookRunner{}
	exec := NewExecutor(nil, nil, hr, nil, &benchFileOp{}, rootDir, hookMapping, 5, logger)

	spec := deployspec.Spec{
		DeploymentID:        "d-bench",
		DeploymentGroupID:   "dg-bench",
		DeploymentGroupName: "bench-group",
		ApplicationName:     "bench-app",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		Source:              deployspec.RevisionS3,
		Bucket:              "b",
		Key:                 "k",
		ETag:                "e",
	}

	ctx := context.Background()
	// Pre-create deployment dir
	_ = os.MkdirAll(filepath.Join(rootDir, "dg-bench", "d-bench"), 0o755)

	b.ResetTimer()
	for range b.N {
		_, _ = exec.Execute(ctx, "BeforeInstall", spec)
	}
}

// BenchmarkCleanupOldArchives measures the cleanup path with N=20 directories
// on disk, including the os.Stat caching and sort.
func BenchmarkCleanupOldArchives(b *testing.B) {
	rootDir := b.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	exec := NewExecutor(nil, nil, nil, nil, &benchFileOp{}, rootDir, nil, 5, logger)

	groupDir := filepath.Join(rootDir, "dg-bench")
	_ = os.MkdirAll(groupDir, 0o755)

	// Create 20 deployment directories with staggered modification times
	for i := range 20 {
		dir := filepath.Join(groupDir, "d-"+string(rune('A'+i)))
		_ = os.MkdirAll(dir, 0o755)
		modTime := time.Now().Add(-time.Duration(20-i) * time.Hour)
		_ = os.Chtimes(dir, modTime, modTime)
	}

	spec := deployspec.Spec{
		DeploymentID:      "d-current",
		DeploymentGroupID: "dg-bench",
	}

	b.ResetTimer()
	for range b.N {
		exec.cleanupOldArchives(spec)
	}
}

// benchHookRunner returns a no-op result for benchmarking dispatch overhead.
type benchHookRunner struct{}

func (b *benchHookRunner) Run(_ context.Context, _ HookRunArgs) (HookResult, error) {
	return HookResult{Log: "ok"}, nil
}

func (b *benchHookRunner) IsNoop(_ HookRunArgs) (bool, error) {
	return false, nil
}

// benchFileOp performs real directory creation for benchmark setup.
type benchFileOp struct{}

func (f *benchFileOp) MkdirAll(path string) error  { return os.MkdirAll(path, 0o755) }
func (f *benchFileOp) RemoveAll(path string) error { return os.RemoveAll(path) }
