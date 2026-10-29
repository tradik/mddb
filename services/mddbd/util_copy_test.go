package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile_FailsWhenTheDestinationDirectoryIsMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "missing", "destination")
	if err := copyFile(src, dst); err == nil {
		t.Fatal("copyFile into a directory that does not exist succeeded")
	}
	if _, err := os.Stat(filepath.Dir(dst)); !os.IsNotExist(err) {
		t.Fatalf("the missing directory appeared: %v", err)
	}
}

// A source that opens but cannot be read — a directory — fails the copy and
// leaves no temp file behind.
func TestCopyFile_FailsWhenTheSourceCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(src, 0o750); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "destination")
	if err := copyFile(src, dst); err == nil {
		t.Fatal("copying a directory succeeded")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("a failed copy left a destination: %v", err)
	}
	assertNoTempLeftovers(t, dst, "a copy whose source cannot be read")
}

// Wrappers that expose only Unwrap (unwrapOnly, sse_middleware_test.go) all
// the way down: the walk has to step through each.
func TestHTTPFlusherFindsTheFlusherBehindAWrapper(t *testing.T) {
	rec := httptest.NewRecorder()
	f, ok := httpFlusher(unwrapOnly{unwrapOnly{rec}})
	if !ok || f == nil {
		t.Fatal("the recorder's Flusher behind two wrappers was not found")
	}
	f.Flush()
	if !rec.Flushed {
		t.Fatal("Flush did not reach the recorder")
	}
}
