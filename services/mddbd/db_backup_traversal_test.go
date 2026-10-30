package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testBackupDir makes a fresh MDDB_BACKUP_DIR for one test and returns it:
// backupCollections refuses to write anywhere else (gosecurity:S2083).
func testBackupDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)
	return dir
}

// SonarCloud gosecurity:S2083 on backupCollections: the path it writes to is
// confined to MDDB_BACKUP_DIR by the function itself, not only by its
// callers. Traversal, an absolute path elsewhere, a symlink out of the jail
// and Windows-style separators are refused, and nothing lands outside; a
// plain name, relative or absolute inside the jail, still works.
func TestBackupCollectionsRefusesAPathOutsideTheBackupDir(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	parent := t.TempDir()
	jail := filepath.Join(parent, "jail")
	if err := os.Mkdir(jail, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDDB_BACKUP_DIR", jail)
	outside := t.TempDir()

	refused := []string{
		filepath.Join(jail, "..", "escape.db"),
		filepath.Join("..", "escape.db"),
		filepath.Join(outside, "absolute.db"),
	}
	if err := os.Symlink(outside, filepath.Join(jail, "link")); err == nil {
		refused = append(refused, filepath.Join(jail, "link", "through-a-link.db"))
	}
	if filepath.Separator == '\\' {
		refused = append(refused, `..\escape.db`, `sub\..\..\escape.db`)
	}
	for _, dst := range refused {
		if err := s.backupCollections(dst, nil); err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("backupCollections(%q) = %v, want it refused", dst, err)
		}
	}
	// On Unix a backslash is part of a file name: the backup stays in the jail.
	if filepath.Separator == '/' {
		if err := s.backupCollections(`..\escape.db`, nil); err != nil {
			t.Errorf("a name with a backslash: %v", err)
		}
		if _, err := os.Stat(filepath.Join(jail, `..\escape.db`)); err != nil {
			t.Errorf("the backslash name did not land in the jail: %v", err)
		}
	}
	for _, d := range []string{parent, outside} {
		if found, _ := filepath.Glob(filepath.Join(d, "*.db")); len(found) != 0 {
			t.Errorf("a backup escaped into %s: %v", d, found)
		}
	}

	for _, dst := range []string{"plain.db", filepath.Join(jail, "absolute-inside.db")} {
		if err := s.backupCollections(dst, nil); err != nil {
			t.Errorf("backupCollections(%q): %v", dst, err)
		}
	}
	for _, name := range []string{"plain.db", "absolute-inside.db"} {
		if _, err := os.Stat(filepath.Join(jail, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
}

// The rest of the jail's error paths: its symlinks cannot be resolved, and a
// name the jail accepts whose temporary file cannot be created (a name one
// short of the limit, which the ".tmp-..." suffix takes over it).
func TestBackupJailErrorPaths(t *testing.T) {
	testBackupDir(t)
	evalJailSymlinks = func(string) (string, error) { return "", errors.New("loop") }
	_, err := safeBackupPath("x.db", false)
	evalJailSymlinks = filepath.EvalSymlinks
	if err == nil || !strings.Contains(err.Error(), "resolve backup dir symlinks") {
		t.Errorf("unresolvable jail: %v", err)
	}

	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.backupTo(strings.Repeat("n", 240)); err == nil {
		t.Error("a backup whose temporary name is too long succeeded")
	}

	t.Setenv("MDDB_GEO_DATA_DIR", "")
	if got := geoDataDir(); got != "geodata" {
		t.Errorf("default geo data dir = %q", got)
	}
}

// confinedJoin, the last step of confineToDir: it returns the cleaned path
// inside root and refuses one that leaves it, also for a root that already
// ends in a separator.
func TestConfinedJoin(t *testing.T) {
	root := t.TempDir()
	for rel, want := range map[string]string{
		".":            root,
		"a.db":         filepath.Join(root, "a.db"),
		"x/../b.db":    filepath.Join(root, "b.db"),
		"sub/./c.db":   filepath.Join(root, "sub", "c.db"),
		"sub/../../..": "",
		"../d.db":      "",
	} {
		got, err := confinedJoin(root, rel, "backup")
		if want == "" {
			if err == nil || !strings.Contains(err.Error(), "backup path escapes") {
				t.Errorf("confinedJoin(%q) = %q, %v; want refused", rel, got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("confinedJoin(%q) = %q, %v; want %q", rel, got, err, want)
		}
	}
	top := filepath.VolumeName(root) + string(filepath.Separator)
	if got, err := confinedJoin(top, "e.db", "backup"); err != nil || got != top+"e.db" {
		t.Errorf("under the file system root: %q, %v", got, err)
	}
}
