package main

import (
	"os"
	"runtime"
	"testing"
)

// A directory under no listed mount has no known filesystem.
func TestCovKFilesystemType_NoMatchingMount(t *testing.T) {
	requirePOSIXPaths(t, "mountinfo path matching")
	mi := writeMountinfo(t, [][2]string{{"/data", "ext4"}})
	if got := filesystemType("/elsewhere", mi); got != "" {
		t.Errorf("filesystemType outside every mount = %q, want \"\"", got)
	}
}

// A relative directory cannot be resolved once the working directory is
// gone; that is not knowing the filesystem, not an error.
func TestCovKFilesystemType_UnresolvableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to remove the working directory")
	}
	mi := writeMountinfo(t, [][2]string{{"/", "ext4"}})
	gone := t.TempDir()
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if got := filesystemType("relative/dir", mi); got != "" {
		t.Errorf("filesystemType without a working directory = %q, want \"\"", got)
	}
}
