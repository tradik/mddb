package main

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/storage"
	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// #380: REST, gRPC and the MCP direct client truncate revision history through
// one function (truncateCollectionRevisions). These tests run the same
// collection through each surface and require the same surviving revisions and
// the same binlog deletions.

// truncateSurvivors lists, per document key, the timestamps of the revisions
// left in a collection.
func truncateSurvivors(t *testing.T, s *Server, coll string, keys []string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	_ = s.DBViewC(coll, func(tx *bolt.Tx) error {
		for _, key := range keys {
			prefix := storage.RevPrefix(coll, genID(coll, key, "en"))
			c := tx.Bucket([]byte("rev")).Cursor()
			out[key] = []string{}
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				out[key] = append(out[key], string(k[len(prefix):]))
			}
		}
		return nil
	})
	return out
}

// binlogRevDeletes counts the binlog deletions of revisions in a collection.
func binlogRevDeletes(t *testing.T, bl *binlog.Binlog, coll string) int {
	t.Helper()
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Type == binlog.BinlogDelete && e.BucketName == "rev" && bytes.HasPrefix(e.Key, []byte("rev|"+coll+"|")) {
			n++
		}
	}
	return n
}

// seedTruncateCollection adds three documents with 6, 3 and 1 extra
// revisions, the same for every collection it is given.
func seedTruncateCollection(t *testing.T, gs *GRPCServer, s *Server, coll string) []string {
	t.Helper()
	keys := []string{"a", "b", "c"}
	for i, key := range keys {
		addDocViaGRPC(t, gs, coll, key, "en", "body "+key, nil)
		seedFakeRevisions(t, s, coll, key, "en", []int{6, 3, 1}[i])
	}
	return keys
}

func TestTruncateRESTAndGRPCTrimIdentically(t *testing.T) {
	for _, tc := range []struct {
		name string
		keep int
	}{
		{"keep2", 2},
		{"keep0", 0},
		{"keepNegative", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gs, s, cleanup := newTestGRPCServer(t)
			defer cleanup()
			bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "binlog.db"), binlog.BinlogConfig{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bl.Close() })
			s.Binlog = bl

			keys := seedTruncateCollection(t, gs, s, "viarest")
			seedTruncateCollection(t, gs, s, "viagrpc")
			seedTruncateCollection(t, gs, s, "viamcp")
			before := truncateSurvivors(t, s, "viarest", keys)

			rec := doRequest(t, s.handleTruncate, TruncateRequest{Collection: "viarest", KeepRevs: tc.keep})
			if rec.Code != http.StatusOK {
				t.Fatalf("REST truncate: %d %s", rec.Code, rec.Body.String())
			}
			if _, err := gs.Truncate(context.Background(), &pb.TruncateRequest{Collection: "viagrpc", KeepRevs: safeInt32(tc.keep)}); err != nil {
				t.Fatalf("gRPC truncate: %v", err)
			}
			if _, err := NewDirectClient(s).Truncate(context.Background(), &MCPTruncateRequest{Collection: "viamcp", KeepRevs: tc.keep}); err != nil {
				t.Fatalf("MCP truncate: %v", err)
			}

			rest := truncateSurvivors(t, s, "viarest", keys)
			deleted := 0
			for _, key := range keys {
				want := before[key]
				if tc.keep >= 0 && len(want) > tc.keep {
					want = want[len(want)-tc.keep:]
				}
				if !slices.Equal(rest[key], want) {
					t.Errorf("REST %s: kept %v, want %v", key, rest[key], want)
				}
				deleted += len(before[key]) - len(want)
			}
			for _, coll := range []string{"viagrpc", "viamcp"} {
				got := truncateSurvivors(t, s, coll, keys)
				for _, key := range keys {
					if !slices.Equal(got[key], rest[key]) {
						t.Errorf("%s %s: kept %v, REST kept %v", coll, key, got[key], rest[key])
					}
				}
				if n := binlogRevDeletes(t, bl, coll); n != deleted {
					t.Errorf("%s: %d binlog deletions, want %d", coll, n, deleted)
				}
			}
			if n := binlogRevDeletes(t, bl, "viarest"); n != deleted {
				t.Errorf("REST: %d binlog deletions, want %d", n, deleted)
			}
		})
	}
}

// TestTruncateDamagedDocumentFailsOnEverySurface: a document that cannot be
// decoded fails the truncate on REST and gRPC alike, and nothing is deleted.
func TestTruncateDamagedDocumentFailsOnEverySurface(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()
	keys := seedTruncateCollection(t, gs, s, "bad")
	if err := s.DBUpdateC("bad", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("bad", "zz-damaged"), []byte{0xff, 0x00})
	}); err != nil {
		t.Fatal(err)
	}
	before := truncateSurvivors(t, s, "bad", keys)

	if rec := doRequest(t, s.handleTruncate, TruncateRequest{Collection: "bad", KeepRevs: 0}); rec.Code != http.StatusBadRequest {
		t.Errorf("REST: %d, want 400", rec.Code)
	}
	if _, err := gs.Truncate(context.Background(), &pb.TruncateRequest{Collection: "bad"}); err == nil {
		t.Error("gRPC truncate succeeded over a damaged document")
	}
	after := truncateSurvivors(t, s, "bad", keys)
	for _, key := range keys {
		if !slices.Equal(after[key], before[key]) {
			t.Errorf("%s: revisions changed by a failed truncate: %v -> %v", key, before[key], after[key])
		}
	}
}

// TestDropOldestRevisionsReportsADeleteFailure: a delete refused by bolt (here,
// in a read-only transaction) is returned, not ignored.
func TestDropOldestRevisionsReportsADeleteFailure(t *testing.T) {
	db := newRevTestDB(t)
	seedRevs(t, db, "c", "d", 3)
	err := db.View(func(tx *bolt.Tx) error {
		return dropOldestRevisions(tx.Bucket([]byte("rev")), nil, "c", "d", 1)
	})
	if err == nil {
		t.Fatal("dropOldestRevisions ignored a failed delete")
	}
}

// TestTruncateCollectionRevisionsReportsADeleteFailure: the collection-wide
// truncate stops at the first refused delete and returns it.
func TestTruncateCollectionRevisionsReportsADeleteFailure(t *testing.T) {
	gs, s, cleanup := newTestGRPCServer(t)
	defer cleanup()
	seedTruncateCollection(t, gs, s, "ro")
	err := s.DBViewC("ro", func(tx *bolt.Tx) error {
		return truncateCollectionRevisions(tx, nil, "ro", 0)
	})
	if err == nil {
		t.Fatal("truncateCollectionRevisions ignored a failed delete")
	}
}
