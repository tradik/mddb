package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// Upgrade compatibility (TEST-003).
//
// Every other test here writes with the current code and reads with the current
// code, which proves the format is self-consistent — precisely the property
// that stays true while compatibility quietly breaks.
//
// These open a database file an *older release* wrote and check it is still
// readable. The failure they exist to prevent is the worst one a database can
// hand a user: a file the new version will not open, with the old binary gone.
//
// Fixtures live in test/upgrade-fixtures; adding one is documented there.

// upgradeFixtures lists the releases whose files must still open, oldest first.
var upgradeFixtures = []struct {
	version    string
	collection string
	documents  int
}{
	{version: "v2.11.4", collection: "upgrade-fixture", documents: 4},
}

func TestOpensFixturesFromOlderReleases(t *testing.T) {
	for _, fx := range upgradeFixtures {
		t.Run(fx.version, func(t *testing.T) {
			path := unpackFixture(t, fx.version)

			db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
			if err != nil {
				t.Fatalf("a database written by %s no longer opens: %v", fx.version, err)
			}
			defer func() { _ = db.Close() }()

			docs := readFixtureDocs(t, db, fx.collection)
			if len(docs) != fx.documents {
				t.Fatalf("%s wrote %d documents, this build reads %d", fx.version, fx.documents, len(docs))
			}

			// Reading the bytes is not enough: they must decode into
			// documents whose content and metadata survived.
			for _, doc := range docs {
				if doc.Key == "" {
					t.Errorf("a document from %s decoded without a key: %+v", fx.version, doc)
				}
				if doc.ContentMD == "" {
					t.Errorf("%s: document %q decoded with empty content", fx.version, doc.Key)
				}
				if len(doc.Meta) == 0 {
					t.Errorf("%s: document %q lost its metadata", fx.version, doc.Key)
				}
			}

			// The metadata index must still point at documents that exist —
			// an index the new build cannot follow is a search that silently
			// returns nothing.
			assertFixtureIndexIntact(t, db, fx.collection, docs)
		})
	}
}

// The fixture's own content, spelled out, so a change that quietly rewrites
// documents on read is caught rather than averaged away by a count.
func TestFixtureContentIsUnchanged(t *testing.T) {
	path := unpackFixture(t, "v2.11.4")

	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	byKey := map[string]*storage.Doc{}
	for _, doc := range readFixtureDocs(t, db, "upgrade-fixture") {
		byKey[doc.Key] = doc
	}

	prose, ok := byKey["doc-1"]
	if !ok {
		t.Fatalf("doc-1 is missing; got keys %v", keysOf(byKey))
	}
	if !strings.Contains(prose.ContentMD, "quick brown fox") {
		t.Errorf("doc-1 content changed: %q", prose.ContentMD)
	}
	if got := prose.Meta["tag"]; len(got) != 2 || got[0] != "fixture" {
		t.Errorf("doc-1 metadata changed: %v", prose.Meta)
	}

	// A code document, because CODE-001 added meaning to `kind` after this
	// fixture was written: an older file must not acquire behaviour it never
	// had, nor lose the convention it did.
	css, ok := byKey["theme/style.css"]
	if !ok {
		t.Fatalf("theme/style.css is missing; got keys %v", keysOf(byKey))
	}
	if got := css.Meta["kind"]; len(got) != 1 || got[0] != "code" {
		t.Errorf("the code document lost its kind: %v", css.Meta)
	}
	// Symbols are extracted on write (CODE-004). This file predates that, so
	// it must have none — and reading it must not invent any.
	if _, found := css.Meta[MetaKeyDefines]; found {
		t.Errorf("a pre-CODE-004 document came back with symbols: %v", css.Meta)
	}
}

func unpackFixture(t *testing.T, version string) string {
	t.Helper()

	src := filepath.Join("..", "..", "test", "upgrade-fixtures", version+".db.gz")
	f, err := os.Open(src) // #nosec G304 -- fixture path built from a fixed table
	if err != nil {
		t.Fatalf("fixture for %s is missing: %v", version, err)
	}
	defer func() { _ = f.Close() }()

	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("fixture for %s is not readable: %v", version, err)
	}
	defer func() { _ = zr.Close() }()

	// Copied to a temp file: the test must not modify the fixture, and
	// bolt.Open takes a write lock.
	dst := filepath.Join(t.TempDir(), version+".db")
	out, err := os.Create(dst) // #nosec G304 -- inside t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, zr); err != nil { // #nosec G110 -- fixture is committed, not untrusted input
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

func readFixtureDocs(t *testing.T, db *bolt.DB, collection string) []*storage.Doc {
	t.Helper()

	var docs []*storage.Doc
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			return fmt.Errorf("the docs bucket is gone")
		}
		prefix := []byte("doc|" + collection + "|")
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, v = c.Next() {
			doc, err := loadDoc(v)
			if err != nil {
				return fmt.Errorf("document %q written by an older release no longer decodes: %w", k, err)
			}
			docs = append(docs, doc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func assertFixtureIndexIntact(t *testing.T, db *bolt.DB, collection string, docs []*storage.Doc) {
	t.Helper()

	live := map[string]bool{}
	for _, d := range docs {
		live[d.ID] = true
	}

	err := db.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx == nil {
			return fmt.Errorf("the metadata index bucket is gone")
		}
		entries := 0
		prefix := []byte("meta|" + collection + "|")
		c := bIdx.Cursor()
		for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
			entries++
		}
		if entries == 0 {
			return fmt.Errorf("the fixture's metadata index is empty — this build cannot follow it")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func keysOf(m map[string]*storage.Doc) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
