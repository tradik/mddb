package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/migrate"

	bolt "go.etcd.io/bbolt"
)

// AGT-005: what a build refuses, and how its snapshot fails.

func TestPrepareCorpusRefusals(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			s := corpusServer(t, 256)
			seedCorpus(t, s)
			_ = s.CollectionManager.Set("s3coll", &CollectionConfig{StorageBackend: "s3"})
			_ = s.CollectionManager.Set("secret", &CollectionConfig{Encrypted: true})
			_ = s.CollectionManager.Set("docs", &CollectionConfig{Description: "packable"})
			if err := (&Server{}).packableCollection("docs"); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				req  corpusBuildRequest
				want error
			}{
				{corpusBuildRequest{Collection: "docs"}, errCorpusInvalid},
				{corpusBuildRequest{Collection: "a b", License: "MIT"}, errCorpusInvalid},
				{corpusBuildRequest{Collection: "s3coll", License: "MIT"}, errCorpusInvalid},
				{corpusBuildRequest{Collection: "secret", License: "MIT"}, errCorpusInvalid},
				{corpusBuildRequest{Collection: "missing", License: "MIT"}, errNotFound},
			} {
				if b, err := s.prepareCorpus(tc.req); !errors.Is(err, tc.want) {
					if b != nil {
						b.close()
					}
					t.Fatalf("prepareCorpus(%+v) = %v, want %v", tc.req, err, tc.want)
				}
			}
		})
	}
}

func TestPrepareCorpusWorkDirFailures(t *testing.T) {
	s := corpusServer(t, 256)
	if (&Server{}).corpusWorkDir() != os.TempDir() {
		t.Fatal("a server without a database file stages elsewhere than the temp dir")
	}
	path := s.Path
	s.Path = filepath.Join(t.TempDir(), "missing", "x.db")
	if _, err := s.prepareCorpus(corpusBuildRequest{Collection: "docs", License: "MIT"}); err == nil {
		t.Fatal("a build staged in a directory that does not exist")
	}
	s.Path = path
	corpusOpenRoot = func(string) (*os.Root, error) { return nil, errors.New("no root") }
	t.Cleanup(func() { corpusOpenRoot = os.OpenRoot })
	if _, _, err := s.newCorpusWork(); err == nil {
		t.Fatal("the staging root's failure went unnoticed")
	}
}

func TestSnapshotFailures(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s := corpusServer(t, 256)
	seedCorpus(t, s)
	root := emptyRoot(t)
	if err := root.Mkdir("taken", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.snapshotCollection("docs", root, "taken"); err == nil {
		t.Fatal("a snapshot was written over a directory")
	}
	if err := s.describeSnapshot(filepath.Join(root.Name(), "taken"), validManifest()); err == nil {
		t.Fatal("a directory was described as a snapshot")
	}

	// A destination that refuses writes: the buckets exist, the keys cannot go in.
	path := filepath.Join(t.TempDir(), "ro.db")
	db, err := openBolt(path, &bolt.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.extractCollection("other", db) // every bucket created, nothing copied
	_ = db.Close()
	ro, err := openBolt(path, &bolt.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if err := s.extractCollection("docs", ro); err == nil {
		t.Fatal("keys were copied into a read-only file")
	}
	corpusBatchBytes = 1
	t.Cleanup(func() { corpusBatchBytes = migrateBatchBytes })
	if err := s.extractCollection("docs", ro); err == nil {
		t.Fatal("keys were copied into a read-only file in small batches")
	}
	closed, _ := openBolt(filepath.Join(t.TempDir(), "c.db"), &bolt.Options{})
	_ = closed.Close()
	if err := s.extractCollection("docs", closed); err == nil {
		t.Fatal("a closed file took the buckets")
	}
	open, _ := openBolt(filepath.Join(t.TempDir(), "e.db"), &bolt.Options{})
	defer func() { _ = open.Close() }()
	kb := &keyBatch{db: open, bucket: []byte("docs")}
	_ = open.Update(func(tx *bolt.Tx) error { _, err := tx.CreateBucket([]byte("docs")); return err })
	if err := kb.put(nil, []byte("v")); err == nil || kb.flush() != nil {
		t.Fatal("an empty key was stored")
	}
}

func TestBuildDescribesTheSnapshotsFormat(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	s := corpusServer(t, 256)
	seedCorpus(t, s)
	if err := s.collectionDB("docs").Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(migrate.Bucket)
		if err != nil {
			return err
		}
		return b.Put([]byte("version"), []byte("bad"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareCorpus(corpusBuildRequest{Collection: "docs", License: "MIT"}); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("a snapshot with an unreadable format was packed: %v", err)
	}
}

func TestBuildOfAFullTextOnlyCollection(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s, cleanup := newHandlerTestServer(t) // no embedding provider: no vectors
	t.Cleanup(cleanup)
	addTestDoc(t, s, "plain", "a", "en", "just words", nil)
	b, err := s.prepareCorpus(corpusBuildRequest{Collection: "plain", License: "MIT"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	if b.manifest.Embedding != nil {
		t.Fatalf("embedding %+v", b.manifest.Embedding)
	}
	if b.manifest.DocCount != 1 || b.manifest.SchemaVersion != migrate.Latest() || len(b.manifest.SHA256) != 64 {
		t.Fatalf("manifest %+v", b.manifest)
	}
}
