package main

import (
	"os"
	"runtime"
	"strconv"
)

// Room to grow before a remap (#350).
//
// Past 1 GB bbolt grows its memory map 1 GB at a time, and every remap waits
// for all open read transactions and holds off new ones meanwhile. A bulk
// ingest into a 100 GB file remapped once per gigabyte written, and each time
// the whole server paused. A large file is therefore mapped a quarter beyond
// its size from the start. The map is address space, not memory or disk: on
// Linux and macOS the file still grows in small steps. Windows extends the
// file to the size of the map, so there the default stays.

// mmapHeadroomFrom is the file size from which the headroom applies.
const mmapHeadroomFrom = 1 << 30

// mmapHeadroom is the initial map for the file at path: its size plus a
// quarter for a file over 1 GB on a 64-bit, non-Windows system, else 0.
func mmapHeadroom(path string) int {
	return headroomFor(path, runtime.GOOS, strconv.IntSize)
}

func headroomFor(path, goos string, intSize int) int {
	if goos == "windows" || intSize < 64 {
		return 0
	}
	fi, err := os.Stat(path) // #nosec G703 -- the database path from server config
	if err != nil || fi.Size() <= mmapHeadroomFrom {
		return 0
	}
	return int(fi.Size() + fi.Size()/4)
}
