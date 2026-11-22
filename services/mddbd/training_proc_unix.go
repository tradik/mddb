//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the command in a process group of its own, so a
// cancel or timeout reaches what it started too: SIGTERM to the group first,
// and once the grace period (WaitDelay) is over the leader is killed.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
}

// killProcessGroup kills whatever of the group outlived its leader: a job
// leaves no process behind. A group already gone answers ESRCH, ignored.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
