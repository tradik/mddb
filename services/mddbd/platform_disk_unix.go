//go:build unix

package main

import (
	"fmt"
	"syscall"
)

// diskSpace reports the capacity of the filesystem holding path and the number
// of bytes available to an unprivileged process.
//
// "Available" rather than "free" is deliberate. Unix filesystems reserve a
// share of their blocks for root, and a server running as a normal user cannot
// write into them. Counting them as free is how a disk-full incident arrives
// with the monitor still reporting headroom.
// statfs is a variable so tests can reach the failure branches below. A healthy
// filesystem returns neither an error nor a non-positive block size, so without
// this the only untested code here would be the error handling — which is
// precisely the code that runs when the machine is already in trouble.
var statfs = syscall.Statfs

func diskSpace(path string) (availBytes, totalBytes uint64, err error) {
	var st syscall.Statfs_t
	if err := statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}

	// Every field is converted through uint64 rather than multiplied directly.
	// Statfs_t is platform-specific and its field widths disagree: Bsize is
	// int64 on Linux and uint32 on Darwin, Bavail is uint64 on Linux and int64
	// on the BSDs. Multiplying directly compiles on Linux and fails the FreeBSD
	// build, which is how that reached a release branch once — the only builder
	// that could catch it is the one nobody runs locally.
	if st.Bsize <= 0 {
		return 0, 0, fmt.Errorf("statfs %s: block size %d is not positive", path, st.Bsize)
	}

	// #nosec G115 -- Bsize is validated positive above, and block counts are sizes
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), nil
}
