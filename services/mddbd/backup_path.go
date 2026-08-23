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

	// Resolve symlinks for the existing portion of the path; for non-existent
	// targets fall back to the parent directory's resolved form.
	resolved := candidate
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

	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s path escapes its directory", label)
	}
	return resolved, nil
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
