package main

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
)

// backupTarget is where a scheduled backup goes (OPS-023). A run stages the
// backup locally, hashes it, stores it on the target, then prunes the target.
type backupTarget interface {
	// stage returns the directory and file name the backup named name is
	// written to on local disk.
	stage(name string) (dir, base string, err error)
	// store puts the hashed set on the target, proves it arrived intact, and
	// says where it is.
	store(set *backupSet, name string) (where string, err error)
	// discard drops what the run left on local disk: a staging copy always,
	// the backup itself only when the run failed.
	discard(dir, base string, failed bool)
	// list names the backups the target holds, in no particular order.
	list() ([]string, error)
	// remove deletes one backup from the target.
	remove(name string) error
}

// newBackupTarget returns the target an action names; normalize has already
// accepted the action.
func newBackupTarget(a *BackupAction) (backupTarget, error) {
	if a.Target == BackupTargetS3 {
		return newS3BackupTarget(a)
	}
	return &localBackupTarget{path: a.Path}, nil
}

// mkdirInBackupDir creates rel, a directory inside MDDB_BACKUP_DIR, through an
// os.Root so a symlink in the way cannot carry it outside.
func mkdirInBackupDir(rel string) error {
	if err := os.MkdirAll(backupDir(), 0o750); err != nil {
		return err
	}
	root, err := os.OpenRoot(backupDir())
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.MkdirAll(rel, 0o750) // NOSONAR gosecurity:S2083 -- os.Root: rel cannot leave MDDB_BACKUP_DIR
}

// stageIn is stage for a directory rel inside MDDB_BACKUP_DIR.
func stageIn(rel, base string) (string, string, error) {
	if err := mkdirInBackupDir(rel); err != nil {
		return "", "", err
	}
	dst, err := safeBackupPath(filepath.Join(rel, base), false)
	if err != nil {
		return "", "", err
	}
	return filepath.Dir(dst), filepath.Base(dst), nil
}

// localBackupTarget keeps backups in a directory inside MDDB_BACKUP_DIR,
// where POST /v1/restore finds them.
type localBackupTarget struct {
	path string // relative to MDDB_BACKUP_DIR
	dir  string // its resolved path, set by stage
}

func (t *localBackupTarget) stage(name string) (string, string, error) {
	dir, base, err := stageIn(cmp.Or(t.path, "."), name) // "": MDDB_BACKUP_DIR itself
	t.dir = dir
	return dir, base, err
}

// store has nothing to move: the backup was written where it stays, and its
// checksum file was just written from the files as they are on disk.
func (t *localBackupTarget) store(set *backupSet, _ string) (string, error) {
	return filepath.Join(set.dir, set.name), nil
}

func (t *localBackupTarget) discard(dir, base string, failed bool) {
	if failed {
		_ = removeBackupSet(dir, base)
	}
}

func (t *localBackupTarget) list() ([]string, error) {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func (t *localBackupTarget) remove(name string) error {
	return removeBackupSet(t.dir, name)
}

// pruneBackups deletes the oldest of the action's backups on t until keep are
// left, never current — the one this run just stored. It runs only after a
// successful run, so a failing schedule never eats into the backups it has.
// Names that are not this action's backups are never touched.
func pruneBackups(t backupTarget, a *BackupAction, current string) ([]string, error) {
	if a.Keep == 0 {
		return nil, nil
	}
	all, err := t.list()
	if err != nil {
		return nil, err
	}
	names := slices.DeleteFunc(all, func(n string) bool { return !a.isBackupName(n) })
	slices.Sort(names) // the UTC stamp sorts in time order
	var removed []string
	for _, n := range names[:max(0, len(names)-a.Keep)] {
		if n == current {
			continue
		}
		if err := t.remove(n); err != nil {
			return removed, err
		}
		removed = append(removed, n)
	}
	return removed, nil
}
