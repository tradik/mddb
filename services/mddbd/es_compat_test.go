package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// INT-019: the /es surface, driven through the same middleware chain main
// builds — aliases, body cap, metrics, auth, rate limiter — with the headers
// elasticsearch-py 9.x sends.

// esCompatMime is the content type elasticsearch-py 9 sends and accepts.
const esCompatMime = "application/vnd.elasticsearch+json; compatible-with=9"

// esTestServer is corpusServer (FTS, routers, lexical embeddings) with the
// surface switched on and the full HTTP handler built around it.
func esTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv(esCompatEnv, "true")
	s := corpusServer(t, 256)
	s.Ready = true
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	return s, esHandler(t, s, false)
}

// esHandler builds main's handler for s.
func esHandler(t *testing.T, s *Server, auth bool) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux, auth)
	h, _, _ := s.wrapHTTPHandler(mux, auth, "internal")
	return h
}

// esDo sends a request as the client does and decodes a JSON object answer.
// hdr is name/value pairs.
func esDo(t *testing.T, h http.Handler, method, path, body string, hdr ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Accept", esCompatMime)
	if body != "" {
		req.Header.Set("Content-Type", esCompatMime)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 && method != http.MethodHead {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

// esErrType is the error type of an Elasticsearch error body.
func esErrType(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	typ, _ := e["type"].(string)
	return typ
}

func TestESSurfaceIsOffByDefault(t *testing.T) {
	s := corpusServer(t, 256)
	s.Ready = true
	h := esHandler(t, s, false)
	rec, _ := esDo(t, h, http.MethodGet, "/es", "")
	if rec.Code != http.StatusNotFound || rec.Header().Get("X-Elastic-Product") != "" {
		t.Fatalf("disabled surface answered %d %v", rec.Code, rec.Header())
	}
	rec, _ = esDo(t, h, http.MethodPost, "/v1/fts", `{"collection":"x","query":"y"}`, "Content-Type", "application/json")
	if rec.Header().Get("X-Elastic-Product") != "" {
		t.Fatal("/v1 must never carry the Elasticsearch product header")
	}
}

func TestESRootInfoAndPing(t *testing.T) {
	_, h := esTestServer(t)
	rec, out := esDo(t, h, http.MethodGet, "/es", "")
	if rec.Code != http.StatusOK || rec.Header().Get("X-Elastic-Product") != "Elasticsearch" {
		t.Fatalf("info: %d %v", rec.Code, rec.Header())
	}
	v := out["version"].(map[string]any)
	if v["number"] != esCompatVersion || v["build_flavor"] != "default" || !strings.Contains(out["tagline"].(string), "not Elasticsearch") {
		t.Fatalf("info body %v", out)
	}
	if rec, _ = esDo(t, h, http.MethodHead, "/es/", ""); rec.Code != http.StatusOK {
		t.Fatalf("ping: %d", rec.Code)
	}
	if rec, out = esDo(t, h, http.MethodDelete, "/es/", ""); rec.Code != http.StatusBadRequest || esErrType(out) != "illegal_argument_exception" {
		t.Fatalf("DELETE /: %d %v", rec.Code, out)
	}
}

func TestESUnsupportedEndpointsAreNamed(t *testing.T) {
	_, h := esTestServer(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/es/_cat/indices"},
		{http.MethodGet, "/es/.kibana/_search"},
		{http.MethodGet, "/es/books"},
		{http.MethodPut, "/es/books"},
		{http.MethodGet, "/es/books/_mapping"},
		{http.MethodPost, "/es/books/_update/1"},
		{http.MethodGet, "/es/books/_doc/1/extra"},
		{http.MethodGet, "/es/_bulk/x"},
		{http.MethodGet, "/es/_bulk"},
	} {
		rec, out := esDo(t, h, c.method, c.path, "")
		reason, _ := out["error"].(map[string]any)["reason"].(string)
		if rec.Code != http.StatusBadRequest || !strings.Contains(reason, c.method+" "+c.path) {
			t.Errorf("%s %s: %d %v", c.method, c.path, rec.Code, out)
		}
	}
}

func TestESIndexExistsAndRefresh(t *testing.T) {
	s, h := esTestServer(t)
	if rec, _ := esDo(t, h, http.MethodHead, "/es/books", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing index: %d", rec.Code)
	}
	addTestDoc(t, s, "books", "a", "en", "text", nil)
	if rec, _ := esDo(t, h, http.MethodHead, "/es/books", ""); rec.Code != http.StatusOK {
		t.Fatalf("existing index: %d", rec.Code)
	}
	rec, out := esDo(t, h, http.MethodPost, "/es/books/_refresh", "")
	if rec.Code != http.StatusOK || out["_shards"].(map[string]any)["successful"] != float64(1) {
		t.Fatalf("refresh: %d %v", rec.Code, out)
	}
}

func TestESRefreshReportsATimeout(t *testing.T) {
	s, h := esTestServer(t)
	// A document whose embedding never finishes, and a client that has
	// already gone: the wait ends at once, undrained.
	s.embeddingTracker().Add("books", "books|stuck|en")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/es/books/_refresh", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"successful":0`) {
		t.Fatalf("a refresh that could not wait must say so: %s", rec.Body.String())
	}
}

func TestESAliasResolvesToItsCollection(t *testing.T) {
	s, h := esTestServer(t)
	addTestDoc(t, s, "books-v2", "a", "en", "alias target", nil)
	if err := s.SetAlias("books", "books-v2"); err != nil {
		t.Fatal(err)
	}
	rec, out := esDo(t, h, http.MethodGet, "/es/books/_doc/a", "")
	if rec.Code != http.StatusOK || out["_index"] != "books-v2" {
		t.Fatalf("alias: %d %v", rec.Code, out)
	}
}

func TestESAPIKeyHeader(t *testing.T) {
	for _, c := range []struct{ header, want string }{
		{"ApiKey mddb_x", "mddb_x"},
		{"apikey  mddb_y ", "mddb_y"},
		{"Bearer mddb_x", ""},
		{"ApiKey", ""},
		{"", ""},
	} {
		r := httptest.NewRequest(http.MethodGet, "/es", nil)
		r.Header.Set("Authorization", c.header)
		if got := esAPIKey(r); got != c.want {
			t.Errorf("%q: got %q, want %q", c.header, got, c.want)
		}
	}
	if !isESPath("/es") || !isESPath("/es/x") || isESPath("/esx") || isESPath("/v1/es") {
		t.Fatal("isESPath")
	}
}

func TestESResponseHelpers(t *testing.T) {
	rec := httptest.NewRecorder()
	esJSON(rec, http.StatusOK, map[string]any{"bad": make(chan int)})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("an unencodable body is a 500, got %d", rec.Code)
	}
	for status, typ := range map[int]string{
		http.StatusForbidden: "security_exception", http.StatusNotFound: "resource_not_found_exception",
		http.StatusConflict: "version_conflict_engine_exception", http.StatusTooManyRequests: "quota_exceeded_exception",
		http.StatusInsufficientStorage: "quota_exceeded_exception", http.StatusServiceUnavailable: "unavailable_exception",
		http.StatusUnprocessableEntity: "illegal_argument_exception",
	} {
		if got := esErrorType(status); got != typ {
			t.Errorf("%d: %s, want %s", status, got, typ)
		}
	}
	for _, c := range []struct {
		err    error
		status int
		typ    string
	}{
		{&esUpstreamError{Status: 422, Message: "x"}, 422, "illegal_argument_exception"},
		{dslErr("x", "y"), 400, "parsing_exception"},
		{&QuotaError{Collection: "c", Resource: "documents", Limit: 1, Used: 1}, 507, "quota_exceeded_exception"},
		{&ReadOnlyError{Collection: "c"}, 403, "cluster_block_exception"},
		{errDocNotFound, 404, "resource_not_found_exception"},
		{fmt.Errorf("w: %w", &RevisionConflictError{Expected: "a"}), 409, "version_conflict_engine_exception"},
		{errors.New("plain"), 400, "illegal_argument_exception"},
	} {
		if status, typ := esStatusOf(c.err); status != c.status || typ != c.typ {
			t.Errorf("%v: %d %s", c.err, status, typ)
		}
	}
	rec = httptest.NewRecorder()
	esFail(rec, &QuotaError{Resource: "embeddingCalls", RetryAfter: 90e9})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "90" {
		t.Fatalf("a budget refusal keeps Retry-After: %d %v", rec.Code, rec.Header())
	}
}

func TestESCallReportsHandlerFailures(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/es/x/_search", nil)
	if err := esCall(r, nil, make(chan int), nil); err == nil {
		t.Fatal("an unencodable request must fail")
	}
	plain := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(" busy \n"))
	}
	var up *esUpstreamError
	if err := esCall(r, plain, map[string]int{}, nil); !errors.As(err, &up) || up.Status != 503 || up.Message != "busy" {
		t.Fatalf("a plain-text failure keeps its text: %v", err)
	}
}

func TestESBodyErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	esBodyError(rec, &http.MaxBytesError{Limit: 1})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the cap: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	esBodyError(rec, errors.New("reset"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("read error: %d", rec.Code)
	}
}

// errReader fails every read, for the body error paths.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestESHandlersReportUnreadableBodies(t *testing.T) {
	s, _ := esTestServer(t)
	for name, h := range map[string]func(w http.ResponseWriter, r *http.Request){
		"search": func(w http.ResponseWriter, r *http.Request) { s.esSearch(w, r, "books", false) },
		"index":  func(w http.ResponseWriter, r *http.Request) { s.esIndexDoc(w, r, "books", "a", false) },
		"bulk":   func(w http.ResponseWriter, r *http.Request) { s.esBulk(w, r, "books") },
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/es/books", errReader{}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}
