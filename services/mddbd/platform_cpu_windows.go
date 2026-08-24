//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// processCPUTime reports the CPU time this process has accumulated in user
// code and in the kernel on its behalf.
//
// Both are durations since the process started, not points in time. The
// sampler cares only about the difference between two readings.
// getProcessTimes is a variable so tests can reach the failure branch; see the
// note on statfs in platform_disk_unix.go.
var getProcessTimes = windows.GetProcessTimes

func processCPUTime() (userNs, systemNs int64, err error) {
	var creation, exit, kernel, user windows.Filetime
	if err := getProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return 0, 0, fmt.Errorf("getprocesstimes: %w", err)
	}
	return filetimeDurationNanos(user), filetimeDurationNanos(kernel), nil
}

// filetimeDurationNanos converts a FILETIME holding a duration into nanoseconds.
//
// windows.Filetime has a Nanoseconds method and it is the wrong one to call
// here: it subtracts the 1601-to-1970 epoch offset, because it exists to
// convert FILETIMEs that hold a point in time. The kernel and user values from
// GetProcessTimes hold elapsed CPU time instead.
//
// The result is worse than a shifted number. For one second of CPU the
// subtraction reaches about -1.16e19, which does not fit in an int64, so the
// multiply by 100 wraps: the method returns 6802270474709551616 — a large
// positive value that looks like a plausible reading rather than an obvious
// error. Measured on windows/amd64, not reasoned about; see
// TestFiletimeNanosecondsMethodIsWrongForDurations.
//
// FILETIME counts 100-nanosecond intervals either way, so the conversion is
// the multiply without the offset.
func filetimeDurationNanos(ft windows.Filetime) int64 {
	return (int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)) * 100
}
