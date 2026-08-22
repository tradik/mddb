package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
	proto "mddb/proto"
)

// GO-021. `storageBackend: "memory"` and `"s3"` were accepted, validated,
// documented — and ignored. Every document went to BoltDB while the operator
// believed otherwise. These pin that the setting now decides where a payload
// actually lands.

func backendServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)
	srv.CollectionManager = NewCollectionManager(srv.DB)
	if err := srv.CollectionManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	srv.InitStorageBackends()
	return srv, cleanup
}

// configureBackend puts a collection on a non-default backend the way the API
// does.
func configureBackend(t *testing.T, srv *Server, collection, backend string) {
	t.Helper()
	cfg := &CollectionConfig{Type: "default", StorageBackend: backend}
	if err := srv.ApplyStorageBackend(collection, cfg); err != nil {
		t.Fatalf("configuring %s for %s: %v", backend, collection, err)
	}
	if err := srv.CollectionManager.Set(collection, cfg); err != nil {
		t.Fatal(err)
	}
}

// docBytesInBolt reports what the local docs bucket holds, which is how the
// original bug was invisible: everything was there regardless of configuration.
func docBytesInBolt(t *testing.T, srv *Server, collection, docID string) []byte {
	t.Helper()
	var out []byte
	if err := srv.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket(srv.BucketNames.Docs)
		if b == nil {
			return nil
		}
		if v := b.Get(storage.DocKey(collection, docID)); v != nil {
			out = append([]byte(nil), v...)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDefaultCollectionsStayOnBolt(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()

	if srv.usesExternalBackend("plain") {
		t.Error("an unconfigured collection was routed to an external backend")
	}
	if b := srv.Backends.Get("plain"); b == nil || b.Name() != "boltdb" {
		t.Errorf("the default backend is %v, want boltdb", b)
	}
}

// The claim the API was making and not keeping: a configured backend holds the
// document.
func TestConfiguredBackendReceivesThePayload(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	configureBackend(t, srv, "ephemeral", "memory")

	if !srv.usesExternalBackend("ephemeral") {
		t.Fatal("the collection was not routed to its configured backend")
	}

	saved, _, err := srv.addDocument("ephemeral", "a", "en",
		map[string][]string{"kind": {"prose"}}, "content in the backend", 0, false)
	if err != nil {
		t.Fatal(err)
	}

	// The payload must be in the backend, not merely in the bucket.
	data, err := srv.Backends.Get("ephemeral").GetDoc("ephemeral", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("the configured backend holds no payload — the setting is still being ignored")
	}

	doc, err := loadDoc(data)
	if err != nil {
		t.Fatalf("the backend's bytes do not decode: %v", err)
	}
	if doc.ContentMD != "content in the backend" {
		t.Errorf("the backend holds different content: %q", doc.ContentMD)
	}
}

// A document written to a backend must read back through the ordinary API,
// byte for byte, or the feature is write-only.
func TestDocumentsOnABackendReadBackIdentically(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	configureBackend(t, srv, "ephemeral", "memory")

	const content = "# Title\n\nBody with **markup** and a ünïcödé line."
	if _, _, err := srv.addDocument("ephemeral", "a", "en",
		map[string][]string{"tag": {"one", "two"}}, content, 0, false); err != nil {
		t.Fatal(err)
	}

	got, err := srv.loadDocByRef("ephemeral", "a", "en")
	if err != nil {
		t.Fatalf("a document on an external backend cannot be read back: %v", err)
	}
	if got.ContentMD != content {
		t.Errorf("content changed through the backend:\n want %q\n got  %q", content, got.ContentMD)
	}
	if tags := got.Meta["tag"]; len(tags) != 2 {
		t.Errorf("metadata was lost: %v", got.Meta)
	}
}

// Deleting must take the payload with it, or the data the caller believes is
// gone is still in object storage.
func TestDeleteRemovesThePayloadFromTheBackend(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	configureBackend(t, srv, "ephemeral", "memory")

	saved, _, err := srv.addDocument("ephemeral", "a", "en", nil, "content", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.deleteDocumentInternal("ephemeral", "a", "en"); err != nil {
		t.Fatal(err)
	}

	data, err := srv.Backends.Get("ephemeral").GetDoc("ephemeral", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 {
		t.Error("the document was deleted but its payload is still in the backend")
	}
}

// A batch must reach the backend too — it is the path a bulk import uses, and
// the one where losing the setting would matter most.
func TestBatchWritesReachTheBackend(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	configureBackend(t, srv, "ephemeral", "memory")

	docs := []*proto.BatchDocument{
		makeBatchDoc("a", "en", "first", map[string]*proto.MetaValues{"kind": {Values: []string{"prose"}}}, false),
		makeBatchDoc("b", "en", "second", map[string]*proto.MetaValues{"kind": {Values: []string{"prose"}}}, false),
	}
	resp, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "ephemeral", docs)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failed > 0 {
		t.Fatalf("batch failed: %v", resp.Errors)
	}

	stored := 0
	if err := srv.Backends.Get("ephemeral").ListDocs("ephemeral", func(string, []byte) error {
		stored++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stored != 2 {
		t.Errorf("the backend holds %d of 2 batch documents", stored)
	}
}

// Switching a collection back to the default must stop routing, so an operator
// can undo the choice.
func TestSwitchingBackToBoltStopsRouting(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	configureBackend(t, srv, "ephemeral", "memory")

	if !srv.usesExternalBackend("ephemeral") {
		t.Fatal("setup failed")
	}
	configureBackend(t, srv, "ephemeral", "boltdb")
	if srv.usesExternalBackend("ephemeral") {
		t.Error("switching back to boltdb left the external backend in place")
	}
}

// The heart of the original bug: a backend that cannot be created must not
// silently fall through to local disk.
func TestAnUnavailableBackendRefusesWritesInsteadOfFallingBack(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()

	srv.Backends.MarkFailed("broken", errors.New("connection refused"))

	err := srv.checkBackendAvailable("broken")
	if err == nil {
		t.Fatal("a collection with an unavailable backend accepted a write — it would land on local disk")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("the error does not say why: %v", err)
	}
}

// An S3 configuration that cannot connect must be refused at configuration
// time, not accepted and ignored.
func TestConfiguringAnUnreachableS3BackendIsRefused(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()

	body := `{"collection":"docs","storageBackend":"s3","storageConfig":{"endpoint":"127.0.0.1:1","bucket":"nope"}}`
	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleCollectionConfigSet(rec, req)

	if rec.Code == http.StatusOK {
		t.Errorf("an unreachable S3 backend was accepted with 200 — the exact behaviour this change removes")
	}
}

// S3 still requires its credentials, and the API must say so rather than
// creating a backend that fails on first use.
func TestS3RequiresItsConfiguration(t *testing.T) {
	if _, err := CreateBackend("s3", nil); err == nil {
		t.Error("an S3 backend was created without any configuration")
	}
	if _, err := CreateBackend("s3", &StorageConfigDef{Endpoint: "x"}); err == nil {
		t.Error("an S3 backend was created without a bucket")
	}
	if _, err := CreateBackend("boltdb", nil); err == nil {
		t.Error("boltdb was created through the factory; it is the implicit fallback")
	}
	if _, err := CreateBackend("telepathy", nil); err == nil {
		t.Error("an unknown backend type was accepted")
	}
}

// The BoltDB backend must behave exactly like the buckets it wraps, or the
// registry becomes a second storage format.
func TestBoltBackendMatchesTheBuckets(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()

	b := NewBoltBackend(srv.DB, srv.BucketNames)

	if err := b.PutDoc("docs", "id-1", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	// Written through the backend, visible through the buckets.
	if got := docBytesInBolt(t, srv, "docs", "id-1"); string(got) != "payload" {
		t.Errorf("the bucket holds %q, want payload", got)
	}

	got, err := b.GetDoc("docs", "id-1")
	if err != nil || string(got) != "payload" {
		t.Errorf("GetDoc returned %q, %v", got, err)
	}

	// Absent means nil, nil — not an error, per the interface contract.
	missing, err := b.GetDoc("docs", "never-written")
	if err != nil || missing != nil {
		t.Errorf("a missing document gave %q, %v; want nil, nil", missing, err)
	}

	if err := b.PutByKey("docs", "k", "en", "id-1"); err != nil {
		t.Fatal(err)
	}
	if id, err := b.GetByKey("docs", "k", "en"); err != nil || id != "id-1" {
		t.Errorf("GetByKey = %q, %v", id, err)
	}

	if err := b.DeleteDoc("docs", "id-1"); err != nil {
		t.Fatal(err)
	}
	if got := docBytesInBolt(t, srv, "docs", "id-1"); got != nil {
		t.Errorf("the document survived deletion: %q", got)
	}

	// Closing must not take the server's database with it.
	if err := b.Close(); err != nil {
		t.Errorf("closing the default backend errored: %v", err)
	}
	if err := srv.DBView(func(*bolt.Tx) error { return nil }); err != nil {
		t.Errorf("closing the default backend closed the server's database: %v", err)
	}
}
