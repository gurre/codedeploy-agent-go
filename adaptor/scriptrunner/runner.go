// Package scriptrunner executes deployment lifecycle hook scripts with process
// group management, timeout enforcement, and stdout/stderr capture.
package scriptrunner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Result holds the outcome of a script execution.
type Result struct {
	// Stdout is the captured standard output (up to maxLogBytes).
	Stdout string
	// Stderr is the captured standard error (up to maxLogBytes).
	Stderr string
	// ExitCode is the process exit code (-1 if killed or not available).
	ExitCode int
	// TimedOut is true if the script was killed due to timeout.
	TimedOut bool
}

const maxLogBytes = 2048

// Runner executes scripts as child processes with process group isolation.
type Runner struct {
	logger *slog.Logger
}

// NewRunner creates a script runner.
//
//	r := scriptrunner.NewRunner(slog.Default())
//	result, err := r.Run(ctx, "/opt/deploy/scripts/install.sh", env, 300)
func NewRunner(logger *slog.Logger) *Runner {
	return &Runner{logger: logger}
}

// Run executes a script with the given environment variables and timeout.
// The script runs in its own process group so the entire group can be killed
// on timeout. Environment vars are merged with the current environment.
//
//	env := map[string]string{"LIFECYCLE_EVENT": "AfterInstall"}
//	result, err := runner.Run(ctx, "/path/to/script.sh", env, 3600)
func (r *Runner) Run(ctx context.Context, scriptPath string, env map[string]string, timeoutSeconds int) (Result, error) {
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return Result{ExitCode: -1}, fmt.Errorf("script does not exist: %s", scriptPath)
	}

	// Ensure script is executable
	info, err := os.Stat(scriptPath)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	if info.Mode()&0o111 == 0 {
		if err := os.Chmod(scriptPath, info.Mode()|0o111); err != nil {
			return Result{ExitCode: -1}, fmt.Errorf("cannot make script executable: %s: %w", scriptPath, err)
		}
		r.logger.Warn("made script executable", "path", scriptPath)
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Dir = "/"
	cmd.Env = buildEnv(env)
	setSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdoutBuf, remaining: maxLogBytes}
	cmd.Stderr = &limitedWriter{w: &stderrBuf, remaining: maxLogBytes}

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("script start failed: %s: %w", scriptPath, err)
	}

	err = cmd.Wait()

	result := Result{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}

	if ctx.Err() == context.DeadlineExceeded {
		// Kill the entire process group
		_ = killProcessGroup(cmd.Process.Pid)
		result.TimedOut = true
		result.ExitCode = -1
		return result, nil
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}

	result.ExitCode = 0
	return result, nil
}

// RunAs executes a script as a different user via sudo.
func (r *Runner) RunAs(ctx context.Context, scriptPath, user string, env map[string]string, timeoutSeconds int) (Result, error) {
	if user == "" {
		return r.Run(ctx, scriptPath, env, timeoutSeconds)
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build env string for sudo
	envArgs := make([]string, 0, len(env))
	for k, v := range env {
		envArgs = append(envArgs, k+"="+v)
	}

	args := []string{"-u", user}
	args = append(args, envArgs...)
	args = append(args, scriptPath)

	cmd := exec.CommandContext(ctx, "sudo", args...)
	setSysProcAttr(cmd)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdoutBuf, remaining: maxLogBytes}
	cmd.Stderr = &limitedWriter{w: &stderrBuf, remaining: maxLogBytes}

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, fmt.Errorf("script start failed: %s: %w", scriptPath, err)
	}

	err := cmd.Wait()

	result := Result{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}

	if ctx.Err() == context.DeadlineExceeded {
		_ = killProcessGroup(cmd.Process.Pid)
		result.TimedOut = true
		result.ExitCode = -1
		return result, nil
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}

	result.ExitCode = 0
	return result, nil
}

// FormatLog formats stdout and stderr into the log format expected by
// the diagnostic payload. Lines are prefixed with [stdout] or [stderr].
func FormatLog(stdout, stderr string) string {
	var b strings.Builder
	for _, line := range strings.Split(stdout, "\n") {
		if line != "" {
			b.WriteString("[stdout]")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	for _, line := range strings.Split(stderr, "\n") {
		if line != "" {
			b.WriteString("[stderr]")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func buildEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// limitedWriter wraps a writer and stops writing after a byte limit.
type limitedWriter struct {
	w         io.Writer
	remaining int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.remaining <= 0 {
		return len(p), nil // Discard but report full length to avoid short write errors
	}
	toWrite := p
	if len(toWrite) > lw.remaining {
		toWrite = toWrite[:lw.remaining]
	}
	n, err := lw.w.Write(toWrite)
	lw.remaining -= n
	if err != nil {
		return n, err
	}
	// Report full original length to prevent short write errors from io.Copy
	return len(p), nil
}
