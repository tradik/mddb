package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// The batch delete answers notFound like the rest of the camelCase API, and
// keeps not_found — deprecated, to be removed in a later minor — for callers
// written against it.

func TestDeleteBatchAnswersNotFoundInCamelCaseAndKeepsTheAlias(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "here", "en", "x", nil)

	rec := doRequest(t, s.handleDeleteBatch, map[string]any{
		"collection": "blog",
		"documents":  []map[string]string{{"key": "here", "lang": "en"}, {"key": "gone", "lang": "en"}, {"key": "", "lang": "en"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete-batch: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["deleted"] != float64(1) || resp["notFound"] != float64(1) || resp["not_found"] != float64(1) || resp["failed"] != float64(1) {
		t.Errorf("counts: %s", rec.Body.String())
	}
}

// The same pair on MCP, which shares the response shape.
func TestMCPDeleteBatchAnswersNotFoundInCamelCase(t *testing.T) {
	client, _, cleanup := directClientServer(t)
	defer cleanup()
	got, err := client.DeleteBatch(context.Background(), &MCPDeleteBatchRequest{
		Collection: "docs",
		Documents:  []MCPDeleteDocument{{Key: "gone", Lang: "en"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"notFound":1`) || !strings.Contains(string(b), `"not_found":1`) {
		t.Errorf("MCP delete_batch: %s", b)
	}
	ub, _ := json.Marshal(&MCPUpdateBatchResponse{NotFound: 2, NotFoundDeprecated: 2})
	if !strings.Contains(string(ub), `"notFound":2`) || !strings.Contains(string(ub), `"not_found":2`) {
		t.Errorf("MCP update_batch: %s", ub)
	}
}

// The other paths of the delete handlers: malformed and incomplete requests,
// refusals, a failing store, and a response that cannot be written.
func TestDeleteHandlersRefuseAndReport(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	for _, body := range []string{`{`, `{"documents":[{"key":"a","lang":"en"}]}`, `{"collection":"blog"}`} {
		w := httptest.NewRecorder()
		s.handleDeleteBatch(w, httptest.NewRequest(http.MethodPost, "/v1/delete-batch", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("delete-batch %s: %d", body, w.Code)
		}
	}

	covEDenyAll(t, s)
	for name, h := range map[string]http.HandlerFunc{
		"delete":            s.handleDelete,
		"delete-batch":      s.handleDeleteBatch,
		"delete-collection": s.handleDeleteCollection,
	} {
		w := httptest.NewRecorder()
		body := `{"collection":"blog","key":"a","lang":"en","documents":[{"key":"a","lang":"en"}]}`
		h(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).WithContext(context.Background()))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s for an anonymous caller: %d", name, w.Code)
		}
	}
	s.AuthManager = nil

	addTestDoc(t, s, "blog", "a", "en", "x", nil)
	fw := &covGFailWriter{}
	s.handleDeleteCollection(fw, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"collection":"blog"}`)))
	if fw.code != 0 && fw.code != http.StatusOK {
		t.Errorf("delete-collection to a gone client: %d", fw.code)
	}

	// Both storage layouts: the collection may live in its own file.
	closeStorage(t, s, "blog")
	rec := doRequest(t, s.handleDeleteBatch, map[string]any{"collection": "blog", "documents": []map[string]string{{"key": "a", "lang": "en"}}})
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["failed"] != float64(1) || resp["notFound"] != float64(0) {
		t.Errorf("closed store: %s", rec.Body.String())
	}
	if rec := doRequest(t, s.handleDeleteCollection, DeleteCollectionRequest{Collection: "blog"}); rec.Code != http.StatusBadRequest {
		t.Errorf("delete-collection on a closed store: %d %s", rec.Code, rec.Body.String())
	}
}
