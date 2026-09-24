package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeBackupPath_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)

	cases := []string{
		"../etc/passwd",
		"../../etc/passwd",
		"/etc/passwd",
		"foo/../../etc/passwd",
		"",
		"with\x00null",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := safeBackupPath(name, false); err == nil {
				t.Fatalf("expected error for %q", name)
			}
		})
	}
}

func TestSafeBackupPath_AcceptsRelative(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)

	got, err := safeBackupPath("snap-1.db", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolvedDir, _ := filepath.EvalSymlinks(dir)
	if !strings.HasPrefix(got, resolvedDir) {
		t.Fatalf("path %q not inside %q", got, resolvedDir)
	}
}

func TestSafeBackupPath_RestoreRequiresExisting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)

	if _, err := safeBackupPath("missing.db", true); err == nil {
		t.Fatal("expected error when file is missing")
	}

	target := filepath.Join(dir, "snap.db")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := safeBackupPath("snap.db", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafeBackupPath_RejectsSymlinkEscape(t *testing.T) {
	jail := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(jail, "escape")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	t.Setenv("MDDB_BACKUP_DIR", jail)

	if _, err := safeBackupPath("escape", true); err == nil {
		t.Fatal("expected error for symlink that escapes the jail")
	}
}

// A path outside the jail must get one answer, whatever is at the other end.
//
// The filesystem was consulted before the containment check, so a restore
// naming ../../etc/shadow was told "not a regular file" or "not found" or
// "escapes its directory" depending on what existed there — an oracle for the
// existence and type of any file on the host. Every such path now gets the
// same error, produced before anything outside the jail is touched.
func TestAnEscapingPathRevealsNothingAboutWhatItNames(t *testing.T) {
	jail := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", jail)

	outside := t.TempDir()
	regular := filepath.Join(outside, "exists.db")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(outside, "a-directory")
	if err := os.Mkdir(subdir, 0o750); err != nil {
		t.Fatal(err)
	}

	targets := map[string]string{
		"an existing regular file": regular,
		"an existing directory":    subdir,
		"nothing at all":           filepath.Join(outside, "absent.db"),
	}

	var first string
	for what, target := range targets {
		for _, requireExisting := range []bool{true, false} {
			_, err := safeBackupPath(target, requireExisting)
			if err == nil {
				t.Fatalf("%s outside the jail was accepted", what)
			}
			msg := err.Error()
			if strings.Contains(msg, "not found") || strings.Contains(msg, "regular file") || strings.Contains(msg, outside) {
				t.Errorf("%s (requireExisting=%v): %q says something about the target", what, requireExisting, msg)
			}
			if first == "" {
				first = msg
			} else if msg != first {
				t.Errorf("%s (requireExisting=%v): %q differs from %q — the difference is the leak",
					what, requireExisting, msg, first)
			}
		}
	}
}

// The jail has two spellings — as configured, and resolved — and an absolute
// path in the configured spelling is inside it. The pre-check added in 2.15.1
// compared against the resolved spelling only, and Windows CI caught it: a
// temp directory resolves from RUNNER~1 to the long name, so a CSV inside the
// configured directory was reported as escaping. macOS would do the same with
// /var and /private/var, where CI does not run the tests. A symlinked
// directory reproduces both on any platform.
func TestAPathInTheConfiguredSpellingOfTheJailIsInside(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "jail")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	t.Setenv("MDDB_BACKUP_DIR", link)

	target := filepath.Join(link, "snapshot.db")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, requireExisting := range []bool{true, false} {
		if _, err := safeBackupPath(target, requireExisting); err != nil {
			t.Errorf("requireExisting=%v: a file inside the configured directory was refused: %v", requireExisting, err)
		}
	}
}
