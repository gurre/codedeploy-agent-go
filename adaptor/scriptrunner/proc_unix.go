//go:build !windows

package scriptrunner

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr configures the command to run in its own process group
// so the entire group can be killed on timeout.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGTERM to the process group identified by pid.
// The negative pid targets the entire group rather than a single process.
func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}
