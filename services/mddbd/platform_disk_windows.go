//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// diskSpace reports the capacity of the volume holding path and the number of
// bytes available to the calling process.
//
// GetDiskFreeSpaceEx returns both figures directly, so unlike the Unix path
// there is no block size to multiply through and no reserved-blocks
// distinction to preserve. Windows applies per-user quotas instead, and
// freeBytesAvailableToCaller already accounts for them — it is the same
// question statfs answers with Bavail, asked of a different kernel.
//
// The path must name an existing directory or a file on the volume; Windows
// rejects anything else, which surfaces here as an error rather than a
// plausible-looking zero.
// getDiskFreeSpaceEx is a variable so tests can reach the failure branch; see
// the note on statfs in platform_disk_unix.go.
var getDiskFreeSpaceEx = windows.GetDiskFreeSpaceEx

func diskSpace(path string) (availBytes, totalBytes uint64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, fmt.Errorf("disk space %s: %w", path, err)
	}

	var availToCaller, total, totalFree uint64
	if err := getDiskFreeSpaceEx(p, &availToCaller, &total, &totalFree); err != nil {
		return 0, 0, fmt.Errorf("disk space %s: %w", path, err)
	}

	return availToCaller, total, nil
}
