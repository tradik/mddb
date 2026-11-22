package main

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Backup files on the server's disk (FE-024): the backups in MDDB_BACKUP_DIR,
// listed with their size and date, and one of them opened for download.
//
// Only the backup directory itself is read, never a directory inside it (a
// scheduled rule's `path`) and never object storage (a rule's s3 target).
// Only names the server writes backups under are listed or opened: a single
// file name ending in ".db", a regular file, not a symlink, not the live
// database. Everything is read through an os.Root on the directory, so a
// name, or a symlink swapped in after the check, cannot lead out of it.

// backupFileExt is the extension of every backup the server names itself:
// GET /v1/backup's default, the scheduled backups and the gRPC Backup.
const backupFileExt = ".db"

// States of a listed backup.
const (
	// backupStateChecksummed: a checksum file was written beside it, which
	// a scheduled run does only after the backup passed the integrity check.
	backupStateChecksummed = "checksummed"
	// backupStateUnknown: nothing on disk records a check. GET /v1/backup
	// checks every copy before keeping it, but records nothing.
	backupStateUnknown = "unknown"
)

// errBackupNotFound answers every name that is not a listed backup — unknown,
// refused or unreadable alike, so the answer tells nothing about the disk.
var errBackupNotFound = errors.New("backup not found")

// backupFileRow is one row of the backup list.
type backupFileRow struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"` // what POST /v1/restore takes, and a scheduled run logs as its target
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
	State      string    `json:"state"`
	// CollectionFiles counts the files in "<name>.d/" (per-collection
	// layout). They stay on the server: a download is the main file only.
	CollectionFiles int `json:"collectionFiles"`
}

// isBackupFileName reports whether name is a single file name of the kind the
// server writes backups under: no separator, no "..", not absolute, no NUL,
// ending in ".db".
func isBackupFileName(name string) bool {
	return len(name) > len(backupFileExt) && strings.HasSuffix(name, backupFileExt) &&
		filepath.IsLocal(name) && !strings.ContainsAny(name, "/\\\x00")
}

// openBackupDir opens MDDB_BACKUP_DIR as an os.Root and returns it with its
// resolved path, the spelling safeBackupPath gives the files in it.
func openBackupDir() (*os.Root, string, error) {
	dir, err := filepath.Abs(backupDir())
	if err == nil {
		dir, err = evalJailSymlinks(dir)
	}
	if err != nil {
		return nil, "", err
	}
	root, err := openJail(dir)
	return root, dir, err
}

// sameFileAs returns a test that reports whether a file is the one at path:
// the live database, which is never listed nor served. An empty or missing
// path matches nothing.
func sameFileAs(path string) func(fs.FileInfo) bool {
	live, err := os.Stat(cmp.Or(path, "\x00"))
	return func(info fs.FileInfo) bool { return err == nil && os.SameFile(live, info) }
}

// listBackupFiles returns the backups in MDDB_BACKUP_DIR, newest first, and
// how many there are; only the rows from offset, at most limit, are read in
// full. livePath is the live database. A missing directory holds none.
func listBackupFiles(livePath string, offset, limit int) ([]backupFileRow, int, error) {
	root, dir, err := openBackupDir()
	if errors.Is(err, fs.ErrNotExist) {
		return []backupFileRow{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, 0, err
	}
	isLive := sameFileAs(livePath)
	present := make(map[string]bool, len(entries))
	rows := []backupFileRow{}
	for _, e := range entries {
		present[e.Name()] = true
		if !e.Type().IsRegular() || !isBackupFileName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || isLive(info) {
			continue // gone since the directory was read, or the live database
		}
		rows = append(rows, backupFileRow{Name: e.Name(), Path: filepath.Join(dir, e.Name()),
			Bytes: info.Size(), ModifiedAt: info.ModTime().UTC()})
	}
	slices.SortFunc(rows, func(a, b backupFileRow) int {
		return cmp.Or(b.ModifiedAt.Compare(a.ModifiedAt), strings.Compare(b.Name, a.Name))
	})
	total := len(rows)
	start := min(offset, total)
	page := rows[start : start+min(limit, total-start)]
	for i := range page {
		describeBackupFile(root, &page[i], present)
	}
	return page, total, nil
}

// describeBackupFile fills a row's state and its collection files.
func describeBackupFile(root *os.Root, row *backupFileRow, present map[string]bool) {
	row.State = backupStateUnknown
	if present[sumFileName(row.Name)] {
		row.State = backupStateChecksummed
	}
	entries, _ := fs.ReadDir(root.FS(), collectionDir(row.Name)) // none is fine
	for _, e := range entries {
		if e.Type().IsRegular() {
			row.CollectionFiles++
		}
	}
}

// openBackupFile opens the backup name in MDDB_BACKUP_DIR for reading. Any
// name that is not a listed backup is errBackupNotFound.
func openBackupFile(name, livePath string) (*os.File, fs.FileInfo, error) {
	if !isBackupFileName(name) {
		return nil, nil, errBackupNotFound
	}
	root, _, err := openBackupDir()
	if err != nil {
		return nil, nil, errBackupNotFound
	}
	defer func() { _ = root.Close() }()
	// Lstat first: a symlink is refused even when it points inside.
	if info, err := root.Lstat(name); err != nil || !info.Mode().IsRegular() { // NOSONAR gosecurity:S2083 -- os.Root: name cannot leave the directory
		return nil, nil, errBackupNotFound
	}
	f, err := root.Open(name) // NOSONAR gosecurity:S2083 -- os.Root: name cannot leave the directory
	if err != nil {
		return nil, nil, errBackupNotFound
	}
	// What was opened, not what was looked at: a file swapped in between
	// the two is judged again.
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || sameFileAs(livePath)(info) {
		_ = f.Close()
		return nil, nil, errBackupNotFound
	}
	return f, info, nil
}
