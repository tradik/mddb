package main

import (
	"path/filepath"
	"testing"
	"time"

	"mddb/internal/audit"
	"mddb/internal/automationlog"
	"mddb/internal/boltx"
	"mddb/internal/spell"

	bolt "go.etcd.io/bbolt"
)

// After a restore the server's handle is a new *bolt.DB. Managers built on
// the old one kept it — closed — and every write to configs, keys or
// automations failed until a restart. They now follow the server's handle.

func TestManagersFollowTheDatabaseAcrossARestore(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	// The restore opens a new handle that cleanup does not know about;
	// Windows cannot remove the temp directory while it is open.
	defer func() { _ = s.DB.Close() }()
	s.CollectionManager.FollowDB(func() *bolt.DB { return s.DB })
	s.AutomationManager = NewAutomationManager(s.DB)
	s.AutomationManager.FollowDB(func() *bolt.DB { return s.DB })
	_ = s.AutomationManager.EnsureBucket()
	keys := newMCPAPIKeyStore(s.DB)
	keys.FollowDB(func() *bolt.DB { return s.DB })

	if err := s.CollectionManager.Set("kept", &CollectionConfig{Type: "default"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "b.db")
	if err := s.backupTo(backup); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectionManager.Set("after-backup", &CollectionConfig{Type: "default"}); err != nil {
		t.Fatal(err)
	}
	old := s.DB
	if err := s.restoreFromBackup(backup); err != nil {
		t.Fatal(err)
	}
	if s.DB == old {
		t.Fatal("the restore did not replace the handle; the test proves nothing")
	}

	// The in-memory configs are the restored database's.
	if _, ok := s.CollectionManager.Get("after-backup"); ok {
		t.Error("a config written after the backup survived the restore")
	}
	if _, ok := s.CollectionManager.Get("kept"); !ok {
		t.Error("a config in the backup is missing after the restore")
	}
	// And writes go to the new file.
	if err := s.CollectionManager.Set("written-after-restore", &CollectionConfig{Type: "default"}); err != nil {
		t.Errorf("collection config write after restore: %v", err)
	}
	if s.AutomationManager.handle() != s.DB || s.AutomationManager.LoadAll() != nil {
		t.Error("the automation manager still uses the old handle")
	}
	if _, _, err := keys.Create("k", 0); err != nil {
		t.Errorf("MCP key write after restore: %v", err)
	}

	if boltx.Current(old, nil) != old || boltx.Current(old, func() *bolt.DB { return nil }) != old {
		t.Error("Current without a usable source should keep the original")
	}
}

func TestEveryServerManagerFollowsTheHandle(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	other := s.DB
	src := func() *bolt.DB { return other }

	auth := NewAuthManager(s.DB, AuthConfig{})
	auth.FollowDB(src)
	cur := NewCurationManager(s.DB)
	cur.FollowDB(src)
	spellM := spell.NewSpellManager(s.DB)
	spellM.FollowDB(src)
	logs := automationlog.NewStore(s.DB, time.Hour)
	logs.FollowDB(src)
	aud := audit.NewAuditManager(s.DB, true, 1)
	aud.FollowDB(src)
	for name, err := range map[string]error{
		"auth": auth.EnsureBuckets(), "curation": cur.EnsureBucket(), "spell": spellM.EnsureBucket(),
		"automation log": logs.EnsureBucket(), "audit": aud.EnsureBuckets(),
	} {
		if err != nil {
			t.Errorf("%s through its followed handle: %v", name, err)
		}
	}
}
