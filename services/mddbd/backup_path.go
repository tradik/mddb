package main

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
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
	rootResolved, err := evalJailSymlinks(root)
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

	// Everything from here goes through an os.Root on the jail. A path, or a
	// symlink on it, that leads out of the directory is refused by the open
	// itself, so nothing outside the jail is ever looked at — the check is
	// the system's, not a comparison of strings made before the look.
	jail, err := openJail(rootResolved)
	if err != nil {
		return "", fmt.Errorf("resolve %s dir: %w", label, err)
	}
	defer func() { _ = jail.Close() }()
	rel, err := filepath.Rel(rootResolved, candidate)
	if err != nil || !withinDir(rootResolved, candidate) {
		rel, _ = filepath.Rel(root, candidate) // the configured spelling
	}

	info, statErr := jail.Stat(rel) // NOSONAR gosecurity:S6549 -- rel passed withinDir, and os.Root refuses a path out of the jail
	switch {
	case statErr == nil:
		// Not only on restore: a backup written to the jail itself, or to a
		// directory in it, put its temporary file beside that directory —
		// for the jail, outside it.
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s path is not a regular file", label)
		}
	case !errors.Is(statErr, fs.ErrNotExist):
		return "", fmt.Errorf("%s path escapes its directory", label)
	case requireExisting:
		return "", fmt.Errorf("%s not found: %w", label, statErr)
	default:
		parent, perr := jail.Stat(filepath.Dir(rel)) // NOSONAR gosecurity:S6549 -- as above
		if perr != nil || !parent.IsDir() {
			return "", fmt.Errorf("resolve %s parent: %w", label, cmp.Or(perr, errors.New("not a directory")))
		}
	}
	return confinedJoin(rootResolved, rel, label)
}

// confinedJoin joins rel to root and returns the cleaned result only when it
// is still root or inside it.
//
// confineToDir has already refused every path that leaves the jail, so this
// never refuses one of its results. It is the last step on purpose: the value
// confineToDir returns is the one this check returned, in the Clean-then-
// prefix form taint analysers recognise as a path sanitiser (SonarCloud
// gosecurity:S2083 on backupCollections did not follow withinDir and the
// os.Root opens above). The callers write to what comes out of here.
func confinedJoin(root, rel, label string) (string, error) {
	cleaned := filepath.Clean(filepath.Join(root, rel))
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if cleaned != root && !strings.HasPrefix(cleaned, prefix) {
		return "", fmt.Errorf("%s path escapes its directory", label)
	}
	return cleaned, nil
}

// openJail is os.OpenRoot; a test replaces it to reach the error path.
var openJail = os.OpenRoot

// evalJailSymlinks is filepath.EvalSymlinks; a test replaces it to reach the
// error path, which a directory MkdirAll has just made cannot otherwise take.
var evalJailSymlinks = filepath.EvalSymlinks

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
