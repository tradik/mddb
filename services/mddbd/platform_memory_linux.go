//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
)

// availableMemory is what the page cache can hold for this process: the
// kernel's MemAvailable, or the cgroup's memory limit when that is lower —
// in a container the page cache counts against the limit.
func availableMemory() uint64 {
	var avail uint64
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		avail = parseMemAvailable(string(data))
	}
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if limit, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil && (avail == 0 || limit < avail) {
			avail = limit
		}
	}
	return avail
}

func parseMemAvailable(meminfo string) uint64 {
	for _, line := range strings.Split(meminfo, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemAvailable:" {
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}
