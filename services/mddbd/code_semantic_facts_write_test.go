package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	proto "mddb/proto"
)

// CODE-010 through the real handlers: which writes keep semantic facts, and
// which reads show them.

const factsCollection = "facts"

// factsDoc is a Go file with valid facts for its content.
func factsDoc(content string) map[string][]string {
	return map[string][]string{
		"path":          {"svc/run.go"},
		MetaKeySymIndex: {"v1 " + semanticContentHash(content) + " 000000000000 gopls/0.24.0"},
		MetaKeySymDefs:  {"Run function 3-5"},
		MetaKeySymRefs:  {"svc/log.go#Log 4"},
		MetaKeySymImpls: {"svc/iface.go#Runner Run"},
	}
}

const factsContent = "package svc\n\nfunc Run() {\n\tLog()\n}\n"

func callHandler(t *testing.T, h http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	return w
}

// getDocMeta reads one document through POST /v1/get.
func getDocMeta(t *testing.T, s *Server, collection, key string) map[string][]string {
	t.Helper()
	w := callHandler(t, s.handleGet, http.MethodPost, "/v1/get",
		map[string]string{"collection": collection, "key": key, "lang": "en"})
	var doc storage.Doc
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Meta
}

func addFactsDoc(t *testing.T, s *Server, key string) {
	t.Helper()
	callHandler(t, s.handleAdd, http.MethodPost, "/v1/add", map[string]any{
		"collection": factsCollection, "key": key, "lang": "en",
		"contentMd": factsContent, "meta": factsDoc(factsContent),
	})
}

func wantAllFacts(t *testing.T, meta map[string][]string) {
	t.Helper()
	if got, want := symMeta(meta), factsDoc(factsContent); !reflect.DeepEqual(got, symMeta(want)) {
		t.Errorf("facts = %v, want %v", got, symMeta(want))
	}
}

func TestSemanticFactsAddKeepsThem(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addFactsDoc(t, s, "svc/run.go")
	meta := getDocMeta(t, s, factsCollection, "svc/run.go")
	wantAllFacts(t, meta)
	// The extractor's keys sit beside them, untouched by the convention.
	if len(meta[MetaKeyDefines]) == 0 {
		t.Errorf("defines missing: %v", meta)
	}
}

func TestSemanticFactsContentEditDropsThem(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addFactsDoc(t, s, "svc/run.go")
	callHandler(t, s.handleUpdate, http.MethodPatch, "/v1/update", map[string]any{
		"collection": factsCollection, "key": "svc/run.go", "lang": "en",
		"contentMd": factsContent + "// edited\n",
	})
	if got := symMeta(getDocMeta(t, s, factsCollection, "svc/run.go")); len(got) != 0 {
		t.Errorf("an edit through the API kept stale facts: %v", got)
	}
}

func TestSemanticFactsMetaOnlyUpdateKeepsThem(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addFactsDoc(t, s, "svc/run.go")
	// Meta is replaced as a whole: what a client read with GET and sends
	// back, plus its change, is checked against the unchanged content.
	meta := getDocMeta(t, s, factsCollection, "svc/run.go")
	meta["tag"] = []string{"reviewed"}
	callHandler(t, s.handleUpdate, http.MethodPatch, "/v1/update", map[string]any{
		"collection": factsCollection, "key": "svc/run.go", "lang": "en", "meta": meta,
	})
	got := getDocMeta(t, s, factsCollection, "svc/run.go")
	wantAllFacts(t, got)
	if !reflect.DeepEqual(got["tag"], []string{"reviewed"}) {
		t.Errorf("tag = %v", got["tag"])
	}
	// A TTL-only patch starts from the stored meta and keeps them too.
	callHandler(t, s.handleUpdate, http.MethodPatch, "/v1/update", map[string]any{
		"collection": factsCollection, "key": "svc/run.go", "lang": "en", "ttl": 3600,
	})
	wantAllFacts(t, getDocMeta(t, s, factsCollection, "svc/run.go"))
}

func TestSemanticFactsBatchAndBulkIngestKeepThem(t *testing.T) {
	s, cleanup := httpBulkTestServer(t)
	defer cleanup()
	callHandler(t, s.handleAddBatch, http.MethodPost, "/v1/add-batch", map[string]any{
		"collection": factsCollection,
		"documents": []map[string]any{{"key": "svc/run.go", "lang": "en",
			"contentMd": factsContent, "meta": factsDoc(factsContent)}},
	})
	wantAllFacts(t, getDocMeta(t, s, factsCollection, "svc/run.go"))

	job, err := s.BulkIngest.Submit(factsCollection, []*proto.BatchDocument{{
		Key: "svc/bulk.go", Lang: "en", ContentMd: factsContent,
		Meta: toProtoMeta(factsDoc(factsContent)),
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, s.BulkIngest, job.ID, 10*time.Second, BulkJobCompleted)
	wantAllFacts(t, getDocMeta(t, s, factsCollection, "svc/bulk.go"))
}

func TestSemanticFactsOversizedValueIsDroppedNotFatal(t *testing.T) {
	// bbolt refuses a key over 32 KB; every meta value is part of an index
	// key, so a 40 KB value must be dropped before it reaches the index.
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	meta := factsDoc(factsContent)
	huge := "svc/log.go#Log " + strings.Repeat("9", 40*1024)
	meta[MetaKeySymRefs] = append(meta[MetaKeySymRefs], huge)
	callHandler(t, s.handleAdd, http.MethodPost, "/v1/add", map[string]any{
		"collection": factsCollection, "key": "svc/run.go", "lang": "en",
		"contentMd": factsContent, "meta": meta,
	})
	wantAllFacts(t, getDocMeta(t, s, factsCollection, "svc/run.go"))
}
