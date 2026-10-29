package main

import (
	"context"
	"strings"
	"testing"
)

// MCP: ingest_documents carries files and text_only; upload_file takes
// text_only too, through the same dispatch (AGT-001).
func TestMCPIngestAndUploadConvertFiles(t *testing.T) {
	fake := covBNewFake()
	if _, err := covBRun(t, fake, "ingest_documents", map[string]interface{}{
		"collection": "c",
		"documents": []interface{}{map[string]interface{}{
			"key": "a.pdf", "lang": "en", "format": "pdf", "content_base64": covBB64("%PDF-1.4"),
		}},
		"options": map[string]interface{}{"text_only": true},
	}); err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "Ingest").(*MCPIngestRequest)
	if d := r.Documents[0]; d.Format != "pdf" || string(d.Content) != "%PDF-1.4" || !r.Options.TextOnly {
		t.Fatalf("request: %+v", r)
	}

	if _, err := covBRun(t, covBNewFake(), "ingest_documents", map[string]interface{}{
		"collection": "c", "documents": []interface{}{map[string]interface{}{"key": "a", "lang": "en", "content_base64": "!!!"}},
	}); err == nil || !strings.Contains(err.Error(), "document at index 0: invalid base64 content") {
		t.Fatalf("bad base64: %v", err)
	}

	fake = covBNewFake()
	if _, err := covBRun(t, fake, "upload_file", map[string]interface{}{
		"collection": "c", "filename": "p.html", "content": covBB64("<h1>Plain</h1>"), "lang": "en", "text_only": true,
	}); err != nil {
		t.Fatal(err)
	}
	if add := fake.covBCall(t, "Add").(*MCPAddRequest); add.ContentMD != "Plain" {
		t.Fatalf("text_only upload stored %q", add.ContentMD)
	}
}

// The direct client hands text_only to the server's ingest.
func TestDirectClientIngestTextOnly(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	if _, err := client.Ingest(context.Background(), &MCPIngestRequest{Collection: "d", Documents: []MCPIngestDocument{
		{Key: "x.html", Lang: "en", Content: []byte("<h1>Heading</h1>")},
	}, Options: MCPIngestOptions{TextOnly: true, SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true}}); err != nil {
		t.Fatal(err)
	}
	if doc, err := srv.loadDocByRef("d", "x.html", "en"); err != nil || doc.ContentMD != "Heading" {
		t.Fatalf("%v %v", doc, err)
	}
}
