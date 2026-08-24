package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// GO-032. bbolt fsyncs every commit, so an acknowledged write survives a
// crash — provided the bytes went somewhere that survives too. The case these
// cover is a container started without a volume: writes acknowledged, fsynced,
// and gone when the container is removed.

// writeMountinfo builds a /proc/self/mountinfo fixture from mount point and
// filesystem pairs, in the real format.
func writeMountinfo(t *testing.T, mounts [][2]string) string {
	t.Helper()
	var b strings.Builder
	for i, m := range mounts {
		// id parent major:minor root MOUNT_POINT opts - FSTYPE source super
		b.WriteString(strings.Join([]string{
			"3" + string(rune('0'+i)), "2", "0:5", "/", m[0], "rw,relatime", "-", m[1], "none", "rw",
		}, " ") + "\n")
	}
	path := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// requirePOSIXPaths skips a test whose subject is POSIX path and permission
// semantics.
//
// filesystemType parses /proc/self/mountinfo and compares absolute paths.
// On Windows filepath.Abs("/data") is "C:\\data", so no fixture entry can
// match and the function returns "" — which is the honest production answer
// there: MDDB does not know what filesystem it is on, and says so rather than
// guessing. What cannot be asserted on Windows is the matching itself.
func requirePOSIXPaths(t *testing.T, what string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skipf("%s is POSIX-specific; on Windows filesystemType returns \"\" by design", what)
	}
}

func TestFilesystemTypePicksTheMostSpecificMount(t *testing.T) {
	requirePOSIXPaths(t, "mountinfo path matching")
	mi := writeMountinfo(t, [][2]string{
		{"/", "overlay"},
		{"/data", "ext4"},
		{"/data/cache", "tmpfs"},
	})

	for dir, want := range map[string]string{
		"/data/cache":     "tmpfs", // exact match on the deepest mount
		"/data/cache/sub": "tmpfs", // below it
		"/data":           "ext4",
		"/data/mddb":      "ext4",
		"/var/lib/mddb":   "overlay", // falls back to the root mount
		"/":               "overlay",
		"/datax":          "overlay", // not under /data despite the prefix
	} {
		if got := filesystemType(dir, mi); got != want {
			t.Errorf("filesystemType(%q) = %q, want %q", dir, got, want)
		}
	}
}

// Not knowing the filesystem is a reason to stay quiet, not to refuse to run.
func TestFilesystemTypeToleratesAMissingMountinfo(t *testing.T) {
	if got := filesystemType("/data", filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("a missing mountinfo should yield \"\", got %q", got)
	}
}

func TestFilesystemTypeSkipsMalformedLines(t *testing.T) {
	requirePOSIXPaths(t, "mountinfo path matching")
	path := filepath.Join(t.TempDir(), "mountinfo")
	content := "garbage\n" +
		"1 2 0:5 / /data rw - ext4 none rw\n" +
		"short line -\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if got := filesystemType("/data", path); got != "ext4" {
		t.Errorf("a valid line among malformed ones should still be found, got %q", got)
	}
}

func TestEphemeralFilesystemsAreRecognised(t *testing.T) {
	for _, fs := range []string{"tmpfs", "ramfs", "overlay", "overlay2", "aufs"} {
		if _, ok := ephemeralFilesystems[fs]; !ok {
			t.Errorf("%s should be treated as ephemeral", fs)
		}
	}
	for _, fs := range []string{"ext4", "xfs", "btrfs", "zfs", "nfs"} {
		if _, ok := ephemeralFilesystems[fs]; ok {
			t.Errorf("%s should not be treated as ephemeral", fs)
		}
	}
}

func TestCheckPersistenceOnARealDirectory(t *testing.T) {
	dir := t.TempDir()
	status := CheckPersistence(filepath.Join(dir, "mddb.db"))

	if status.Path != dir {
		t.Errorf("Path = %q, want %q", status.Path, dir)
	}
	if !status.Writable {
		t.Error("a temp directory should be writable")
	}
	if status.FreeBytes == 0 {
		t.Error("free space should have been measured")
	}
	for _, w := range status.Warnings {
		if w.Code == "not_writable" {
			t.Errorf("unexpected warning: %+v", w)
		}
	}
}

func TestCheckPersistenceReportsAnUnwritableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.Chmod on Windows toggles the read-only attribute and does not
		// remove write access for the owner, so the directory this test makes
		// unwritable stays writable and isWritable is right to say so. Testing
		// the same property there needs an ACL, which is a different subject
		// from the one this test is about.
		t.Skip("Unix mode bits do not restrict the owner on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory, so the check cannot be exercised")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0500); err != nil {
		t.Fatal(err)
	}

	status := CheckPersistence(filepath.Join(dir, "mddb.db"))
	if status.Writable {
		t.Fatal("a directory without write permission should not report writable")
	}
	if status.Durable() {
		t.Error("an unwritable directory cannot be durable")
	}
	var found bool
	for _, w := range status.Warnings {
		if w.Code == "not_writable" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a not_writable warning, got %+v", status.Warnings)
	}
}

func TestLowDiskSpaceWarns(t *testing.T) {
	// A threshold far above any real free space forces the branch.
	t.Setenv("MDDB_DISK_MIN_FREE", "9223372036854775807")
	status := CheckPersistence(filepath.Join(t.TempDir(), "mddb.db"))

	var found bool
	for _, w := range status.Warnings {
		if w.Code == "low_disk_space" {
			found = true
			if !strings.Contains(w.Detail, "MB free") {
				t.Errorf("the warning should say how much is free: %q", w.Detail)
			}
		}
	}
	if !found {
		t.Errorf("expected a low_disk_space warning, got %+v", status.Warnings)
	}
}

func TestDiskThresholdCanBeDisabled(t *testing.T) {
	t.Setenv("MDDB_DISK_MIN_FREE", "0")
	for _, w := range CheckPersistence(filepath.Join(t.TempDir(), "mddb.db")).Warnings {
		if w.Code == "low_disk_space" {
			t.Error("MDDB_DISK_MIN_FREE=0 should disable the free-space warning")
		}
	}
}

func TestDurableRequiresWritableAndPersistent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status PersistenceStatus
		want   bool
	}{
		{"writable and persistent", PersistenceStatus{Writable: true}, true},
		{"ephemeral", PersistenceStatus{Writable: true, Ephemeral: true}, false},
		{"not writable", PersistenceStatus{}, false},
		{"neither", PersistenceStatus{Ephemeral: true}, false},
	} {
		if got := tc.status.Durable(); got != tc.want {
			t.Errorf("%s: Durable() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLogPersistenceStatusReportsEveryWarning(t *testing.T) {
	var codes []string
	logPersistenceStatus(PersistenceStatus{
		Path: "/data",
		Warnings: []PersistenceWarning{
			{Code: "ephemeral_storage", Detail: "on tmpfs"},
			{Code: "low_disk_space", Detail: "almost full"},
		},
	}, func(_ string, args ...any) {
		for i := 0; i+1 < len(args); i += 2 {
			if args[i] == "code" {
				codes = append(codes, args[i+1].(string))
			}
		}
	})

	if len(codes) != 2 || codes[0] != "ephemeral_storage" || codes[1] != "low_disk_space" {
		t.Errorf("every warning should be logged with its code, got %v", codes)
	}
}

func TestLogPersistenceStatusSaysNothingWhenAllIsWell(t *testing.T) {
	called := false
	logPersistenceStatus(PersistenceStatus{Path: "/data", Writable: true},
		func(string, ...any) { called = true })
	if called {
		t.Error("a healthy data directory should produce no warnings")
	}
}
