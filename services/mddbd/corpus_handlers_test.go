package main

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// AGT-005: the corpus endpoints' statuses.

func corpusCall(h http.HandlerFunc, method, body string, admin *bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/corpus", strings.NewReader(body))
	if admin != nil {
		req = req.WithContext(authenticatedContext("someone", *admin))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCorpusEndpointsRefuse(t *testing.T) {
	s := corpusServer(t, 256)
	for name, h := range map[string]http.HandlerFunc{
		"build": s.handleCorpusBuild, "install": s.handleCorpusInstall, "remove": s.handleCorpusRemove,
	} {
		if rec := corpusCall(h, http.MethodGet, "", nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s answered GET with %d", name, rec.Code)
		}
	}
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		body string
		code int
	}{
		{"build: not JSON", s.handleCorpusBuild, "{", http.StatusBadRequest},
		{"build: no licence", s.handleCorpusBuild, `{"collection":"docs"}`, http.StatusBadRequest},
		{"build: no such collection", s.handleCorpusBuild, `{"collection":"none","license":"MIT"}`, http.StatusNotFound},
		{"install: not multipart", s.handleCorpusInstall, "{}", http.StatusBadRequest},
		{"remove: not JSON", s.handleCorpusRemove, "{", http.StatusBadRequest},
		{"remove: no such pack", s.handleCorpusRemove, `{"name":"none"}`, http.StatusNotFound},
	} {
		if rec := corpusCall(tc.h, http.MethodPost, tc.body, nil); rec.Code != tc.code {
			t.Errorf("%s: %d, want %d: %s", tc.name, rec.Code, tc.code, rec.Body.String())
		}
	}

	// A form without the pack field.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("other", "x")
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/corpus/install", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.handleCorpusInstall(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `no \"pack\" field`) {
		t.Errorf("install without a pack: %d %s", rec.Code, rec.Body.String())
	}
	// A pack that is not one.
	if rec := installPack(t, s, []byte("garbage")); rec.Code != http.StatusBadRequest {
		t.Errorf("install of garbage: %d %s", rec.Code, rec.Body.String())
	}
	// The server's own failure.
	path := s.Path
	t.Cleanup(func() { s.Path = path })
	s.Path = filepath.Join(t.TempDir(), "missing", "x.db")
	if rec := installPack(t, s, []byte("garbage")); rec.Code != http.StatusInternalServerError {
		t.Errorf("install without a staging directory: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCorpusEndpointsNeedAdmin(t *testing.T) {
	s, _, cleanup := setupTestServer(t)
	defer cleanup()
	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b-pack", "a-pack"} {
		_ = s.CollectionManager.put(name, &CollectionConfig{ReadOnly: true, Corpus: &CorpusInfo{Name: name}})
	}
	_ = s.CollectionManager.put("plain", &CollectionConfig{})
	no, yes := false, true
	for name, h := range map[string]http.HandlerFunc{
		"build": s.handleCorpusBuild, "install": s.handleCorpusInstall, "remove": s.handleCorpusRemove, "list": s.handleCorpusList,
	} {
		if rec := corpusCall(h, http.MethodPost, "{}", &no); rec.Code != http.StatusForbidden {
			t.Errorf("%s let a non-admin in: %d", name, rec.Code)
		}
	}
	if rec := corpusCall(s.handleCorpusList, http.MethodGet, "", &yes); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":2`) ||
		strings.Index(rec.Body.String(), "a-pack") > strings.Index(rec.Body.String(), "b-pack") {
		t.Errorf("list for an admin: %d %s", rec.Code, rec.Body.String())
	}
	if rec := corpusCall(s.handleCorpusRemove, http.MethodPost, `{"name":"none"}`, &yes); rec.Code != http.StatusNotFound {
		t.Errorf("remove for an admin: %d %s", rec.Code, rec.Body.String())
	}
}

// goneClient is a response whose body cannot be written: a client gone.
type goneClient struct{ *httptest.ResponseRecorder }

func (goneClient) Write([]byte) (int, error) { return 0, errors.New("client gone") }

func TestCorpusBuildToAClientThatLeft(t *testing.T) {
	s := corpusServer(t, 256)
	seedCorpus(t, s)
	w := goneClient{httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodPost, "/v1/corpus/build", strings.NewReader(`{"collection":"docs","license":"MIT"}`))
	s.handleCorpusBuild(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Content-Disposition") != `attachment; filename="docs.mddbpack"` {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}
