//go:build darwin || freebsd

package main

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// availableMemory is physical memory: neither system offers a cheap
// "available" figure, and shouldPrewarm only uses half of it.
func availableMemory() uint64 {
	name := "hw.physmem"
	if runtime.GOOS == "darwin" {
		name = "hw.memsize"
	}
	v, err := unix.SysctlUint64(name)
	if err != nil {
		return 0
	}
	return v
}
