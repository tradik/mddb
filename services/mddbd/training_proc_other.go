//go:build !unix

package main

import "os/exec"

// setProcessGroup keeps exec's own cancel on Windows and other systems
// without Unix process groups: the process is killed, its children are not
// reached. The documentation says so.
func setProcessGroup(*exec.Cmd) {
	// Nothing to set: there is no process group to start the command in, and
	// exec's default cancel already kills the process.
}

// killProcessGroup has no group to kill here.
func killProcessGroup(*exec.Cmd) {
	// Nothing to kill: without a process group, exec's cancel and Wait have
	// already ended the one process this platform reaches.
}
