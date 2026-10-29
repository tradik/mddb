package automationlog

import (
	"testing"

	bolt "go.etcd.io/bbolt"
)

// A scheduled backup's run (OPS-023) is logged with its action, target and
// what retention removed; an entry that cannot be decoded is skipped by List
// and Count rather than failing them; a page size of 0 means the default.
func TestBackupEntryAndUnreadableEntries(t *testing.T) {
	ls, done := setupLogStoreTest(t, 0)
	defer done()
	if err := ls.Log(Entry{RuleID: "nightly", RuleType: "cron", Status: "success", Action: "backup",
		Target: "s3://b/mddb-20261030T030000Z.db", Removed: []string{"mddb-20261023T030000Z.db"}}); err != nil {
		t.Fatal(err)
	}
	if err := ls.handle().Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAutomationLog).Put([]byte("00000000000000000001|bad"), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}

	got, _, err := ls.List(0, "", "", "")
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v %v", got, err)
	}
	e := got[0]
	if e.Action != "backup" || e.Target != "s3://b/mddb-20261030T030000Z.db" || len(e.Removed) != 1 {
		t.Errorf("backup entry round trip: %+v", e)
	}
	if n, err := ls.Count("nightly", ""); err != nil || n != 1 {
		t.Errorf("count: %d %v", n, err)
	}
}

// A store whose database is closed reports it from List.
func TestListOnAClosedDatabase(t *testing.T) {
	ls, done := setupLogStoreTest(t, 0)
	defer done()
	_ = ls.handle().Close()
	if _, _, err := ls.List(10, "", "", ""); err == nil {
		t.Error("listing a closed database succeeded")
	}
}
