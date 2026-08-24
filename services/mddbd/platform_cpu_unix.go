//go:build unix

package main

import (
	"fmt"
	"syscall"
)

// processCPUTime reports the CPU time this process has accumulated in user
// code and in the kernel on its behalf.
//
// Both are durations since the process started, not points in time. The
// sampler cares only about the difference between two readings.
// getrusage is a variable for the same reason statfs is in platform_disk_unix.go:
// it does not fail for RUSAGE_SELF on a working kernel, and the branch that
// handles it failing should still be exercised.
var getrusage = syscall.Getrusage

func processCPUTime() (userNs, systemNs int64, err error) {
	var ru syscall.Rusage
	if err := getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0, fmt.Errorf("getrusage: %w", err)
	}
	return ru.Utime.Nano(), ru.Stime.Nano(), nil
}
