package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/internal/migrate"

	bolt "go.etcd.io/bbolt"
)

// AGT-005: what an install refuses, and how it puts things back when it fails.

// packBolt writes a bbolt file shaped by fill and returns its path.
func packBolt(t *testing.T, fill func(tx *bolt.Tx) error) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "collection.db")
	db, err := openBolt(path, &bolt.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(fill); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return path
}

// putKeys fills bucket with keys, each with a value.
func putKeys(bucket string, keys ...string) func(tx *bolt.Tx) error {
	return func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Put([]byte(k), []byte("v")); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestCheckPackFileRefusals(t *testing.T) {
	m := validManifest()
	docs := putKeys("docs", "doc|docs|1")
	cases := []struct {
		name string
		fill func(tx *bolt.Tx) error
		want string
	}{
		{"unreadable format", func(tx *bolt.Tx) error {
			_ = docs(tx)
			return putKeys(string(migrate.Bucket), "version")(tx)
		}, "storage format"},
		{"server data", func(tx *bolt.Tx) error { _ = docs(tx); return putKeys("users", "admin")(tx) }, `bucket "users"`},
		{"another collection's key", putKeys("docs", "doc|docs|1", "doc|other|2"), "doc|other|2"},
		{"a nested bucket", func(tx *bolt.Tx) error {
			b, _ := tx.CreateBucketIfNotExists([]byte("docs"))
			_ = b.Put([]byte("doc|docs|1"), []byte("v"))
			_, err := b.CreateBucket([]byte("doc|docs|nested"))
			return err
		}, "doc|docs|nested"},
		{"documents missing", putKeys("fts", "fts|docs|term|1"), "holds 0 documents"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPackFile(packBolt(t, tc.fill), m, []byte("docs"))
			if !errors.Is(err, errCorpusInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := checkPackFile(junkFile(t), m, []byte("docs")); !errors.Is(err, errCorpusInvalid) {
		t.Fatalf("a missing file was checked: %v", err)
	}
	if err := checkPackFile(packBolt(t, docs), m, []byte("docs")); err != nil {
		t.Fatalf("a valid file was refused: %v", err)
	}
}

// rawPack is a pack around any collection.db bytes, with a matching checksum.
func rawPack(t *testing.T, data []byte, change func(m *corpusManifest)) []byte {
	t.Helper()
	m := validManifest()
	sum := sha256.Sum256(data)
	m.SHA256, m.Bytes = hex.EncodeToString(sum[:]), int64(len(data))
	if change != nil {
		change(m)
	}
	raw, _ := json.Marshal(m)
	return tarZst(t, tarMember{name: corpusManifestName, body: raw}, tarMember{name: corpusDataName, body: data})
}

func TestInstallRefusesADamagedFile(t *testing.T) {
	s := corpusServer(t, 256)
	_, err := s.installCorpus(bytes.NewReader(rawPack(t, []byte("not a database at all"), nil)))
	if !errors.Is(err, errCorpusInvalid) || !strings.Contains(err.Error(), "not a usable database") {
		t.Fatalf("err = %v", err)
	}
	path := s.Path
	t.Cleanup(func() { s.Path = path })
	s.Path = filepath.Join(t.TempDir(), "missing", "x.db")
	if _, err := s.installCorpus(bytes.NewReader(nil)); err == nil || errors.Is(err, errCorpusInvalid) {
		t.Fatalf("staging in a missing directory: %v", err)
	}
}

func TestInstallOnAServerWithoutEmbeddingsKeepsFullText(t *testing.T) {
	src := corpusServer(t, 256)
	seedCorpus(t, src)
	pack := buildPack(t, src, "docs")
	dst, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	res, err := dst.installCorpus(bytes.NewReader(pack))
	if err != nil || !res.VectorsDisabled || !strings.Contains(res.Warnings[0], "no embedding provider") {
		t.Fatalf("install = %+v, %v", res, err)
	}
	if !dst.collectionVectorsDisabled("docs") || (&Server{}).collectionVectorsDisabled("docs") {
		t.Fatal("vectorsDisabled is not recorded")
	}
}

func TestInstallOverAWrittenCollectionPutsTheConfigBack(t *testing.T) {
	for _, layout := range corpusLayouts {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			src := corpusServer(t, 256)
			seedCorpus(t, src)
			pack := buildPack(t, src, "docs")

			dst := corpusServer(t, 256)
			addTestDoc(t, dst, "docs", "mine", "en", "written here", nil)
			assertInstallConflictKeepsConfig(t, dst, pack)
		})
	}
}

// assertInstallConflictKeepsConfig installs pack over a collection holding
// data, without a config and then with one, and checks the config is as
// it was each time.
func assertInstallConflictKeepsConfig(t *testing.T, dst *Server, pack []byte) {
	t.Helper()
	if _, err := dst.installCorpus(bytes.NewReader(pack)); !errors.Is(err, errCorpusConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if _, found := dst.CollectionManager.Get("docs"); found {
		t.Fatal("the failed install left a config behind")
	}
	_ = dst.CollectionManager.Set("docs", &CollectionConfig{Description: "kept"})
	if _, err := dst.installCorpus(bytes.NewReader(pack)); !errors.Is(err, errCorpusConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	if cfg, _ := dst.CollectionManager.Get("docs"); cfg.Description != "kept" || cfg.ReadOnly {
		t.Fatalf("config after the failed install: %+v", cfg)
	}
}

func TestAttachCorpusFailures(t *testing.T) {
	m := validManifest()
	if err := (&Server{}).attachCorpus(m, "x", false); err == nil {
		t.Fatal("installed without collection configs")
	}
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	_ = s.DB.Close()
	if err := s.attachCorpus(m, "x", false); err == nil {
		t.Fatal("installed with a closed database")
	}
}

func TestFillCorpusCollectionFailures(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	if err := s.fillCorpusCollection("docs", junkFile(t)); err == nil {
		t.Fatal("filled from a missing file")
	}
	// A file never checked has no format bucket to drop.
	if err := s.fillCorpusCollection("docs", packBolt(t, putKeys("docs", "doc|docs|1"))); err == nil {
		t.Fatal("filled from an unchecked file")
	}
}

func TestAttachRefusals(t *testing.T) {
	dir := t.TempDir()
	cf, err := openCollectionFiles(dir, collectionFileOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer cf.closeAll()
	if err := cf.attach("docs", filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("attached a file that does not exist")
	}
	junk := filepath.Join(t.TempDir(), "junk.db")
	_ = os.WriteFile(junk, []byte("not a database"), 0o600)
	if err := cf.attach("docs", junk); err == nil {
		t.Fatal("attached a file that is not a database")
	}
	if _, err := os.Stat(filepath.Join(dir, collectionFileName("docs"))); !os.IsNotExist(err) {
		t.Fatal("a refused file stayed in the collection directory")
	}
	if _, err := cf.getOrCreate("docs"); err != nil {
		t.Fatal(err)
	}
	if err := cf.attach("docs", junk); err == nil || !strings.Contains(err.Error(), "already has a file") {
		t.Fatalf("attached over an open file: %v", err)
	}
}

func TestRemoveCorpusFailures(t *testing.T) {
	if _, err := (&Server{}).removeCorpus("docs"); !errors.Is(err, errNotFound) {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	src := corpusServer(t, 256)
	seedCorpus(t, src)
	pack := buildPack(t, src, "docs")
	dst := corpusServer(t, 256)
	if _, err := dst.removeCorpus("docs"); !errors.Is(err, errNotFound) {
		t.Fatalf("removed a pack never installed: %v", err)
	}
	if _, err := dst.installCorpus(bytes.NewReader(pack)); err != nil {
		t.Fatal(err)
	}
	// The pack's file fails under the delete: the pack stays, read-only.
	_ = dst.collectionDB("docs").Close()
	if _, err := dst.removeCorpus("docs"); err == nil {
		t.Fatal("the delete's failure went unnoticed")
	}
	if !dst.collectionReadOnly("docs") {
		t.Fatal("a failed remove left the pack writable")
	}
	_ = dst.DB.Close()
	if _, err := dst.removeCorpus("docs"); err == nil {
		t.Fatal("removed with a closed database")
	}
	if got := (&Server{}).corpusPacks(context.Background()); len(got) != 0 {
		t.Fatalf("a server without configs lists %v", got)
	}
}

// junkFile is a file that is not a database.
func junkFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(path, []byte("not a database: "+strings.Repeat("x", 8192)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallRefusesAFileWithAnotherCollectionsKeys(t *testing.T) {
	s := corpusServer(t, 256)
	data, err := os.ReadFile(packBolt(t, putKeys("docs", "doc|docs|1", "doc|victim|2")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.installCorpus(bytes.NewReader(rawPack(t, data, nil))); !errors.Is(err, errCorpusInvalid) || !strings.Contains(err.Error(), "victim") {
		t.Fatalf("err = %v", err)
	}
	if s.collectionHasData("victim") {
		t.Fatal("the pack wrote into another collection")
	}
}
