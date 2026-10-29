package main

import (
	"net/http"
	"strings"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// A stored value that does not decode is a storage error, not a missing
// document: the handler answers 400 and leaves the bytes as they were.
func TestCovJSetTTLOnUndecodableDocument(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	dk := storage.DocKey("c", genID("c", "k", "en"))
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(dk, []byte("{broken"))
	}); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, s.handleSetTTL, SetTTLRequest{Collection: "c", Key: "k", Lang: "en", TTL: 60})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("undecodable doc: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("a corrupt document was reported as missing: %s", rec.Body.String())
	}
	var raw string
	_ = s.DBViewC("c", func(tx *bolt.Tx) error {
		raw = string(tx.Bucket([]byte("docs")).Get(dk))
		return nil
	})
	if raw != "{broken" {
		t.Errorf("stored bytes changed: %q", raw)
	}
}

func TestCovJSetTTLMissingDocumentIs404(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleSetTTL, SetTTLRequest{Collection: "c", Key: "absent", Lang: "en", TTL: 60})
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing document: %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}
