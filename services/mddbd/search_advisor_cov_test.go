package main

import (
	"fmt"
	"strings"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covGPutManyDocs writes n documents in one transaction.
func covGPutManyDocs(t *testing.T, s *Server, collection string, n int, content string) {
	t.Helper()
	if err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("d%05d", i)
			data, err := marshalDoc(&storage.Doc{ID: id, Key: id, Lang: "en", ContentMD: content})
			if err != nil {
				return err
			}
			if err := b.Put(storage.DocKey(collection, id), data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCovGProfileSamplesAtMostTheLimit(t *testing.T) {
	s := covGServer(t)
	covGPutManyDocs(t, s, "big", advisorSampleLimit+5, "same few words")
	covGPutRaw(t, s, "big", "corrupt", []byte{0xff, 0xfe})

	p, err := s.profileCollection("big")
	if err != nil {
		t.Fatal(err)
	}
	if p.Documents != advisorSampleLimit+6 || p.Sampled != advisorSampleLimit {
		t.Errorf("documents %d sampled %d", p.Documents, p.Sampled)
	}
}

func TestCovGProfileSkipsCorruptAndCountsLongDocuments(t *testing.T) {
	s := covGServer(t)
	covGPutRaw(t, s, "c", "bad", []byte{0xff, 0xfe})
	long := strings.TrimSpace(strings.Repeat("word ", longDocumentWords+1))
	covGPutDoc(t, s, "c", &storage.Doc{ID: "long", Key: "long", Lang: "en", ContentMD: long})
	covGPutDoc(t, s, "c", &storage.Doc{ID: "short", Key: "short", Lang: "en", ContentMD: "brief"})

	p, err := s.profileCollection("c")
	if err != nil {
		t.Fatal(err)
	}
	if p.Documents != 3 || p.Sampled != 2 || p.LongDocumentRatio != 0.5 {
		t.Errorf("profile = %+v", p)
	}
}

func TestCovGProfileVectors(t *testing.T) {
	s := covGVectorServer(t)
	covGEmbedded(t, s, "v", "a", "embedded text", nil)

	rec, err := s.RecommendSearch("v")
	if err != nil {
		t.Fatal(err)
	}
	p := rec.Profile
	if p.EmbeddedDocuments == 0 || p.VectorDimensions != 3 || p.EstimatedVectorBytes != int64(p.EmbeddedDocuments)*3*4 {
		t.Errorf("vector profile = %+v", p)
	}
	if rec.SearchType != "hybrid" {
		t.Errorf("a fully embedded collection should get hybrid, got %q", rec.SearchType)
	}

	// Vectors present but no provider: dimensions unknown, size not guessed.
	s.Embedding = nil
	p2, err := s.profileCollection("v")
	if err != nil {
		t.Fatal(err)
	}
	if p2.EmbeddedDocuments == 0 || p2.VectorDimensions != 0 || p2.EstimatedVectorBytes != 0 {
		t.Errorf("no provider: %+v", p2)
	}
}

func TestCovGRecommendSearchStoreFailures(t *testing.T) {
	s := covGServer(t)
	if err := s.DBUpdateC("x", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) }); err != nil {
		t.Fatal(err)
	}
	rec, err := s.RecommendSearch("x")
	if err != nil || rec.Profile.Documents != 0 || rec.SearchType != "fts" {
		t.Errorf("no docs bucket reads as empty: %+v %v", rec, err)
	}

	closeStorage(t, s, "x")
	if _, err := s.RecommendSearch("x"); err == nil || !strings.Contains(err.Error(), "reading x") {
		t.Errorf("a store failure must be returned with context, got %v", err)
	}
}
