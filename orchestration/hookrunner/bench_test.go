package hookrunner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gurre/codedeploy-agent-go/logic/lifecycle"
)

// BenchmarkRun measures the full Run() path including appspec parse from a
// temp file and mock ScriptRunner. This isolates the hookrunner overhead
// (root selection, appspec lookup, env building, script dispatch) from actual
// script execution time.
func BenchmarkRun(b *testing.B) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	deployDir := b.TempDir()
	archiveDir := filepath.Join(deployDir, "deployment-archive")
	_ = os.MkdirAll(filepath.Join(archiveDir, "scripts"), 0o755)
	osValue := "linux"
	if runtime.GOOS == "windows" {
		osValue = "windows"
	}
	_ = os.WriteFile(filepath.Join(archiveDir, "appspec.yml"), []byte(fmt.Sprintf(`
version: 0.0
os: %s
hooks:
  BeforeInstall:
    - location: scripts/install.sh
      timeout: 60
    - location: scripts/setup.sh
      timeout: 60
`, osValue)), 0o644)
	_ = os.WriteFile(filepath.Join(archiveDir, "scripts/install.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755)
	_ = os.WriteFile(filepath.Join(archiveDir, "scripts/setup.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755)

	sr := &benchScriptRunner{}
	runner := NewRunner(sr, logger)

	args := RunArgs{
		LifecycleEvent:      lifecycle.BeforeInstall,
		DeploymentID:        "d-bench",
		ApplicationName:     "bench-app",
		DeploymentGroupName: "bench-group",
		DeploymentGroupID:   "dg-bench",
		DeploymentCreator:   "user",
		DeploymentType:      "IN_PLACE",
		AppSpecPath:         "appspec.yml",
		DeploymentRootDir:   deployDir,
		RevisionEnvs: map[string]string{
			"BUNDLE_BUCKET": "my-bucket",
			"BUNDLE_KEY":    "my-key",
		},
	}

	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		_, _ = runner.Run(ctx, args)
	}
}

// benchScriptRunner returns a fixed result without executing any process.
type benchScriptRunner struct{}

func (s *benchScriptRunner) Run(_ context.Context, _ string, _ map[string]string, _ int) (ScriptResult, error) {
	return ScriptResult{Stdout: "ok\n", ExitCode: 0}, nil
}
