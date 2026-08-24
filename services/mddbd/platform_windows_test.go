//go:build windows

package main

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// The Windows counterparts of the failure branches covered in
// platform_unix_test.go. GetDiskFreeSpaceEx and GetProcessTimes do not fail for
// a valid directory and the current process, so the only way to run the code
// that handles them failing is to make them fail.

func TestDiskSpaceReportsAFailedGetDiskFreeSpaceEx(t *testing.T) {
	want := errors.New("GetDiskFreeSpaceEx exploded")
	original := getDiskFreeSpaceEx
	getDiskFreeSpaceEx = func(*uint16, *uint64, *uint64, *uint64) error { return want }
	t.Cleanup(func() { getDiskFreeSpaceEx = original })

	if _, _, err := diskSpace(`C:\`); !errors.Is(err, want) {
		t.Fatalf("diskSpace error = %v, want it to wrap %v", err, want)
	}
}

func TestDiskSpaceReturnsTheFiguresWindowsGives(t *testing.T) {
	const (
		wantAvail = uint64(250 << 20)
		wantTotal = uint64(1000 << 20)
	)
	original := getDiskFreeSpaceEx
	getDiskFreeSpaceEx = func(_ *uint16, avail, total, free *uint64) error {
		// Deliberately distinct from totalFree: the caller wants what it may
		// write, and on a volume with a quota those two disagree.
		*avail, *total, *free = wantAvail, wantTotal, 900<<20
		return nil
	}
	t.Cleanup(func() { getDiskFreeSpaceEx = original })

	avail, total, err := diskSpace(`C:\`)
	if err != nil {
		t.Fatalf("diskSpace: %v", err)
	}
	if avail != wantAvail {
		t.Fatalf("available = %d, want %d (the caller's quota, not the volume's free space)",
			avail, wantAvail)
	}
	if total != wantTotal {
		t.Fatalf("capacity = %d, want %d", total, wantTotal)
	}
}

func TestProcessCPUTimeReportsAFailedGetProcessTimes(t *testing.T) {
	want := errors.New("GetProcessTimes exploded")
	original := getProcessTimes
	getProcessTimes = func(windows.Handle, *windows.Filetime, *windows.Filetime, *windows.Filetime, *windows.Filetime) error {
		return want
	}
	t.Cleanup(func() { getProcessTimes = original })

	if _, _, err := processCPUTime(); !errors.Is(err, want) {
		t.Fatalf("processCPUTime error = %v, want it to wrap %v", err, want)
	}
}

func TestProcessCPUTimeReadsUserAndKernelInThatOrder(t *testing.T) {
	original := getProcessTimes
	getProcessTimes = func(_ windows.Handle, _, _, kernel, user *windows.Filetime) error {
		*kernel = windows.Filetime{LowDateTime: 20_000_000} // 2s
		*user = windows.Filetime{LowDateTime: 10_000_000}   // 1s
		return nil
	}
	t.Cleanup(func() { getProcessTimes = original })

	userNs, systemNs, err := processCPUTime()
	if err != nil {
		t.Fatalf("processCPUTime: %v", err)
	}
	// GetProcessTimes takes kernel before user; returning them in that order
	// would swap the two figures and the mistake would be invisible in any
	// aggregate that adds them together.
	if userNs != 1_000_000_000 {
		t.Fatalf("user = %d ns, want 1e9 — kernel and user look swapped", userNs)
	}
	if systemNs != 2_000_000_000 {
		t.Fatalf("system = %d ns, want 2e9 — kernel and user look swapped", systemNs)
	}
}
