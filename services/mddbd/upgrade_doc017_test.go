package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/migrate"
	"mddb/internal/storage"
)

// DOC-017: a database 2.18.8 wrote — keys stored in the index as written —
// opens, is migrated, and finds every document under any spelling.
//
// The fixtures (test/upgrade-fixtures/v2.18.8*) were written by the 2.18.8
// binary: "README.md" and then "readme.md" (one document, two index
// entries), "Guide/Intro.MD" in language "EN", "plain.md", "TODO.md" in the
// collection "Notes", and "Gone.md" deleted as "gone.md", which left its
// entry behind.

// fixtureLookups are the reads every migrated fixture must answer.
var fixtureLookups = []struct {
	collection, key, lang, content string
}{
	{"docs", "readme.md", "en", "Lower-case spelling"},
	{"docs", "README.md", "en", "Lower-case spelling"},
	{"docs", "guide/intro.md", "en", "Mixed case key"},
	{"docs", "Guide/Intro.MD", "EN", "Mixed case key"},
	{"docs", "plain.md", "en", "Already lower case"},
	{"Notes", "todo.md", "pl", "Collection name with upper case"},
	{"Notes", "ToDo.MD", "PL", "Collection name with upper case"},
}

// unpackPerCollectionFixture unpacks the per-collection fixture and returns
// the main file's path; the collection files are beside it in <path>.d.
func unpackPerCollectionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, "mddb.db")
	gunzipFixture(t, "v2.18.8-per-collection/mddb.db.gz", main)
	if err := os.MkdirAll(collectionDir(main), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docs.db", "%4Eotes.db"} {
		gunzipFixture(t, "v2.18.8-per-collection/mddb.db.d/"+name+".gz", filepath.Join(collectionDir(main), name))
	}
	return main
}

// fixtureForLayout unpacks the v2.18.8 fixture of the layout the tests run
// in and returns its main file.
func fixtureForLayout(t *testing.T) string {
	t.Helper()
	if perCollectionLayout() {
		return unpackPerCollectionFixture(t)
	}
	return unpackFixture(t, "v2.18.8")
}

// serverOnFixture opens path the way the server does at startup, migration
// included.
func serverOnFixture(t *testing.T, path string) *Server {
	t.Helper()
	db, _ := openServerDatabase(newStartupClock(), path)
	s := &Server{DB: db, Path: path, Mode: ModeRW, BucketNames: BucketNames{
		Docs: []byte("docs"), IdxMeta: []byte("idxmeta"), Rev: []byte("rev"), ByKey: []byte("bykey"),
	}}
	t.Cleanup(func() {
		s.closeCollectionFiles()
		_ = s.DB.Close()
	})
	if err := s.ensureBuckets(); err != nil {
		t.Fatal(err)
	}
	return s
}

func assertFixtureLookups(t *testing.T, s *Server) {
	t.Helper()
	client := &DirectClient{server: s}
	for _, l := range fixtureLookups {
		doc, err := client.Get(context.Background(), &MCPGetRequest{Collection: l.collection, Key: l.key, Lang: l.lang})
		if err != nil {
			t.Errorf("%s/%s/%s written by 2.18.8 is not found: %v", l.collection, l.key, l.lang, err)
			continue
		}
		if !strings.Contains(doc.ContentMD, l.content) {
			t.Errorf("%s/%s/%s = %q, want content containing %q", l.collection, l.key, l.lang, doc.ContentMD, l.content)
		}
	}
}

// byKeyEntries returns the key-index entries of a collection.
func byKeyEntries(t *testing.T, s *Server, collection string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		prefix := storage.ByKeyCollectionPrefix(collection)
		c := tx.Bucket([]byte("bykey")).Cursor()
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, v = c.Next() {
			out[string(k)] = string(v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertFixtureIndexNormalised(t *testing.T, s *Server) {
	t.Helper()
	want := map[string]string{
		"bykey|docs|readme.md|en":      "docs|readme.md|en",
		"bykey|docs|guide/intro.md|en": "docs|guide/intro.md|en",
		"bykey|docs|plain.md|en":       "docs|plain.md|en",
		// Left by 2.18.8's delete; normalised, not invented or lost.
		"bykey|docs|gone.md|en": "docs|gone.md|en",
	}
	got := byKeyEntries(t, s, "docs")
	if len(got) != len(want) {
		t.Errorf("docs index = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("docs index[%q] = %q, want %q", k, got[k], v)
		}
	}
	if got := byKeyEntries(t, s, "Notes"); len(got) != 1 || got["bykey|Notes|todo.md|pl"] != "notes|todo.md|pl" {
		t.Errorf("Notes index = %v", got)
	}
}

func TestStartupMigratesASingleFileDatabaseFrom2188(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s := serverOnFixture(t, unpackFixture(t, "v2.18.8"))

	if v, err := migrate.Version(s.DB); err != nil || v != migrate.Latest() {
		t.Fatalf("schema version after startup = %d (%v), want %d", v, err, migrate.Latest())
	}
	assertFixtureLookups(t, s)
	assertFixtureIndexNormalised(t, s)
}

func TestStartupMigratesEveryCollectionFileFrom2188(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	s := serverOnFixture(t, unpackPerCollectionFixture(t))

	cf := s.collectionStore()
	if cf == nil {
		t.Fatal("the collection files did not open")
	}
	for _, c := range []string{"docs", "Notes"} {
		if v, err := migrate.Version(cf.get(c)); err != nil || v != migrate.Latest() {
			t.Fatalf("collection %s at schema version %d (%v), want %d", c, v, err, migrate.Latest())
		}
	}
	assertFixtureLookups(t, s)
	assertFixtureIndexNormalised(t, s)
}

// A backup taken by 2.18.8 restores into this build and is migrated before
// it serves, in either layout.
func TestRestoringA2188BackupMigratesIt(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()

	if err := s.restoreFromBackup(fixtureForLayout(t)); err != nil {
		t.Fatalf("restore: %v", err)
	}
	assertFixtureLookups(t, s)
	assertFixtureIndexNormalised(t, s)
}
