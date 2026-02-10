//go:build !windows

package scriptrunner

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestRunSuccessfulScript verifies that a script returning exit code 0
// produces a success result with captured stdout.
func TestRunSuccessfulScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	result, err := r.Run(context.Background(), script, nil, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "hello\n" {
		t.Errorf("Stdout = %q", result.Stdout)
	}
}

// TestRunFailingScript verifies that a non-zero exit code is captured
// correctly without returning an error (the error is in the exit code).
func TestRunFailingScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	result, err := r.Run(context.Background(), script, nil, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", result.ExitCode)
	}
}

// TestRunScriptWithEnvVars verifies that environment variables are passed
// to the script. This is how LIFECYCLE_EVENT and DEPLOYMENT_ID are provided.
func TestRunScriptWithEnvVars(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "env.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho $MY_VAR\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	env := map[string]string{"MY_VAR": "test_value"}
	result, err := r.Run(context.Background(), script, env, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "test_value\n" {
		t.Errorf("Stdout = %q, want test_value", result.Stdout)
	}
}

// TestRunTimedOutScript verifies that scripts exceeding their timeout
// are killed and the TimedOut flag is set. This prevents runaway scripts
// from blocking deployments indefinitely.
func TestRunTimedOutScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	result, err := r.Run(context.Background(), script, nil, 1)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.TimedOut {
		t.Error("expected TimedOut = true")
	}
}

// TestRunMissingScript verifies that a non-existent script path returns
// an error rather than a zero exit code.
func TestRunMissingScript(t *testing.T) {
	r := NewRunner(slog.Default())
	_, err := r.Run(context.Background(), "/nonexistent/script.sh", nil, 10)
	if err == nil {
		t.Fatal("expected error for missing script")
	}
}

// TestRunMakesScriptExecutable verifies that a non-executable script is
// automatically made executable before running. This matches the Ruby
// agent's chmod +x behavior.
func TestRunMakesScriptExecutable(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "noexec.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	result, err := r.Run(context.Background(), script, nil, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
}

// TestFormatLog verifies the log format matches the Ruby agent's
// [stdout]/[stderr] prefix format used in diagnostic payloads.
func TestFormatLog(t *testing.T) {
	got := FormatLog("line1\nline2\n", "err1\n")
	want := "[stdout]line1\n[stdout]line2\n[stderr]err1\n"
	if got != want {
		t.Errorf("FormatLog = %q, want %q", got, want)
	}
}

// TestLimitedWriter verifies that output is truncated at the byte limit,
// preventing memory exhaustion from verbose scripts.
func TestLimitedWriter(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "verbose.sh")
	// Generate more than 2048 bytes of output
	if err := os.WriteFile(script, []byte("#!/bin/sh\ndd if=/dev/zero bs=1 count=4096 2>/dev/null | tr '\\0' 'A'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	result, err := r.Run(context.Background(), script, nil, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Stdout) > maxLogBytes {
		t.Errorf("Stdout length = %d, should be <= %d", len(result.Stdout), maxLogBytes)
	}
}

// TestLimitedWriterDirect verifies the limitedWriter independently from script
// execution. After exhausting the limit, writes are silently discarded but
// report the full input length to prevent io.Copy short write errors.
func TestLimitedWriterDirect(t *testing.T) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, remaining: 10}

	// First write within limit
	n, err := lw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}

	// Second write exceeds limit, only 5 remaining bytes written
	n, err = lw.Write([]byte("world12345extra"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Reports full input length to avoid short write errors
	if n != 15 {
		t.Errorf("n = %d, want 15", n)
	}
	if buf.Len() != 10 {
		t.Errorf("buf.Len() = %d, want 10", buf.Len())
	}

	// Third write: all discarded since remaining <= 0
	n, err = lw.Write([]byte("discarded"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 9 {
		t.Errorf("n = %d, want 9", n)
	}
	if buf.Len() != 10 {
		t.Errorf("buf should not grow, got %d", buf.Len())
	}
}

// TestRunScriptStderr verifies that stderr output is captured separately
// from stdout. Hook scripts commonly write errors to stderr that need to
// appear in deployment diagnostics.
func TestRunScriptStderr(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "stderr.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho stdout_line\necho stderr_line >&2\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	result, err := r.Run(context.Background(), script, nil, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Stdout != "stdout_line\n" {
		t.Errorf("Stdout = %q", result.Stdout)
	}
	if result.Stderr != "stderr_line\n" {
		t.Errorf("Stderr = %q", result.Stderr)
	}
}

// TestFormatLogEmptyInputs verifies that FormatLog handles empty strings
// without producing spurious prefix lines.
func TestFormatLogEmptyInputs(t *testing.T) {
	got := FormatLog("", "")
	if got != "" {
		t.Errorf("FormatLog empty = %q, want empty", got)
	}
}

// TestRunAsEmptyUserDelegatesToRun verifies that RunAs with an empty user
// string delegates to the regular Run function. This is the common case
// when hooks don't specify a runas user.
func TestRunAsEmptyUserDelegatesToRun(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "runas.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho delegated\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := NewRunner(slog.Default())
	result, err := r.RunAs(context.Background(), script, "", nil, 10)
	if err != nil {
		t.Fatalf("RunAs: %v", err)
	}
	if result.Stdout != "delegated\n" {
		t.Errorf("Stdout = %q, want delegated", result.Stdout)
	}
}
