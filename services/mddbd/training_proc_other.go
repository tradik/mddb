//go:build !unix

package main

import "os/exec"

// setProcessGroup keeps exec's own cancel on Windows and other systems
// without Unix process groups: the process is killed, its children are not
// reached. The documentation says so.
func setProcessGroup(*exec.Cmd) {}

// killProcessGroup has no group to kill here.
func killProcessGroup(*exec.Cmd) {}
