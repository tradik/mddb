package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// backupDir returns the directory where backup files must live.
// Configurable via MDDB_BACKUP_DIR; defaults to "./backups".
func backupDir() string {
	if d := strings.TrimSpace(os.Getenv("MDDB_BACKUP_DIR")); d != "" {
		return d
	}
	return "backups"
}

// safeBackupPath validates that name resolves to a regular file inside backupDir().
// It defends against path traversal (`../`, absolute paths, symlinks escaping the
// jail) on the user-controlled `to`/`from` parameters of backup/restore endpoints.
// It returns the cleaned absolute path safe to pass to os.Open / os.Create.
//
// `requireExisting` is true for restore (the file must already be present and be
// a regular file); false for backup (the file may not yet exist, but its parent
// directory must resolve inside the jail).
func safeBackupPath(name string, requireExisting bool) (string, error) {
	return confineToDir(backupDir(), name, requireExisting, "backup")
}

// confineToDir resolves name inside root and refuses anything that escapes it.
//
// Generalised from safeBackupPath when CodeQL's go/path-injection turned up a
// second endpoint taking a filesystem path from a request — the geo reindexer,
// which had no jail at all. The symlink handling here is the part worth not
// writing twice: both the root and the candidate are resolved, and for a path
// that does not exist yet the parent is resolved instead, so a symlink planted
// anywhere along the way cannot point out of the directory.
//
// `requireExisting` is true when the file must already be present and regular
// (restore, loading a CSV); false when it may not exist yet but its parent must
// resolve inside the jail (writing a backup). `label` names the jail in errors.
func confineToDir(dir, name string, requireExisting bool, label string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("empty %s path", label)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid %s path", label)
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s dir: %w", label, err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create %s dir: %w", label, err)
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s dir symlinks: %w", label, err)
	}

	// Treat `name` as relative to the backup dir. Reject anything that, after
	// joining and cleaning, escapes the jail.
	candidate := name
	if filepath.IsAbs(candidate) {
		candidate = filepath.Clean(candidate)
	} else {
		candidate = filepath.Join(rootResolved, candidate)
	}
	candidate = filepath.Clean(candidate)

	// Refuse a path outside the jail before touching the filesystem. The stat
	// below used to come first, and it answered a question nobody should be
	// able to ask: for any path on the host — ../../etc/shadow — it returned
	// "not found", "not a regular file" or, only after that, "escapes its
	// directory", so the three errors told a caller whether a file existed and
	// what kind it was before the containment check ever ran. The lexical check
	// costs nothing and settles every such path with one answer; the check at
	// the end still catches a symlink inside the jail that points out of it.
	//
	// The directory has two spellings — as configured, and with its symlinks
	// resolved — and a caller may use either: on Windows a temp directory
	// resolves from its 8.3 short name (RUNNER~1) to the long one, on macOS
	// /var resolves to /private/var. The pre-check accepts a path inside
	// either spelling; both name the jail, so nothing outside it is stat'ed.
	if !withinDir(rootResolved, candidate) && !withinDir(root, candidate) {
		return "", fmt.Errorf("%s path escapes its directory", label)
	}

	// Resolve symlinks for the existing portion of the path; for non-existent
	// targets fall back to the parent directory's resolved form.
	resolved := candidate
	// #nosec G703 -- candidate passed withinDir above: this stat cannot reach
	// outside the jail, which TestAnEscapingPathRevealsNothingAboutWhatItNames
	// proves rather than asserts. gosec does not trace a sanitizer through a
	// helper function.
	if info, statErr := os.Lstat(candidate); statErr == nil {
		if requireExisting && !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s path is not a regular file", label)
		}
		if r, rerr := filepath.EvalSymlinks(candidate); rerr == nil {
			resolved = r
		}
	} else if requireExisting {
		return "", fmt.Errorf("%s not found: %w", label, statErr)
	} else {
		parent := filepath.Dir(candidate)
		pr, perr := filepath.EvalSymlinks(parent)
		if perr != nil {
			return "", fmt.Errorf("resolve %s parent: %w", label, perr)
		}
		resolved = filepath.Join(pr, filepath.Base(candidate))
	}

	if !withinDir(rootResolved, resolved) {
		return "", fmt.Errorf("%s path escapes its directory", label)
	}
	return resolved, nil
}

// withinDir reports whether p lies inside root, lexically. Both must already be
// cleaned; symlinks are the caller's business.
func withinDir(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// geoDataDir returns the directory postcode CSVs must live in.
// Configurable via MDDB_GEO_DATA_DIR; defaults to "./geodata".
func geoDataDir() string {
	if d := strings.TrimSpace(os.Getenv("MDDB_GEO_DATA_DIR")); d != "" {
		return d
	}
	return "geodata"
}

// safeGeoCSVPath confines a postcode CSV path to geoDataDir().
//
// The geo reindex endpoint took this path straight from the request body and
// handed it to os.Open. Over gRPC the call at least required write permission
// on a collection; over HTTP it required nothing at all, and neither is
// authority to read an arbitrary file. Reported by CodeQL as go/path-injection
// once the Go analysis started working.
func safeGeoCSVPath(name string) (string, error) {
	return confineToDir(geoDataDir(), name, true, "postcode CSV")
}
