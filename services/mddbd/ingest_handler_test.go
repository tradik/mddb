package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	proto "mddb/proto"
)

// The /v1/ingest handler's refusals, and the per-document errors of the
// pre-processing step.
func TestIngestHandlerRefusals(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	srv.handleIngest(rec, httptest.NewRequest(http.MethodGet, "/v1/ingest", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.handleIngest(rec, httptest.NewRequest(http.MethodPost, "/v1/ingest", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: %d", rec.Code)
	}
	if rec := doRequest(t, srv.handleIngest, map[string]any{"documents": []any{}}); rec.Code != http.StatusBadRequest {
		t.Errorf("no collection: %d", rec.Code)
	}
	if rec := doRequest(t, srv.handleIngest, map[string]any{"collection": "c"}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"collection":"c"`) {
		t.Errorf("no documents: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, srv.handleIngest, map[string]any{"collection": "c", "documents": []any{map[string]any{"key": "a", "lang": "en"}},
		"options": map[string]any{"profile": "turbo"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown profile: %d", rec.Code)
	}

	resp, err := srv.ingestDocuments(context.Background(), "c", []IngestDocumentHTTP{{Lang: "en", ContentMD: "x"}, {Key: "k", ContentMD: "x"}},
		IngestOptionsHTTP{})
	if err != nil || resp.Failed != 2 || !strings.Contains(resp.Errors[0], "cannot derive key") || !strings.Contains(resp.Errors[1], "missing lang") {
		t.Fatalf("%+v %v", resp, err)
	}
}

func TestIngestProtoMetaAndErrors(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	resp, err := srv.ingestDocumentsFromProto(context.Background(), &proto.IngestRequest{Collection: "p", Documents: []*proto.IngestDocument{
		{Key: "a", Lang: "en", ContentMd: "body", Meta: map[string]*proto.MetaValues{"tag": {Values: []string{"t"}}}},
		{Key: "b"},
	}})
	if err != nil || resp.Added != 1 {
		t.Fatalf("%+v %v", resp, err)
	}
	if doc, err := srv.loadDocByRef("p", "a", "en"); err != nil || doc.Meta["tag"][0] != "t" {
		t.Fatalf("meta: %v %v", doc, err)
	}
	if out := protoFromIngestResponse(resp); out.Failed != 1 || len(out.Errors) != 1 {
		t.Fatalf("proto response: %+v", out)
	}
}
