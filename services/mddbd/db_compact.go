package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

// Offline compaction (#335).
//
// A bbolt file never shrinks: pages freed by a delete are reused, not given
// back. Compacting copies every key into a new file, in key order and with
// full pages, which is then as small as the data. Key order also helps cold
// reads: on a 2.5 GB file on WSL2 the walk after a crash fell from 57 s to
// 44 s. A larger page made the file smaller (2.0 GB at 16 KiB) but that walk
// slower (58 s) — the kernel reads around each fault anyway — so the page
// size stays the system's unless asked.
//
// The source is opened read-only, which skips bbolt's freelist walk, and the
// copy is written with its freelist stored, so its first start is instant.

const compactTxMaxSize = 64 << 20 // bytes per transaction while copying

// compactReport is what a compaction did.
type compactReport struct {
	SrcBytes, DstBytes int64
	PageSize           int
}

// compactDatabase copies src into a new file dst, with pageSize bytes per
// page (0 keeps the system default).
func compactDatabase(src, dst string, pageSize int) (*compactReport, error) {
	if _, err := os.Stat(dst); err == nil {
		return nil, fmt.Errorf("%s exists; compact writes a new file", dst)
	}
	if pageSize != 0 && (pageSize < 4096 || pageSize > 65536 || pageSize&(pageSize-1) != 0) {
		return nil, errors.New("page size must be a power of two from 4096 to 65536")
	}
	from, err := openBolt(src, &bolt.Options{ReadOnly: true, Timeout: getOptimizedBoltOptions().Timeout})
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w (stop the server first)", src, err)
	}
	defer func() { _ = from.Close() }()
	to, err := bolt.Open(filepath.Clean(dst), 0o600, &bolt.Options{PageSize: pageSize, FreelistType: bolt.FreelistMapType})
	if err != nil {
		return nil, err
	}
	if err := bolt.Compact(to, from, compactTxMaxSize); err != nil {
		_ = to.Close()
		return nil, err
	}
	report := &compactReport{PageSize: to.Info().PageSize}
	if err := to.Close(); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(src); err == nil {
		report.SrcBytes = fi.Size()
	}
	if fi, err := os.Stat(dst); err == nil {
		report.DstBytes = fi.Size()
	}
	return report, nil
}

// runCompact is `mddbd -compact-db SRC -compact-to DST [-compact-page-size N]`.
func runCompact(src, dst string, pageSize int) int {
	if dst == "" {
		fmt.Fprintln(os.Stderr, "-compact-db needs -compact-to: the path of the new database to write")
		return 2
	}
	report, err := compactDatabase(src, dst, pageSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("compacted %s (%d bytes) into %s (%d bytes, %d-byte pages)\n",
		src, report.SrcBytes, dst, report.DstBytes, report.PageSize)
	if err := verifyDatabase(dst); err != nil {
		fmt.Fprintln(os.Stderr, "the compacted database does not pass the check: "+err.Error())
		return 1
	}
	fmt.Println("the compacted database passes bbolt's integrity check; replace the old file with it while the server is stopped")
	return 0
}
