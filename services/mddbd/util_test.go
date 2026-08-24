package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
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

	assertNoTempLeftovers(t, dst, "a failed copy")
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
	assertNoTempLeftovers(t, dst, "a successful copy")
}

// assertNoTempLeftovers globs rather than stat-ing a fixed name: copyFile names
// its temp file through os.CreateTemp, so the suffix is unique per call. An
// assertion on a fixed dst+".tmp" would pass by looking for a file that is
// never created — which is exactly what these two tests did for one commit.
func assertNoTempLeftovers(t *testing.T, dst, after string) {
	t.Helper()
	leftovers, err := filepath.Glob(dst + ".tmp-*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temp files left after %s: %v", after, leftovers)
	}
}

// Two copies to the same destination must not write through each other's temp
// file. They race on the final rename either way — one of them wins and that is
// the caller's problem — but with a shared fixed temp name they would interleave
// their bytes into one file first, and the winner could publish a blend of both.
func TestCopyFile_ConcurrentCopiesDoNotShareATempFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "destination")

	const copies = 8
	sources := make([]string, copies)
	for i := range sources {
		sources[i] = filepath.Join(dir, "source-"+string(rune('a'+i)))
		// Distinct lengths and bytes, so an interleaved result is recognisable.
		body := bytes.Repeat([]byte{byte('a' + i)}, 4096*(i+1))
		if err := os.WriteFile(sources[i], body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make([]error, copies)
	for i := range sources {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = copyFile(sources[i], dst)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("copy %d: %v", i, err)
		}
	}

	got, err := os.ReadFile(dst) // #nosec G304 -- test-constructed path
	if err != nil {
		t.Fatalf("destination unreadable: %v", err)
	}
	// Whichever copy won, the file must be exactly one of the sources — same
	// length, same single repeated byte — and not a mixture.
	if len(got) == 0 || len(got)%4096 != 0 {
		t.Fatalf("destination is %d bytes, which matches no single source", len(got))
	}
	if bytes.Count(got, got[:1]) != len(got) {
		t.Fatalf("destination mixes bytes from more than one source")
	}

	assertNoTempLeftovers(t, dst, "concurrent copies")
}
