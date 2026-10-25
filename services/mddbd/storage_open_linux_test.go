//go:build linux

package main

import "testing"

func TestPlatformCounters(t *testing.T) {
	if got := parseMemAvailable("MemTotal: 10 kB\nMemAvailable:  2048 kB\n"); got != 2048*1024 {
		t.Errorf("MemAvailable = %d", got)
	}
	if parseMemAvailable("nothing") != 0 {
		t.Error("no MemAvailable line")
	}
	if n, ok := sumRbytes("8:0 rbytes=100 wbytes=5\n8:16 rbytes=23 wbytes=0"); !ok || n != 123 {
		t.Errorf("rbytes = %d %v", n, ok)
	}
	if _, ok := sumRbytes(""); ok {
		t.Error("empty io.stat")
	}
	if availableMemory() == 0 {
		t.Error("no memory figure on this platform")
	}
	if _, ok := storageReadBytes(); !ok {
		t.Error("no storage read counter on this platform")
	}
}
