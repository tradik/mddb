package main

import (
	"fmt"
	"os"
	"path/filepath"

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
// dst must be safeBackupPath's output; every caller passes it. CodeQL raises
// go/path-injection on the CreateTemp and Rename below (alerts #62, #63) for
// the reason given at copyFile in util.go: it does not model safeBackupPath's
// symlink-resolved filepath.Rel check as a barrier. Dismissed as false
// positives, as the copyFile alerts were.
func (s *Server) backupTo(dst string) error {
	// #nosec G703 -- every caller passes safeBackupPath's output; see copyFile
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // #nosec G703 -- os.CreateTemp produced name; no-op once renamed

	err = s.DBView(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(tmp)
		return err
	})
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
		if err := verifyDatabase(name); err != nil {
			return fmt.Errorf("the live database is damaged — a consistent copy of it %w; "+
				"take no restart until it is repaired (mddbd -repair-db, see docs/BACKUP.md)", err)
		}
	}
	return os.Rename(name, dst) // #nosec G703 -- dst is safeBackupPath's output
}
