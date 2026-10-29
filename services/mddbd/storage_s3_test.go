package main

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// The S3 backend against fakeS3 (backup_fake_s3_test.go), since MinIO is not
// available where the suite runs.

// newTestS3Backend is a backend on bucket "data" of f, under prefix.
func newTestS3Backend(t *testing.T, f *fakeS3, prefix string) *S3Backend {
	t.Helper()
	be, err := NewS3Backend(&StorageConfigDef{Endpoint: f.endpoint(), Bucket: "data", Region: "us-east-1",
		AccessKey: "test", SecretKey: "test-secret", Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	return be
}

// #360: the key and language of a key-index entry are normalised as in bbolt,
// so README.md is found as readme.md and two spellings leave one object; the
// collection keeps its case.
func TestS3KeyIndexIsNormalised(t *testing.T) {
	f := newFakeS3(t)
	be := newTestS3Backend(t, f, "mddb")

	if err := be.PutByKey("docs", "README.md", "EN", "id-1"); err != nil {
		t.Fatal(err)
	}
	if err := be.PutByKey("docs", "readme.md", "en", "id-1"); err != nil {
		t.Fatal(err)
	}
	if got := f.keys("data"); !reflect.DeepEqual(got, []string{"mddb/bykey/docs/readme.md/en"}) {
		t.Fatalf("objects after two spellings: %v", got)
	}
	for _, spelling := range [][2]string{{"readme.md", "en"}, {"README.md", "EN"}, {"ReadMe.MD", "En"}} {
		if id, err := be.GetByKey("docs", spelling[0], spelling[1]); err != nil || id != "id-1" {
			t.Errorf("GetByKey(%q, %q) = %q, %v", spelling[0], spelling[1], id, err)
		}
	}
	if id, err := be.GetByKey("Docs", "readme.md", "en"); err != nil || id != "" {
		t.Errorf("collections are case-sensitive, got %q, %v", id, err)
	}
	if err := be.DeleteByKey("docs", "README.MD", "EN"); err != nil {
		t.Fatal(err)
	}
	if got := f.keys("data"); len(got) != 0 {
		t.Errorf("a delete by another spelling left %v", got)
	}
}

// The server keeps the key index of an S3-backed collection in bbolt and
// moves only document bodies (GO-021): writing README.md leaves no key-index
// object, which is why #360 needs no migration of the bucket, and the
// document is found by any spelling of its key.
func TestS3CollectionKeepsItsKeyIndexInBolt(t *testing.T) {
	srv, cleanup := backendServer(t)
	defer cleanup()
	f := newFakeS3(t)
	cfg := &CollectionConfig{Type: "default", StorageBackend: "s3", StorageConfig: &StorageConfigDef{
		Endpoint: f.endpoint(), Bucket: "data", Region: "us-east-1", AccessKey: "test", SecretKey: "test-secret"}}
	if err := srv.ApplyStorageBackend("archive", cfg); err != nil {
		t.Fatal(err)
	}
	if err := srv.CollectionManager.Set("archive", cfg); err != nil {
		t.Fatal(err)
	}

	if _, _, err := srv.addDocument("archive", "README.md", "EN", nil, "body", 0, false); err != nil {
		t.Fatal(err)
	}
	doc, err := srv.loadDocByRef("archive", "readme.md", "en")
	if err != nil || doc.ContentMD != "body" {
		t.Fatalf("README.md not found as readme.md: %v, %v", doc, err)
	}
	keys := f.keys("data")
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "docs/archive/") {
		t.Errorf("objects in the bucket: %v, want one document body and no key-index object", keys)
	}
}

func TestS3BackendDocuments(t *testing.T) {
	f := newFakeS3(t)
	be := newTestS3Backend(t, f, "p/")
	if be.Name() != "s3" || be.Close() != nil {
		t.Fatal("name or close")
	}

	if err := be.PutDoc("c", "a", []byte("A")); err != nil {
		t.Fatal(err)
	}
	if err := be.PutDoc("c", "empty", nil); err != nil {
		t.Fatal(err)
	}
	if data, err := be.GetDoc("c", "a"); err != nil || string(data) != "A" {
		t.Errorf("GetDoc = %q, %v", data, err)
	}
	for _, id := range []string{"missing", "empty"} {
		if data, err := be.GetDoc("c", id); err != nil || data != nil {
			t.Errorf("GetDoc(%s) = %q, %v; want nil, nil", id, data, err)
		}
	}

	// An object named as the listing prefix itself has no id and is skipped,
	// as is an empty one.
	f.mu.Lock()
	f.buckets["data"]["p/docs/c/"] = []byte("x")
	f.mu.Unlock()
	var seen []string
	if err := be.ListDocs("c", func(id string, data []byte) error {
		seen = append(seen, id+"="+string(data))
		return nil
	}); err != nil || !reflect.DeepEqual(seen, []string{"a=A"}) {
		t.Errorf("ListDocs saw %v, %v", seen, err)
	}
	stop := errors.New("stop")
	if err := be.ListDocs("c", func(string, []byte) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("ListDocs did not return the callback's error: %v", err)
	}

	if err := be.DeleteDoc("c", "a"); err != nil {
		t.Fatal(err)
	}
	if data, _ := be.GetDoc("c", "a"); data != nil {
		t.Error("the document survived its delete")
	}
}

func TestS3BackendReportsFailures(t *testing.T) {
	f := newFakeS3(t)
	be := newTestS3Backend(t, f, "")
	if err := be.PutDoc("c", "a", []byte("A")); err != nil {
		t.Fatal(err)
	}

	f.failNext(http.MethodGet, http.StatusForbidden)
	if _, err := be.GetDoc("c", "a"); err == nil {
		t.Error("GetDoc hid a failed read")
	}
	if _, err := be.GetByKey("c", "k", "en"); err == nil {
		t.Error("GetByKey hid a failed read")
	}
	if err := be.ListDocs("c", func(string, []byte) error { return nil }); err == nil {
		t.Error("ListDocs hid a failed read of a document")
	}
	f.failNext("LIST", http.StatusForbidden)
	if err := be.ListDocs("c", func(string, []byte) error { return nil }); err == nil {
		t.Error("ListDocs hid a failed listing")
	}

	// A name the client refuses before any request.
	bad := &S3Backend{client: be.client}
	if _, err := bad.GetDoc("c", "a"); err == nil {
		t.Error("GetDoc accepted an empty bucket name")
	}
	if _, err := bad.GetByKey("c", "k", "en"); err == nil {
		t.Error("GetByKey accepted an empty bucket name")
	}
}

func TestNewS3BackendConfiguration(t *testing.T) {
	if _, err := NewS3Backend(&StorageConfigDef{Bucket: "b"}); err == nil {
		t.Error("an S3 backend was created without an endpoint")
	}
	if _, err := NewS3Backend(&StorageConfigDef{Endpoint: "http://host/path", Bucket: "b"}); err == nil {
		t.Error("an S3 backend was created with an endpoint the client refuses")
	}

	f := newFakeS3(t)
	be := newTestS3Backend(t, f, "no-slash")
	if be.prefix != "no-slash/" {
		t.Errorf("prefix %q, want a trailing slash", be.prefix)
	}
	if f.keys("data") != nil || f.object("data", "x") != nil {
		t.Error("a new bucket is not empty")
	}
	// The bucket now exists: a second backend uses it as it is.
	newTestS3Backend(t, f, "")

	failing := newFakeS3(t)
	failing.failNext(http.MethodHead, http.StatusForbidden)
	if _, err := NewS3Backend(&StorageConfigDef{Endpoint: failing.endpoint(), Bucket: "data", Region: "us-east-1"}); err == nil {
		t.Error("a failed bucket check was ignored")
	}
	refusing := newFakeS3(t)
	refusing.failNext(http.MethodPut, http.StatusForbidden)
	if _, err := NewS3Backend(&StorageConfigDef{Endpoint: refusing.endpoint(), Bucket: "data", Region: "us-east-1"}); err == nil {
		t.Error("a failed bucket creation was ignored")
	}
}

// The memory backend keys its index as bbolt does (#360).
func TestMemoryKeyIndexIsNormalised(t *testing.T) {
	m := NewMemoryBackend()
	if err := m.PutByKey("docs", "README.md", "EN", "id-1"); err != nil {
		t.Fatal(err)
	}
	if id, _ := m.GetByKey("docs", "readme.md", "en"); id != "id-1" {
		t.Errorf("README.md not found as readme.md: %q", id)
	}
	if id, _ := m.GetByKey("Docs", "readme.md", "en"); id != "" {
		t.Errorf("collections are case-sensitive, got %q", id)
	}
	if err := m.DeleteByKey("docs", "ReadMe.md", "en"); err != nil {
		t.Fatal(err)
	}
	if len(m.byKey) != 0 {
		t.Errorf("a delete by another spelling left %v", m.byKey)
	}
}

// The memory backend's edges: a collection it never held, and a listing the
// callback stops.
func TestMemoryBackendEdges(t *testing.T) {
	m := NewMemoryBackend()
	if err := m.DeleteDoc("none", "a"); err != nil {
		t.Errorf("deleting from a collection never held: %v", err)
	}
	if err := m.ListDocs("none", func(string, []byte) error { return errors.New("called") }); err != nil {
		t.Errorf("listing a collection never held: %v", err)
	}
	if err := m.PutDoc("c", "a", []byte("A")); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	if err := m.ListDocs("c", func(string, []byte) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("ListDocs did not return the callback's error: %v", err)
	}
}
