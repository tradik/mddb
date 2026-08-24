package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
	proto "mddb/proto"
)

// TEST-002. The direct client is what every MCP tool call goes through, and
// the batch paths — the ones an agent uses to change many documents at once —
// were untested. A batch that reports success while doing nothing is worse
// than one that fails.

func directClientServer(t *testing.T) (*DirectClient, *Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)
	srv.CollectionManager = NewCollectionManager(srv.DB)
	if err := srv.CollectionManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	return NewDirectClient(srv), srv, cleanup
}

func seedDocs(t *testing.T, srv *Server, collection string, keys ...string) {
	t.Helper()
	docs := make([]*proto.BatchDocument, 0, len(keys))
	for _, k := range keys {
		docs = append(docs, makeBatchDoc(k, "en", "original content of "+k,
			map[string]*proto.MetaValues{"kind": {Values: []string{"prose"}}}, false))
	}
	resp, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), collection, docs)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failed > 0 {
		t.Fatalf("seeding failed: %v", resp.Errors)
	}
}

func TestDirectClientHealthReportsMode(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	got, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "healthy" {
		t.Errorf("status = %q, want healthy", got.Status)
	}
	// The mode is what tells an agent whether writes will be accepted.
	if got.Mode != string(srv.Mode) {
		t.Errorf("mode = %q, want %q", got.Mode, srv.Mode)
	}
}

func TestUpdateBatchChangesContentAndReportsCounts(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a", "b")

	got, err := client.UpdateBatch(context.Background(), &MCPUpdateBatchRequest{
		Collection: "docs",
		Documents: []MCPUpdateDocument{
			{Key: "a", Lang: "en", ContentMD: "updated a"},
			{Key: "b", Lang: "en", ContentMD: "updated b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Updated != 2 {
		t.Errorf("updated = %d, want 2 (errors: %v)", got.Updated, got.Errors)
	}

	// The count is not the claim that matters — the content is.
	for _, key := range []string{"a", "b"} {
		doc, err := srv.loadDocByRef("docs", key, "en")
		if err != nil {
			t.Fatalf("%s vanished: %v", key, err)
		}
		if doc.ContentMD != "updated "+key {
			t.Errorf("%s still reads %q", key, doc.ContentMD)
		}
	}
}

// A batch naming documents that do not exist must say so rather than report
// them updated — an agent retrying on a count would loop forever.
func TestUpdateBatchReportsMissingDocuments(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a")

	got, err := client.UpdateBatch(context.Background(), &MCPUpdateBatchRequest{
		Collection: "docs",
		Documents: []MCPUpdateDocument{
			{Key: "a", Lang: "en", ContentMD: "updated"},
			{Key: "never-existed", Lang: "en", ContentMD: "x"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Updated != 1 {
		t.Errorf("updated = %d, want 1", got.Updated)
	}
	if got.NotFound != 1 {
		t.Errorf("notFound = %d, want 1 — a missing document must not count as updated", got.NotFound)
	}
}

// Updating only meta must not blank the content: partial update means partial.
func TestUpdateBatchWithOnlyMetaKeepsContent(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a")

	if _, err := client.UpdateBatch(context.Background(), &MCPUpdateBatchRequest{
		Collection: "docs",
		Documents: []MCPUpdateDocument{
			{Key: "a", Lang: "en", Meta: map[string][]string{"tag": {"new"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := srv.loadDocByRef("docs", "a", "en")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContentMD != "original content of a" {
		t.Errorf("a metadata-only update erased the content: %q", doc.ContentMD)
	}
	if tags := doc.Meta["tag"]; len(tags) != 1 || tags[0] != "new" {
		t.Errorf("the metadata was not applied: %v", doc.Meta)
	}
}

func TestDeleteBatchRemovesDocumentsAndReportsCounts(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a", "b", "c")

	got, err := client.DeleteBatch(context.Background(), &MCPDeleteBatchRequest{
		Collection: "docs",
		Documents: []MCPDeleteDocument{
			{Key: "a", Lang: "en"},
			{Key: "b", Lang: "en"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Deleted != 2 {
		t.Errorf("deleted = %d, want 2 (errors: %v)", got.Deleted, got.Errors)
	}

	for _, key := range []string{"a", "b"} {
		if _, err := srv.loadDocByRef("docs", key, "en"); err == nil {
			t.Errorf("%s was reported deleted but still reads back", key)
		}
	}
	// And the one not named must survive — a delete that takes neighbours
	// with it is the worst kind of success.
	if _, err := srv.loadDocByRef("docs", "c", "en"); err != nil {
		t.Errorf("c was not in the batch but is gone: %v", err)
	}
}

func TestDeleteBatchReportsMissingDocuments(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a")

	got, err := client.DeleteBatch(context.Background(), &MCPDeleteBatchRequest{
		Collection: "docs",
		Documents: []MCPDeleteDocument{
			{Key: "a", Lang: "en"},
			{Key: "never-existed", Lang: "en"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Deleted != 1 || got.NotFound != 1 {
		t.Errorf("deleted=%d notFound=%d, want 1 and 1", got.Deleted, got.NotFound)
	}
}

func TestBatchOperationsOnAnEmptyList(t *testing.T) {
	client, _, cleanup := directClientServer(t)
	defer cleanup()

	up, err := client.UpdateBatch(context.Background(), &MCPUpdateBatchRequest{Collection: "docs"})
	if err != nil {
		t.Errorf("an empty update batch failed: %v", err)
	} else if up.Updated != 0 {
		t.Errorf("an empty batch reported %d updates", up.Updated)
	}

	del, err := client.DeleteBatch(context.Background(), &MCPDeleteBatchRequest{Collection: "docs"})
	if err != nil {
		t.Errorf("an empty delete batch failed: %v", err)
	} else if del.Deleted != 0 {
		t.Errorf("an empty batch reported %d deletions", del.Deleted)
	}
}

// The mirror of the metadata-only case: updating content must not erase the
// metadata. Both directions of this were data loss (TEST-002).
func TestUpdateBatchWithOnlyContentKeepsMetadata(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a")

	if _, err := client.UpdateBatch(context.Background(), &MCPUpdateBatchRequest{
		Collection: "docs",
		Documents: []MCPUpdateDocument{
			{Key: "a", Lang: "en", ContentMD: "rewritten"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := srv.loadDocByRef("docs", "a", "en")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContentMD != "rewritten" {
		t.Errorf("content = %q, want rewritten", doc.ContentMD)
	}
	if kind := doc.Meta["kind"]; len(kind) != 1 || kind[0] != "prose" {
		t.Errorf("a content-only update erased the metadata: %v", doc.Meta)
	}
}

// Sending both replaces both — the ordinary case must still work.
func TestUpdateBatchWithBothReplacesBoth(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a")

	if _, err := client.UpdateBatch(context.Background(), &MCPUpdateBatchRequest{
		Collection: "docs",
		Documents: []MCPUpdateDocument{
			{Key: "a", Lang: "en", ContentMD: "new body",
				Meta: map[string][]string{"kind": {"code"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := srv.loadDocByRef("docs", "a", "en")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContentMD != "new body" {
		t.Errorf("content = %q", doc.ContentMD)
	}
	if kind := doc.Meta["kind"]; len(kind) != 1 || kind[0] != "code" {
		t.Errorf("meta = %v, want kind=code", doc.Meta)
	}
}

// An update naming neither field changes nothing rather than emptying the
// document.
func TestUpdateBatchWithNeitherFieldIsInert(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a")

	if _, err := client.UpdateBatch(context.Background(), &MCPUpdateBatchRequest{
		Collection: "docs",
		Documents:  []MCPUpdateDocument{{Key: "a", Lang: "en"}},
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := srv.loadDocByRef("docs", "a", "en")
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContentMD != "original content of a" {
		t.Errorf("an empty update erased the content: %q", doc.ContentMD)
	}
	if len(doc.Meta) == 0 {
		t.Error("an empty update erased the metadata")
	}
}

// --- export ---

// Export is how a corpus leaves MDDB. Its filter deciding *which* documents
// leave is the part that matters: exporting too much is a disclosure, too
// little is a silent gap in someone's backup.
func TestExportReturnsTheWholeCollection(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "a", "b", "c")

	rc, err := client.Export(context.Background(), &MCPExportRequest{
		Collection: "docs", Format: "jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, key := range []string{"a", "b", "c"} {
		if !strings.Contains(body, `"key":"`+key+`"`) {
			t.Errorf("export is missing document %q:\n%s", key, body)
		}
	}
}

func TestExportHonoursAMetadataFilter(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	docs := []*proto.BatchDocument{
		makeBatchDoc("public", "en", "public content",
			map[string]*proto.MetaValues{"visibility": {Values: []string{"public"}}}, false),
		makeBatchDoc("secret", "en", "secret content",
			map[string]*proto.MetaValues{"visibility": {Values: []string{"private"}}}, false),
	}
	if _, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "docs", docs); err != nil {
		t.Fatal(err)
	}

	rc, err := client.Export(context.Background(), &MCPExportRequest{
		Collection: "docs",
		FilterMeta: map[string][]string{"visibility": {"public"}},
		Format:     "jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()

	data, _ := io.ReadAll(rc)
	body := string(data)

	if !strings.Contains(body, "public content") {
		t.Errorf("the filtered export dropped a matching document:\n%s", body)
	}
	// The failure that matters: a filter that leaks what it was meant to
	// exclude.
	if strings.Contains(body, "secret content") {
		t.Errorf("the filter let a non-matching document through:\n%s", body)
	}
}

func TestExportOfAnEmptyCollection(t *testing.T) {
	client, _, cleanup := directClientServer(t)
	defer cleanup()

	rc, err := client.Export(context.Background(), &MCPExportRequest{
		Collection: "never-written", Format: "jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(data))) != 0 {
		t.Errorf("an empty collection exported %q", data)
	}
}

// --- truncate ---

// Truncate removes revision history. It must remove history and nothing else:
// a truncate that takes documents with it is unrecoverable.
func TestTruncateRemovesRevisionsButKeepsDocuments(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	// Three writes of one document, each saving a revision.
	for i := range 3 {
		docs := []*proto.BatchDocument{
			makeBatchDoc("a", "en", fmt.Sprintf("version %d", i),
				map[string]*proto.MetaValues{"kind": {Values: []string{"prose"}}}, true),
		}
		if _, err := NewBatchProcessor(srv, 1).ProcessBatch(context.Background(), "docs", docs); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := client.Truncate(context.Background(), &MCPTruncateRequest{
		Collection: "docs", KeepRevs: 0,
	}); err != nil {
		t.Fatal(err)
	}

	// The document itself must survive.
	doc, err := srv.loadDocByRef("docs", "a", "en")
	if err != nil {
		t.Fatalf("truncating revisions removed the document: %v", err)
	}
	if doc.ContentMD != "version 2" {
		t.Errorf("the current version changed: %q", doc.ContentMD)
	}

	// And its history must be gone.
	remaining := countRevisions(t, srv, "docs", doc.ID)
	if remaining != 0 {
		t.Errorf("%d revisions survived a truncate to zero", remaining)
	}
}

func TestTruncateKeepsTheRequestedNumberOfRevisions(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	for i := range 5 {
		docs := []*proto.BatchDocument{
			makeBatchDoc("a", "en", fmt.Sprintf("version %d", i),
				map[string]*proto.MetaValues{"kind": {Values: []string{"prose"}}}, true),
		}
		if _, err := NewBatchProcessor(srv, 1).ProcessBatch(context.Background(), "docs", docs); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := client.Truncate(context.Background(), &MCPTruncateRequest{
		Collection: "docs", KeepRevs: 2,
	}); err != nil {
		t.Fatal(err)
	}

	doc, err := srv.loadDocByRef("docs", "a", "en")
	if err != nil {
		t.Fatal(err)
	}
	if got := countRevisions(t, srv, "docs", doc.ID); got > 2 {
		t.Errorf("keepRevs=2 left %d revisions", got)
	}
}

func countRevisions(t *testing.T, srv *Server, collection, docID string) int {
	t.Helper()
	count := 0
	err := srv.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("rev"))
		if b == nil {
			return nil
		}
		prefix := storage.RevPrefix(collection, docID)
		c := b.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// GO-021: export used to marshal the whole collection into one buffer before
// returning a reader over it, so a large export built the entire result in
// memory — twice the corpus — before the caller saw a byte.
func TestExportStreamsRatherThanBuffering(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	// Enough documents that a buffered implementation would have to finish
	// all of them before the first read returns.
	keys := make([]string, 0, 50)
	for i := range 50 {
		keys = append(keys, fmt.Sprintf("doc-%02d", i))
	}
	seedDocs(t, srv, "docs", keys...)

	rc, err := client.Export(context.Background(), &MCPExportRequest{
		Collection: "docs", Format: "jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()

	// Reading one line must work without draining the rest, which a
	// NopCloser over a finished buffer would also satisfy — what this really
	// pins is that closing early does not deadlock the writer goroutine.
	line := make([]byte, 64)
	if _, err := rc.Read(line); err != nil {
		t.Fatalf("reading the first bytes failed: %v", err)
	}

	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("draining the export failed: %v", err)
	}
	if !strings.Contains(string(line)+string(rest), "doc-49") {
		t.Error("the export is missing its last document")
	}
}

// Closing early must not leave the writer goroutine blocked on a pipe nobody
// reads — that is a goroutine leak per abandoned export.
func TestAbandonedExportDoesNotBlockTheWriter(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	keys := make([]string, 0, 200)
	for i := range 200 {
		keys = append(keys, fmt.Sprintf("doc-%03d", i))
	}
	seedDocs(t, srv, "docs", keys...)

	rc, err := client.Export(context.Background(), &MCPExportRequest{
		Collection: "docs", Format: "jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Read a little, then walk away.
	buf := make([]byte, 32)
	if _, err := rc.Read(buf); err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("closing an export early errored: %v", err)
	}

	// A second close must also be safe: handlers close in defer as well as
	// on the error path.
	_ = rc.Close()
}
