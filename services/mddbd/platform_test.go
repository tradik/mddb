package main

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// WIN-001. diskSpace and processCPUTime are the two syscalls that had to grow a
// Windows implementation. These tests run on every platform and assert the
// contract both implementations promise, rather than either one's mechanics.

func TestDiskSpaceReportsSaneFiguresForARealDirectory(t *testing.T) {
	avail, total, err := diskSpace(t.TempDir())
	if err != nil {
		t.Fatalf("diskSpace on a temp dir: %v", err)
	}
	if total == 0 {
		t.Fatal("total capacity is 0; no filesystem holding a temp dir has zero capacity")
	}
	if avail > total {
		t.Fatalf("available %d exceeds capacity %d", avail, total)
	}
}

func TestDiskSpaceFailsOnAMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-directory")
	if _, _, err := diskSpace(missing); err == nil {
		t.Fatalf("diskSpace(%q) succeeded on a path that does not exist", missing)
	}
}

func TestDiskUsageDerivesUsedFromCapacityAndAvailable(t *testing.T) {
	dir := t.TempDir()

	avail, total, err := diskSpace(dir)
	if err != nil {
		t.Fatalf("diskSpace: %v", err)
	}

	used, reported, ok := diskUsage(dir)
	if !ok {
		t.Fatal("diskUsage failed on a directory diskSpace answered for")
	}
	if reported != total {
		t.Fatalf("diskUsage reports capacity %d, diskSpace reports %d", reported, total)
	}

	// used+avail should equal capacity, but the two figures come from separate
	// calls and anything else on the machine may write between them. The
	// tolerance is for that churn, not for arithmetic: it is far tighter than
	// any plausible wrong answer (0, or capacity itself).
	const churn = 64 << 20
	sum := used + avail
	if sum > total+churn || sum < total-churn {
		t.Fatalf("used %d + available %d = %d, capacity %d — off by more than %d",
			used, avail, sum, total, churn)
	}

	// What this test does NOT cover: whether diskSpace asks for the blocks
	// available to an unprivileged process (Bavail) rather than all free blocks
	// (Bfree). Both figures flow through the same function, so any assertion
	// here holds for either. The distinction is only observable as a non-root
	// process on a filesystem with a root reserve, and has no Windows
	// equivalent at all. It is guarded by the comment in platform_disk_unix.go
	// and by review, not by this test — saying so beats a test named as if it
	// checked something it cannot.
}

func TestDiskUsageFailsOnAMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-directory")
	if _, _, ok := diskUsage(missing); ok {
		t.Fatalf("diskUsage(%q) succeeded on a path that does not exist", missing)
	}
}

func TestFreeBytesReportsAvailableSpaceNotCapacity(t *testing.T) {
	dir := t.TempDir()

	_, total, err := diskSpace(dir)
	if err != nil {
		t.Fatalf("diskSpace: %v", err)
	}

	// Occupy a megabyte so the filesystem provably holds something. Without
	// this, a freeBytes that wrongly returned capacity would be indistinguishable
	// from a correct one on an empty filesystem, where available == capacity.
	blob := make([]byte, 1<<20)
	if err := os.WriteFile(filepath.Join(dir, "ballast"), blob, 0o600); err != nil {
		t.Fatalf("writing ballast: %v", err)
	}

	free, err := freeBytes(dir)
	if err != nil {
		t.Fatalf("freeBytes: %v", err)
	}
	if free >= total {
		t.Fatalf("freeBytes %d is not below capacity %d, so it is reporting capacity "+
			"rather than available space", free, total)
	}
}

func TestFreeBytesFailsOnAMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-directory")
	if _, err := freeBytes(missing); err == nil {
		t.Fatalf("freeBytes(%q) succeeded on a path that does not exist", missing)
	}
}

func TestProcessCPUTimeAdvancesWhenTheProcessWorks(t *testing.T) {
	user0, system0, err := processCPUTime()
	if err != nil {
		t.Fatalf("processCPUTime: %v", err)
	}
	if user0 < 0 || system0 < 0 {
		t.Fatalf("negative CPU time: user=%d system=%d", user0, system0)
	}

	// Burn CPU until the counter moves rather than doing a fixed amount of
	// work and asserting. How much work registers is a platform property:
	// GetProcessTimes advances in ~15.6 ms steps on Windows, so a loop sized to
	// register on Linux can finish inside one unchanged sample there.
	deadline := time.Now().Add(10 * time.Second)
	for {
		sink := 0.0
		for i := 0; i < 2_000_000; i++ {
			sink += math.Sqrt(float64(i))
		}
		runtime.KeepAlive(sink)

		user1, _, err := processCPUTime()
		if err != nil {
			t.Fatalf("processCPUTime: %v", err)
		}
		if user1 > user0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("user CPU time stayed at %d ns through 10s of arithmetic; "+
				"the platform reading is not accumulating", user0)
		}
	}
}
