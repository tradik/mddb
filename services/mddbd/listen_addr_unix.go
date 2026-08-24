//go:build unix

package main

import "os"

// restrictSocketToOwner limits a Unix domain socket to the user that created
// it, so the socket behaves as the single-user IPC channel openListener
// promises.
func restrictSocketToOwner(path string) error {
	return os.Chmod(path, 0o600)
}
