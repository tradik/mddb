package binlog

import (
	"errors"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// GO-052: the entry gate refuses a write by what it writes, inside its
// transaction, so nothing is committed or logged.
func TestEntryGateRefusesByEntries(t *testing.T) {
	bl, db := openLogged(t, BinlogConfig{})
	refused := errors.New("placed elsewhere")
	bl.SetEntryGate(func(entries []*BinlogEntry) error {
		for _, e := range entries {
			if string(e.Key) == "elsewhere" {
				return refused
			}
		}
		return nil
	})
	if err := Logged(db.Update, bl, put("elsewhere")); !errors.Is(err, refused) {
		t.Fatalf("gated write: %v", err)
	}
	if err := Logged(db.Update, bl, put("here")); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() != 1 {
		t.Fatalf("lsn %d, want only the admitted write", bl.CurrentLSN())
	}
	bl.SetEntryGate(nil)
	if err := Logged(db.Update, bl, put("elsewhere")); err != nil {
		t.Fatal(err)
	}
	_ = db.View(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte("b")).Get([]byte("elsewhere")) == nil {
			t.Error("the write after removing the gate is missing")
		}
		return nil
	})
}
