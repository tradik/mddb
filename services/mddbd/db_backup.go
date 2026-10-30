package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	bolt "go.etcd.io/bbolt"
)

// backupTo writes a consistent copy of the database to dst and proves it can
// be opened before putting it there (#266).
//
// The HTTP and MCP backups copied the database file while the server was
// writing to it. A copy taken that way can hold pages from before and after
// any number of commits, and bbolt refuses it — on restore, not on backup:
//
//	freepages: failed to get all reachable pages (key[0]=... on branch page(258913)
//	needs to be < than key of the next element in ancestor ...)
//
// Reported on two nightly backups taken two days apart. The gRPC backup was
// already copying through a read transaction, which sees one commit and only
// that; all three now share this function.
//
// The copy is then checked the way a restore would open it. A copy of a
// healthy database always passes, so a failure here means the live database
// itself is damaged (#270) — which is worth knowing while it is still
// serving, before a restart finds out. MDDB_BACKUP_VERIFY=false skips the
// check for databases too large to walk on every backup.
//
// dst must lie inside MDDB_BACKUP_DIR. Every caller passes safeBackupPath's
// output, and backupCollections confines it again itself, so nothing below
// writes to a path that did not come out of that check. The writes then go
// through an os.Root on dst's directory: the temporary file, the rename and
// the collection files cannot leave that directory, even through a symlink
// created in it after the path was checked.
func (s *Server) backupTo(dst string) error { return s.backupCollections(dst, nil) }

// backupCollections is backupTo with, in the per-collection layout, only the
// files of the collections in only copied — every collection when only is
// nil. A scheduled backup of a few collections (OPS-023) is this.
func (s *Server) backupCollections(dst string, only []string) error {
	// Everything below writes to what safeBackupPath returns, on this
	// function's own data flow (SonarCloud gosecurity:S2083): a path outside
	// MDDB_BACKUP_DIR is refused here even if a caller forgot to confine it.
	safeDst, err := safeBackupPath(dst, false)
	if err != nil {
		return err
	}
	dir, err := os.OpenRoot(filepath.Dir(safeDst))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	name := filepath.Base(safeDst)
	if err := backupFile(s.DB, &s.restoreMu, dir, name); err != nil {
		return err
	}
	// #336: with one file per collection, each collection file is copied
	// into "<dst>.d/" under its own name. Every file is a consistent copy;
	// the files are copied one after another, not at one instant.
	if cf := s.collectionStore(); cf != nil {
		return s.backupCollectionFiles(cf, dir, filepath.Base(collectionDir(name)), only)
	}
	return nil
}

// backupCollectionFiles copies the collection files in only (every one when
// only is nil) into the directory sub of dir.
func (s *Server) backupCollectionFiles(cf *collectionFiles, dir *os.Root, sub string, only []string) error {
	_ = dir.Mkdir(sub, 0o700)        // already there is fine; OpenRoot reports anything else
	subDir, err := dir.OpenRoot(sub) // os.Root: sub is a base name of safeDst and cannot leave dir
	if err != nil {
		return err
	}
	defer func() { _ = subDir.Close() }()
	for _, c := range cf.names() {
		db := cf.get(c)
		if db == nil || (only != nil && !slices.Contains(only, c)) {
			continue
		}
		if err := backupFile(db, &s.restoreMu, subDir, collectionFileName(c)); err != nil {
			return fmt.Errorf("collection %q: %w", c, err)
		}
	}
	return nil
}

// backupFile writes a consistent copy of db to name in dir, verified before
// it replaces name.
func backupFile(db *bolt.DB, restoreMu *sync.RWMutex, dir *os.Root, name string) error {
	tmpName := name + ".tmp-" + rand.Text()
	tmp, err := dir.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(tmpName) }() // a no-op once renamed

	restoreMu.RLock()
	err = db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(tmp)
		return err
	})
	restoreMu.RUnlock()
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("writing the backup: %w", err)
	}

	if os.Getenv("MDDB_BACKUP_VERIFY") != "false" {
		if err := verifyDatabase(filepath.Join(dir.Name(), tmpName)); err != nil {
			return fmt.Errorf("the live database is damaged — a consistent copy of it %w; "+
				"take no restart until it is repaired (mddbd -repair-db, see docs/BACKUP.md)", err)
		}
	}
	return dir.Rename(tmpName, name) // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
}
