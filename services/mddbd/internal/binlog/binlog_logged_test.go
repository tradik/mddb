package binlog

import (
	"errors"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// GO-051: writes logged inside their transaction.

func openLogged(t *testing.T, cfg BinlogConfig) (*Binlog, *bolt.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := bolt.Open(filepath.Join(dir, "d.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg.RetentionInterval = -1
	bl, err := NewBinlog(filepath.Join(dir, "d.db"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bl.Close() })
	return bl, db
}

func put(key string) func(*bolt.Tx, *BinlogOps) error {
	return func(tx *bolt.Tx, bo *BinlogOps) error {
		b, err := tx.CreateBucketIfNotExists([]byte("b"))
		if err != nil {
			return err
		}
		bo.Put("b", []byte(key), []byte("v"))
		return b.Put([]byte(key), []byte("v"))
	}
}

func TestLoggedWritesInsideTheTransaction(t *testing.T) {
	bl, db := openLogged(t, BinlogConfig{Sync: SyncCommit})
	var stableInside uint64
	update := func(f func(*bolt.Tx) error) error {
		err := db.Update(f)
		stableInside = bl.StableLSN() // committed, not yet settled
		return err
	}
	if err := Logged(update, bl, put("a")); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() != 1 || stableInside != 0 || bl.StableLSN() != 1 {
		t.Fatalf("lsn %d stable inside %d after %d", bl.CurrentLSN(), stableInside, bl.StableLSN())
	}
	// Already synced, and a closed log has nothing left to sync.
	if err := bl.syncThrough(1); err != nil {
		t.Fatal(err)
	}
	// Without a log the write is just the transaction; a failing one logs
	// nothing.
	if err := Logged(db.Update, nil, put("b")); err != nil {
		t.Fatal(err)
	}
	fail := errors.New("no")
	if err := Logged(db.Update, bl, func(*bolt.Tx, *BinlogOps) error { return fail }); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if err := Logged(db.Update, bl, func(*bolt.Tx, *BinlogOps) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if bl.CurrentLSN() != 1 {
		t.Fatalf("lsn %d", bl.CurrentLSN())
	}
	_ = bl.Close()
	if err := bl.syncThrough(5); !errors.Is(err, ErrBinlogClosed) {
		t.Fatalf("sync of a closed log: %v", err)
	}
	// A closed log takes the write's entries no more, and the database
	// keeps serving.
	if err := Logged(db.Update, bl, put("c")); err != nil {
		t.Fatalf("a write with a closed log: %v", err)
	}
}

func TestACommitThatFailsAfterLoggingStopsTheLog(t *testing.T) {
	bl, db := openLogged(t, BinlogConfig{})
	commitFails := errors.New("disk full")
	update := func(f func(*bolt.Tx) error) error {
		_ = db.Update(f)
		return commitFails
	}
	if err := Logged(update, bl, put("a")); !errors.Is(err, commitFails) {
		t.Fatal(err)
	}
	if bl.Err() == nil {
		t.Fatal("the log took a write that did not commit and carried on")
	}
	if bl.StableLSN() != 1 {
		t.Fatalf("stable %d", bl.StableLSN())
	}
}

func TestTheGateAndTheAckWaiter(t *testing.T) {
	bl, db := openLogged(t, BinlogConfig{})
	refused := errors.New("not the leader")
	bl.SetWriteGate(func() error { return refused })
	if err := Logged(db.Update, bl, put("a")); !errors.Is(err, refused) {
		t.Fatalf("gated write: %v", err)
	}
	if err := bl.Append(&BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte("x")}); !errors.Is(err, refused) {
		t.Fatalf("gated append: %v", err)
	}
	if !bl.Dirty() {
		t.Fatal("a refused append after its commit does not mark the log")
	}
	bl.SetWriteGate(nil)
	waited := errors.New("no quorum")
	var got uint64
	bl.SetAckWaiter(func(lsn uint64) error { got = lsn; return waited })
	if err := Logged(db.Update, bl, put("b")); !errors.Is(err, waited) || got != 1 {
		t.Fatalf("ack waiter: %v at %d", err, got)
	}
	bl.SetAckWaiter(nil)
	if err := Logged(db.Update, bl, put("c")); err != nil {
		t.Fatal(err)
	}
}

func TestASlowSubscriberMissesEntriesItReadsBack(t *testing.T) {
	bl, _ := openLogged(t, BinlogConfig{})
	ch := bl.Subscribe("slow")
	batch := make([]*BinlogEntry, binlogSubscriberCap+1)
	for i := range batch {
		batch[i] = &BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte("k")}
	}
	if err := bl.AppendBatch(batch); err != nil {
		t.Fatal(err)
	}
	if len(ch) != binlogSubscriberCap {
		t.Fatalf("subscriber holds %d", len(ch))
	}
}

func TestACommitModeSyncFailureFailsTheWrite(t *testing.T) {
	bl, db := openLogged(t, BinlogConfig{Sync: SyncCommit})
	inject(t, func(o *fileOps) { o.sync = failingSync })
	if err := Logged(db.Update, bl, put("a")); !errors.Is(err, errInjected) {
		t.Fatalf("got %v", err)
	}
}
