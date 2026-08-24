//go:build unix

package main

import (
	"errors"
	"syscall"
	"testing"
)

// The branches a healthy machine never takes. Both are reached by replacing the
// syscall rather than by finding a broken filesystem, which is the only way to
// test them at all — and leaving them untested would mean the code that runs
// when a machine is already failing has never run.

func TestDiskSpaceReportsAFailedStatfs(t *testing.T) {
	want := errors.New("statfs exploded")
	restoreStatfs(t, func(string, *syscall.Statfs_t) error { return want })

	_, _, err := diskSpace("/anywhere")
	if !errors.Is(err, want) {
		t.Fatalf("diskSpace error = %v, want it to wrap %v", err, want)
	}
}

func TestDiskSpaceRejectsANonPositiveBlockSize(t *testing.T) {
	restoreStatfs(t, func(_ string, st *syscall.Statfs_t) error {
		*st = syscall.Statfs_t{Bsize: 0, Blocks: 1 << 20, Bavail: 1 << 10}
		return nil
	})

	// A zero block size would multiply every figure to zero and report a
	// filesystem with no capacity as if that were a measurement.
	_, _, err := diskSpace("/anywhere")
	if err == nil {
		t.Fatal("diskSpace accepted a filesystem reporting a block size of 0")
	}
}

func TestDiskSpaceMultipliesBlocksByBlockSize(t *testing.T) {
	restoreStatfs(t, func(_ string, st *syscall.Statfs_t) error {
		*st = syscall.Statfs_t{Bsize: 4096, Blocks: 1000, Bavail: 250}
		return nil
	})

	avail, total, err := diskSpace("/anywhere")
	if err != nil {
		t.Fatalf("diskSpace: %v", err)
	}
	if total != 4096*1000 {
		t.Fatalf("capacity = %d, want %d", total, 4096*1000)
	}
	if avail != 4096*250 {
		t.Fatalf("available = %d, want %d", avail, 4096*250)
	}
}

func TestProcessCPUTimeReportsAFailedGetrusage(t *testing.T) {
	want := errors.New("getrusage exploded")
	original := getrusage
	getrusage = func(int, *syscall.Rusage) error { return want }
	t.Cleanup(func() { getrusage = original })

	_, _, err := processCPUTime()
	if !errors.Is(err, want) {
		t.Fatalf("processCPUTime error = %v, want it to wrap %v", err, want)
	}
}

func restoreStatfs(t *testing.T, stub func(string, *syscall.Statfs_t) error) {
	t.Helper()
	original := statfs
	statfs = stub
	t.Cleanup(func() { statfs = original })
}
