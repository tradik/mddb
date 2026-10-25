//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// storageReadBytes is how many bytes have been read from storage, for the
// progress of the freelist walk (#334). bbolt reads through mmap, and page
// faults do not show in /proc/self/io, so this is this cgroup's io.stat
// rbytes when the io controller is on, else the system-wide pgpgin — an
// estimate, which is why the progress it feeds stops at 99%.
func storageReadBytes() (int64, bool) {
	if n, ok := cgroupReadBytes(); ok {
		return n, true
	}
	data, err := os.ReadFile("/proc/vmstat")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "pgpgin "); ok {
			kb, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return kb * 1024, err == nil
		}
	}
	return 0, false
}

func cgroupReadBytes() (int64, bool) {
	self, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0, false
	}
	_, path, found := strings.Cut(strings.TrimSpace(string(self)), "::")
	if !found || strings.Contains(path, "..") {
		return 0, false
	}
	data, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", path, "io.stat")) // #nosec G304 G703 -- the kernel's own cgroup path, ".." refused
	if err != nil {
		return 0, false
	}
	return sumRbytes(string(data))
}

func sumRbytes(ioStat string) (int64, bool) {
	var total int64
	found := false
	for _, f := range strings.Fields(ioStat) {
		if v, ok := strings.CutPrefix(f, "rbytes="); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err == nil {
				total, found = total+n, true
			}
		}
	}
	return total, found
}
