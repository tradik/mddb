//go:build !linux

package main

// storageReadBytes is not available here; the open reports its phase only.
func storageReadBytes() (int64, bool) { return 0, false }
