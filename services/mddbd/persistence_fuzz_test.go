package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
	proto "mddb/proto"
)

// TEST-003, persistence round trip.
//
// The decoder fuzzers ask whether one record survives. This asks whether the
// *store* survives a sequence: writes, overwrites, deletes and a reopen, with
// the invariants checked after each restart.
//
// The class it hunts is the one GO-001 and GO-010 were: an index that stops
// agreeing with the documents it points at. That never shows up as a crash —
// it shows up as a search returning a document that was deleted, or missing one
// that was not, weeks later.

// opKind is what a fuzzed byte gets turned into.
type opKind uint8

const (
	opPut opKind = iota
	opOverwrite
	opDelete
	opReopen
	opKindCount
)

func FuzzPersistenceRoundTrip(f *testing.F) {
	// Seeds are operation programs: each byte picks an op, each key picks a
	// document. Small alphabets keep collisions frequent, which is where
	// overwrite-and-delete bugs live.
	f.Add([]byte{0, 0, 0}, []byte("abc"))
	f.Add([]byte{0, 1, 2}, []byte("aa"))
	f.Add([]byte{0, 3, 2, 3}, []byte("ab"))
	f.Add([]byte{0, 0, 2, 3, 0}, []byte("aab"))
	f.Add([]byte{2}, []byte("a"))
	f.Add([]byte{}, []byte{})

	f.Fuzz(func(t *testing.T, program, keyBytes []byte) {
		// Bound the work: this target is about invariants, not throughput.
		if len(program) > 64 || len(keyBytes) == 0 || len(keyBytes) > 16 {
			return
		}

		srv, cleanup := fuzzPersistenceServer(t)
		defer cleanup()

		// live mirrors what the store should contain, maintained by the test
		// rather than read back from it — otherwise the check would just be
		// the implementation agreeing with itself.
		live := map[string]string{}

		for i, b := range program {
			// Hex, not %c: document keys resolve case-insensitively
			// (genID lowercases), so 'A' and 'a' would name the same
			// document while the mirror below kept them apart. That
			// behaviour is real and now filed as DOC-016; this target is
			// about persistence, not about rediscovering it every run.
			key := fmt.Sprintf("doc-%02x", keyBytes[i%len(keyBytes)])

			switch opKind(b % byte(opKindCount)) {
			case opPut, opOverwrite:
				content := fmt.Sprintf("content-%d", i)
				if err := fuzzPut(srv, key, content); err != nil {
					t.Fatalf("step %d: put %s: %v", i, key, err)
				}
				live[key] = content

			case opDelete:
				if err := fuzzDelete(srv, key); err != nil {
					t.Fatalf("step %d: delete %s: %v", i, key, err)
				}
				delete(live, key)

			case opReopen:
				// The point of the whole target: everything written so far
				// must still be there after the file is closed and opened.
				if err := fuzzReopen(srv); err != nil {
					t.Fatalf("step %d: reopen: %v", i, err)
				}
			}

			assertStoreMatches(t, srv, live, i)
		}

		// One final reopen, because a store that is only ever read while warm
		// hides everything that was never flushed.
		if err := fuzzReopen(srv); err != nil {
			t.Fatalf("final reopen: %v", err)
		}
		assertStoreMatches(t, srv, live, -1)
	})
}

// assertStoreMatches checks the two invariants that matter: every live document
// reads back with its content, and the metadata index points only at documents
// that exist.
func assertStoreMatches(t *testing.T, srv *Server, live map[string]string, step int) {
	t.Helper()

	for key, want := range live {
		doc, err := srv.loadDocByRef("fuzz", key, "en")
		if err != nil || doc == nil {
			t.Fatalf("step %d: %s was written but cannot be read: %v", step, key, err)
		}
		// Reading back a *different* document is worse than not reading one:
		// an overwrite that half-applied leaves a plausible wrong answer.
		if doc.ContentMD != want {
			t.Fatalf("step %d: %s reads back as %q, want %q", step, key, doc.ContentMD, want)
		}
	}

	err := srv.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(srv.BucketNames.Docs)
		bIdx := tx.Bucket(srv.BucketNames.IdxMeta)
		if bDocs == nil || bIdx == nil {
			return nil
		}

		// Every metadata index entry names a document id. A deleted document
		// leaving its index entries behind is how a search starts returning
		// things that are gone (GO-001).
		//
		// The id is whatever follows the known prefix — it contains '|'
		// itself (collection|key|lang), so splitting the key on '|' would
		// read only its last segment.
		prefix := storage.MetaKeyPrefix("fuzz", "kind", "prose")
		c := bIdx.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			docID := string(k[len(prefix):])
			if bDocs.Get(storage.DocKey("fuzz", docID)) == nil {
				return fmt.Errorf("index entry %q points at document %q, which does not exist", k, docID)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("step %d: %v", step, err)
	}
}

func fuzzPut(srv *Server, key, content string) error {
	bp := NewBatchProcessor(srv, 1)
	resp, err := bp.ProcessBatch(context.Background(), "fuzz", []*proto.BatchDocument{
		makeBatchDoc(key, "en", content, map[string]*proto.MetaValues{
			"kind": {Values: []string{"prose"}},
		}, false),
	})
	if err != nil {
		return err
	}
	if resp.Failed > 0 {
		return fmt.Errorf("%v", resp.Errors)
	}
	return nil
}

func fuzzDelete(srv *Server, key string) error {
	ref, err := srv.loadCodeDocByKey("fuzz", key)
	if err != nil || ref == nil {
		return nil // deleting what was never there is not an error
	}
	return srv.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(srv.BucketNames.Docs)
		bByK := tx.Bucket(srv.BucketNames.ByKey)
		bIdx := tx.Bucket(srv.BucketNames.IdxMeta)

		if err := bDocs.Delete(storage.DocKey("fuzz", ref.DocID)); err != nil {
			return err
		}
		if err := bByK.Delete(storage.ByKeyKey("fuzz", key, ref.Lang)); err != nil {
			return err
		}
		// Index entries must go with the document, which is the invariant
		// assertStoreMatches then verifies.
		prefix := []byte("meta|fuzz|")
		c := bIdx.Cursor()
		var stale [][]byte
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			if bytes.HasSuffix(k, []byte("|"+ref.DocID)) {
				stale = append(stale, bytes.Clone(k))
			}
		}
		for _, k := range stale {
			if err := bIdx.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// fuzzReopen closes the database and opens it again from the same file.
func fuzzReopen(srv *Server) error {
	path := srv.Path
	if err := srv.DB.Close(); err != nil {
		return err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return err
	}
	srv.DB = db
	if srv.CollectionManager != nil {
		srv.CollectionManager = NewCollectionManager(db)
		if err := srv.CollectionManager.EnsureBucket(); err != nil {
			return err
		}
	}
	return nil
}

func fuzzPersistenceServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)
	if _, err := os.Stat(srv.Path); err != nil {
		cleanup()
		t.Fatalf("test server has no database file: %v", err)
	}
	return srv, cleanup
}
