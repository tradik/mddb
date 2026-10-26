package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMmapHeadroom(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.db")
	f, err := os.Create(big) // #nosec G304 -- a test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2 << 30); err != nil { // sparse: no disk used
		t.Fatal(err)
	}
	_ = f.Close()
	small := filepath.Join(dir, "small.db")
	_ = os.WriteFile(small, []byte("x"), 0o600)

	want := (2 << 30) + (2<<30)/4
	if got := headroomFor(big, "linux", 64); got != want {
		t.Errorf("big file: %d, want %d", got, want)
	}
	for name, got := range map[string]int{
		"small":   headroomFor(small, "linux", 64),
		"missing": headroomFor(filepath.Join(dir, "none"), "linux", 64),
		"windows": headroomFor(big, "windows", 64),
		"32-bit":  headroomFor(big, "linux", 32),
	} {
		if got != 0 {
			t.Errorf("%s: %d, want 0", name, got)
		}
	}
	if o := boltOptionsFor(small); o.InitialMmapSize != 100*1024*1024 {
		t.Errorf("main file default map: %d", o.InitialMmapSize)
	}
	if o := collectionFileOptions(small); o.InitialMmapSize != 0 {
		t.Errorf("small collection file map: %d", o.InitialMmapSize)
	}
	_ = mmapHeadroom(big)
}
