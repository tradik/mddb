package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// GO-051: the binlog takes writes in the order the database commits them.
//
// Writers of one key used to commit and then append outside any lock, so
// the log could hold B before A while the database kept B: a follower that
// replayed the log ended with A. The test races writers on one document and
// checks that the log has the writes' revisions in the order they were
// committed (each revision key is one past the last committed one), that
// the last version of the document in the log is the one in the database,
// and that a follower replaying the log ends with it too.
func TestBinlogOrderMatchesCommitOrder(t *testing.T) {
	const writers, writes, rounds = 16, 100, 3
	for round := range rounds {
		s := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "order.db"))
		// A log that syncs on every append holds its lock for the sync;
		// writers that have committed queue on it, and nothing but the
		// order of the commits themselves says who goes first.
		bl, err := binlog.NewBinlog(s.Path, binlog.BinlogConfig{RetentionInterval: -1, Sync: binlog.SyncCommit})
		if err != nil {
			t.Fatal(err)
		}
		s.Binlog = bl
		noSync(t, s)

		var wg sync.WaitGroup
		for w := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range writes {
					body := fmt.Sprintf("writer %d write %d", w, i)
					if _, _, err := s.addDocument("race", "same", "en", nil, body, 0, true); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
		wg.Wait()

		assertRevisionsInCommitOrder(t, bl, round)
		stored, logged := docBytes(t, s), lastLoggedDoc(t, bl)
		if !bytes.Equal(stored, logged) {
			t.Fatalf("round %d: the log's last version of the document is not the stored one:\nstored %q\nlogged %q", round, stored, logged)
		}
		replayed := replayToFollower(t, bl)
		if !bytes.Equal(stored, replayed) {
			t.Fatalf("round %d: a follower replaying the log ends with another version:\nleader   %q\nfollower %q", round, stored, replayed)
		}
		_ = bl.Close()
	}
}

func raceDocID() string  { return genID("race", "same", "en") }
func raceDocKey() []byte { return storage.DocKey("race", raceDocID()) }

// noSync turns off the sync on commit of the files a write to "race" goes
// to: commits microseconds long make the window between a commit and its
// append matter.
func noSync(t *testing.T, s *Server) {
	t.Helper()
	s.DB.NoSync = true
	db, err := s.collectionDBForWrite("race")
	if err != nil {
		t.Fatal(err)
	}
	db.NoSync = true
}

// assertRevisionsInCommitOrder checks that the document's revision keys are
// strictly increasing in log order.
func assertRevisionsInCommitOrder(t *testing.T, bl *binlog.Binlog, round int) {
	t.Helper()
	prefix := storage.RevPrefix("race", raceDocID())
	var prev []byte
	n := 0
	if err := bl.ScanFrom(0, func(e *binlog.BinlogEntry) error {
		if e.Type != binlog.BinlogPut || e.BucketName != "rev" || !bytes.HasPrefix(e.Key, prefix) {
			return nil
		}
		n++
		if prev != nil && bytes.Compare(e.Key, prev) <= 0 {
			return fmt.Errorf("revision %s logged after %s (write %d)", e.Key[len(prefix):], prev[len(prefix):], n)
		}
		prev = e.Key
		return nil
	}); err != nil {
		t.Fatalf("round %d: the log's order is not the commit order: %v", round, err)
	}
}

// docBytes is the stored document, as the database holds it.
func docBytes(t *testing.T, s *Server) []byte {
	t.Helper()
	var out []byte
	if err := s.DBViewC("race", func(tx *bolt.Tx) error {
		out = bytes.Clone(tx.Bucket([]byte("docs")).Get(raceDocKey()))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// lastLoggedDoc is the value of the document's last Put in the log.
func lastLoggedDoc(t *testing.T, bl *binlog.Binlog) []byte {
	t.Helper()
	var last []byte
	key := raceDocKey()
	if err := bl.ScanFrom(0, func(e *binlog.BinlogEntry) error {
		if e.Type == binlog.BinlogPut && e.BucketName == "docs" && bytes.Equal(e.Key, key) {
			last = e.Value
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return last
}

// replayToFollower applies the whole log to a fresh follower and returns
// the document it ends with.
func replayToFollower(t *testing.T, bl *binlog.Binlog) []byte {
	t.Helper()
	f := newHandlerTestServerAt(t, filepath.Join(t.TempDir(), "follower.db"))
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewReplicationApplier(f).ApplyBatch(entries); err != nil {
		t.Fatal(err)
	}
	return docBytes(t, f)
}
