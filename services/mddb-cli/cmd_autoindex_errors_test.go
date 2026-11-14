package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The ways a run stops before it sends anything.

// A repository whose file list git cannot give is an error, not an empty
// collection.
func TestAutoindex_GitListFails(t *testing.T) {
	bin := t.TempDir()
	// Inside a work tree by its answers, but ls-files fails.
	script := "#!/bin/sh\n[ \"$3\" = ls-files ] && { echo broken >&2; exit 1; }\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { // #nosec G306 -- a test program must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	fs := newFakeServer(t, autoindexRoutes())
	if _, err := runCLI(t, fs.URL, "autoindex", sampleTree(t)); err == nil {
		t.Fatal("a failed git ls-files is an error")
	}
	if len(callsTo(fs, "POST", "/v1/ingest")) != 0 {
		t.Fatal("nothing is sent without the file list")
	}
}

// A relative root needs the working directory; without one there is no run.
func TestAutoindex_NoWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone")
	if err := os.Mkdir(gone, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PWD", "")
	if err := runAutoindex(context.Background(), newClient(), autoindexOptions{root: "."}); err == nil {
		t.Fatal("a run from a deleted directory")
	}
}
