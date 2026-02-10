//go:build windows

package scriptrunner

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setSysProcAttr configures the command to run in its own process group
// using CREATE_NEW_PROCESS_GROUP so the entire tree can be killed on timeout.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup terminates the process tree rooted at pid using taskkill.
// The /T flag kills child processes and /F forces termination.
func killProcessGroup(pid int) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(pid)).Run()
}
