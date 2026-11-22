package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

// #447: the /qdrant surface driven through main's middleware chain, with
// the requests qdrant-client 1.20 sends.

// qdrantTestServer is corpusServer with the surface on and main's handler.
func qdrantTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv(qdrantCompatEnv, "true")
	s := corpusServer(t, 256)
	s.Ready = true
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	return s, esHandler(t, s, false)
}

// qdrantDo sends a request and decodes the JSON object answer; hdr is
// name/value pairs.
func qdrantDo(t *testing.T, h http.Handler, method, path, body string, hdr ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

// qdrantErrText is the message of a Qdrant error envelope.
func qdrantErrText(out map[string]any) string {
	st, _ := out["status"].(map[string]any)
	msg, _ := st["error"].(string)
	return msg
}

func TestQdrantSurfaceIsOffByDefault(t *testing.T) {
	s := corpusServer(t, 256)
	s.Ready = true
	rec, _ := qdrantDo(t, esHandler(t, s, false), http.MethodGet, "/qdrant", "")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "version") {
		t.Fatalf("disabled surface answered %d %s", rec.Code, rec.Body.String())
	}
}

func TestQdrantRootProbe(t *testing.T) {
	_, h := qdrantTestServer(t)
	rec, out := qdrantDo(t, h, http.MethodGet, "/qdrant", "")
	if rec.Code != http.StatusOK || out["version"] != qdrantCompatVersion || !strings.HasPrefix(out["title"].(string), "mddb") {
		t.Fatalf("probe: %d %v", rec.Code, out)
	}
	if rec, _ = qdrantDo(t, h, http.MethodHead, "/qdrant/", ""); rec.Code != http.StatusOK {
		t.Fatalf("HEAD: %d", rec.Code)
	}
}

func TestQdrantUnsupportedRoutesAreNamed(t *testing.T) {
	_, h := qdrantTestServer(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodDelete, "/qdrant"},
		{http.MethodGet, "/qdrant/collections"},
		{http.MethodGet, "/qdrant/collections/books/points/query"},
		{http.MethodPut, "/qdrant/collections/books/points"},
		{http.MethodPost, "/qdrant/collections/books/points/search"},
	} {
		rec, out := qdrantDo(t, h, c.method, c.path, "{}")
		if rec.Code != http.StatusNotFound || !strings.Contains(qdrantErrText(out), c.method+" "+c.path) {
			t.Errorf("%s %s: %d %v", c.method, c.path, rec.Code, out)
		}
	}
}

func TestQdrantAPIKeyHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/qdrant", nil)
	r.Header.Set("api-key", " mddb_x ")
	if got := qdrantAPIKey(r); got != "mddb_x" {
		t.Fatalf("got %q", got)
	}
	if !isQdrantPath("/qdrant") || !isQdrantPath("/qdrant/x") || isQdrantPath("/qdrantx") || isQdrantPath("/v1/qdrant") {
		t.Fatal("isQdrantPath")
	}
	// /v1 ignores the header: it accepts exactly the credentials it always has.
	r = httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	r.Header.Set("api-key", "mddb_x")
	if key, _ := requestCredentials(r); key != "" {
		t.Fatalf("/v1 read api-key: %q", key)
	}
}

func TestQdrantConfigReportsTheSurface(t *testing.T) {
	_, h := qdrantTestServer(t)
	rec, out := qdrantDo(t, h, http.MethodGet, "/v1/config", "")
	q, _ := out["qdrantCompat"].(map[string]any)
	if rec.Code != http.StatusOK || q["enabled"] != true || q["prefix"] != qdrantPathPrefix {
		t.Fatalf("config: %d %v", rec.Code, out["qdrantCompat"])
	}
}

func TestQdrantPermissions(t *testing.T) {
	s, _ := qdrantTestServer(t)
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s.AuthManager = am
	h := esHandler(t, s, true)
	if _, err := am.CreateUser("reader", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := am.SetPermission(&Permission{Username: "reader", Collection: "mine", Read: true}); err != nil {
		t.Fatal(err)
	}
	key, err := am.CreateAPIKey("reader", "qdrant", 0)
	if err != nil {
		t.Fatal(err)
	}
	doc := addTestDoc(t, s, "mine", "a", "en", "The owl hunts mice at night", nil)
	waitForEmbedding(t, s, "mine", doc.ID)
	body := qdrantVectorBody(t, s, "owl at night", "")

	if rec, out := qdrantDo(t, h, http.MethodPost, "/qdrant/collections/mine/points/query", body, "api-key", key); rec.Code != http.StatusOK || len(qdrantPoints(out)) != 1 {
		t.Fatalf("api-key on /qdrant: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := qdrantDo(t, h, http.MethodPost, "/qdrant/collections/mine/points/query", body, "Authorization", "Bearer "+key); rec.Code != http.StatusOK {
		t.Fatalf("Bearer key on /qdrant: %d", rec.Code)
	}
	if rec, _ := qdrantDo(t, h, http.MethodGet, "/qdrant", "", "api-key", key); rec.Code != http.StatusOK {
		t.Fatalf("probe with a key: %d", rec.Code)
	}
	// No permission: 403 whether the collection exists or not.
	for _, coll := range []string{"theirs", "no-such-collection"} {
		addTestDoc(t, s, "theirs", "b", "en", "secret", nil)
		rec, out := qdrantDo(t, h, http.MethodPost, "/qdrant/collections/"+coll+"/points/query", body, "api-key", key)
		if rec.Code != http.StatusForbidden || qdrantErrText(out) != "forbidden" {
			t.Fatalf("%s: %d %v", coll, rec.Code, out)
		}
	}
	if rec, _ := qdrantDo(t, h, http.MethodPost, "/qdrant/collections/mine/points/query", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials: %d", rec.Code)
	}
	if rec, _ := qdrantDo(t, h, http.MethodPost, "/qdrant/collections/mine/points/query", body, "api-key", "mddb_wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", rec.Code)
	}
}

func TestQdrantFailStatuses(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
	}{
		{&http.MaxBytesError{Limit: 1}, http.StatusRequestEntityTooLarge},
		{&esUpstreamError{Status: 422, Message: "x"}, 422},
		{errors.New("plain"), http.StatusTeapot},
	} {
		rec := httptest.NewRecorder()
		qdrantFail(rec, time.Now(), c.err, http.StatusTeapot)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != c.status || qdrantErrText(out) == "" {
			t.Errorf("%v: %d %v", c.err, rec.Code, out)
		}
	}
}
