package main

import (
	"os"
	"path/filepath"
	"testing"
)

// copyFile used to leave its temp file behind on every failure path. On a
// restore that orphan is a partial copy of the database — sitting on the
// filesystem whose running out of room is the most likely reason the copy
// failed in the first place.
func TestCopyFile_RemovesItsTempFileWhenTheCopyFails(t *testing.T) {
	dir := t.TempDir()

	src := filepath.Join(dir, "source")
	if err := os.WriteFile(src, []byte("some content"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A destination that exists as a directory: the write and the close both
	// succeed, and the final rename is what fails.
	dst := filepath.Join(dir, "destination")
	if err := os.Mkdir(dst, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err == nil {
		t.Fatal("copyFile onto an existing directory succeeded")
	}

	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left at %s.tmp after a failed copy (err=%v)", dst, err)
	}
}

// And the success path must still leave the destination in place and no temp
// file beside it — the cleanup runs there too, and must be a no-op.
func TestCopyFile_LeavesNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()

	src := filepath.Join(dir, "source")
	want := []byte("some content")
	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "destination")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	got, err := os.ReadFile(dst) // #nosec G304 -- test-constructed path
	if err != nil {
		t.Fatalf("destination unreadable: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("destination = %q, want %q", got, want)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left at %s.tmp after a successful copy (err=%v)", dst, err)
	}
}
