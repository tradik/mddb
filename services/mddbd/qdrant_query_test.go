package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"mddb/internal/embedding"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
)

// #447: POST /qdrant/collections/{name}/points/query with the bodies
// qdrant-client's query_points sends.

const qdrantBooks = "/qdrant/collections/books/points/query"

// qdrantVectorBody is a query_points body for text embedded with the
// server's provider, as Open WebUI embeds it with its own; extra is more
// JSON fields, each with a leading comma.
func qdrantVectorBody(t *testing.T, s *Server, text, extra string) string {
	t.Helper()
	v, err := s.Embedding.Embed(t.Context(), text, embedding.RoleQuery)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	return `{"query":` + string(b) + `,"limit":10,"with_payload":true,"with_vector":false` + extra + `}`
}

// qdrantPoints lists the points of a query answer.
func qdrantPoints(out map[string]any) []map[string]any {
	res, _ := out["result"].(map[string]any)
	raw, _ := res["points"].([]any)
	points := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		points = append(points, p.(map[string]any))
	}
	return points
}

// qdrantSeed writes two documents and waits for their embeddings.
func qdrantSeed(t *testing.T, s *Server) {
	t.Helper()
	for key, text := range map[string]string{
		"owl": "The owl hunts mice at night", "fox": "The quick brown fox jumps over the lazy dog",
	} {
		meta := map[string][]string{"tags": {"bird", "night"}}
		if key == "fox" {
			meta = map[string][]string{"title": {"The Fox"}, "source": {"fables.md"}}
		}
		waitForEmbedding(t, s, "books", addTestDoc(t, s, "books", key, "en", text, meta).ID)
	}
}

var uuidV8 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-8[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestQdrantQueryPoints(t *testing.T) {
	s, h := qdrantTestServer(t)
	qdrantSeed(t, s)
	rec, out := qdrantDo(t, h, http.MethodPost, qdrantBooks, qdrantVectorBody(t, s, "owl hunting at night", ""))
	points := qdrantPoints(out)
	if rec.Code != http.StatusOK || out["status"] != "ok" || len(points) != 2 {
		t.Fatalf("query: %d %s", rec.Code, rec.Body.String())
	}
	owl, fox := points[0], points[1]
	payload := owl["payload"].(map[string]any)
	md := payload["metadata"].(map[string]any)
	if payload["text"] != "The owl hunts mice at night" || md["key"] != "owl" || md["collection"] != "books" ||
		md["lang"] != "en" || md["source"] != "owl" || md["name"] != "owl" || md["chunk"] != float64(0) {
		t.Fatalf("owl payload %v", payload)
	}
	if tags, _ := md["tags"].([]any); len(tags) != 2 {
		t.Fatalf("a multi-valued field stays a list: %v", md["tags"])
	}
	fmd := fox["payload"].(map[string]any)["metadata"].(map[string]any)
	if fmd["source"] != "fables.md" || fmd["name"] != "The Fox" || fmd["title"] != "The Fox" {
		t.Fatalf("the document's own source and title win: %v", fmd)
	}
	if !uuidV8.MatchString(owl["id"].(string)) || owl["id"] == fox["id"] || owl["version"] != float64(0) {
		t.Fatalf("ids %v %v", owl["id"], fox["id"])
	}
	if owl["score"].(float64) <= fox["score"].(float64) {
		t.Fatalf("higher score is closer: %v %v", owl["score"], fox["score"])
	}

	// {"nearest": vector}, offset, no payload, and the same id again.
	v, _ := s.Embedding.Embed(t.Context(), "owl hunting at night", embedding.RoleQuery)
	b, _ := json.Marshal(map[string]any{"query": map[string]any{"nearest": v}, "offset": 1, "with_payload": false,
		"using": nil, "params": map[string]any{"hnsw_ef": 128}, "filter": nil})
	rec, out = qdrantDo(t, h, http.MethodPost, qdrantBooks, string(b))
	if p := qdrantPoints(out); rec.Code != http.StatusOK || len(p) != 1 || p[0]["id"] != fox["id"] || p[0]["payload"] != nil {
		t.Fatalf("nearest+offset: %d %s", rec.Code, rec.Body.String())
	}
	// An offset past the end is an empty page, not an error.
	rec, out = qdrantDo(t, h, http.MethodPost, qdrantBooks, qdrantVectorBody(t, s, "owl", `,"offset":5`))
	if rec.Code != http.StatusOK || len(qdrantPoints(out)) != 0 {
		t.Fatalf("offset past the end: %d %s", rec.Code, rec.Body.String())
	}
	// score_threshold drops the weaker match.
	threshold := (owl["score"].(float64) + fox["score"].(float64)) / 2
	b, _ = json.Marshal(threshold)
	rec, out = qdrantDo(t, h, http.MethodPost, qdrantBooks, qdrantVectorBody(t, s, "owl hunting at night", `,"score_threshold":`+string(b)))
	if rec.Code != http.StatusOK || len(qdrantPoints(out)) != 1 {
		t.Fatalf("threshold: %d %s", rec.Code, rec.Body.String())
	}
}

func TestQdrantQueryRefusals(t *testing.T) {
	s, h := qdrantTestServer(t)
	qdrantSeed(t, s)
	vecBody := qdrantVectorBody(t, s, "owl", "")
	cut := strings.TrimSuffix(vecBody, "}")
	for body, want := range map[string]string{
		`[]`:                              "body:",
		`{}`:                              "query:",
		`{"query":{"recommend":{}}}`:      "query:",
		`{"query":"5f0e"}`:                "query:",
		`{"query":[]}`:                    "query:",
		cut + `,"using":"dense"}`:         `using: an MDDB collection has one unnamed vector; leave the vector name empty (got "dense")`,
		cut + `,"using":7}`:               `(got "7")`,
		cut + `,"limit":0}`:               "limit:",
		cut + `,"offset":-1}`:             "offset:",
		cut + `,"score_threshold":"x"}`:   "score_threshold:",
		cut + `,"with_payload":["text"]}`: "with_payload",
		strings.Replace(vecBody, `"with_vector":false`, `"with_vector":true`, 1): "with_vector: only false",
		strings.Replace(vecBody, `"with_vector":false`, `"with_vector":1`, 1):    "with_vector: only false",
		cut + `,"prefetch":[{}],"filter":{"must":[]}}`:                           "filter, prefetch: not supported",
	} {
		rec, out := qdrantDo(t, h, http.MethodPost, qdrantBooks, body)
		if msg := qdrantErrText(out); rec.Code != http.StatusBadRequest || !strings.Contains(msg, want) || !strings.HasSuffix(msg, qdrantDocsHint) {
			t.Errorf("%.60s: %d %q, want %q", body, rec.Code, msg, want)
		}
	}
}

func TestQdrantQueryCollectionAndDimensions(t *testing.T) {
	s, h := qdrantTestServer(t)
	qdrantSeed(t, s)
	body := qdrantVectorBody(t, s, "owl hunting at night", "")
	rec, out := qdrantDo(t, h, http.MethodPost, "/qdrant/collections/missing/points/query", body)
	if rec.Code != http.StatusNotFound || qdrantErrText(out) != "Collection `missing` doesn't exist!" {
		t.Fatalf("missing: %d %v", rec.Code, out)
	}
	// An alias resolves to its collection.
	if err := s.SetAlias("library", "books"); err != nil {
		t.Fatal(err)
	}
	if rec, out = qdrantDo(t, h, http.MethodPost, "/qdrant/collections/library/points/query", body); rec.Code != http.StatusOK || len(qdrantPoints(out)) != 2 {
		t.Fatalf("alias: %d %s", rec.Code, rec.Body.String())
	}
	// A vector from another model: both dimensions named.
	rec, out = qdrantDo(t, h, http.MethodPost, qdrantBooks, `{"query":[0.1,0.2,0.3]}`)
	if msg := qdrantErrText(out); rec.Code != http.StatusBadRequest ||
		!strings.Contains(msg, "query vector has 3 dimensions") || !strings.Contains(msg, "(256 dimensions)") {
		t.Fatalf("dimensions: %d %q", rec.Code, msg)
	}
	// Without a provider the dimension is unknown and the search decides.
	provider := s.Embedding
	s.Embedding = nil
	if err := s.qdrantCheckDimensions("books", 3); err != nil {
		t.Fatalf("no provider: %v", err)
	}
	s.Embedding = provider
	// A refusal from /v1/vector-search keeps its status.
	s.VectorSearchers = map[string]vec.VectorSearcher{}
	rec, out = qdrantDo(t, h, http.MethodPost, qdrantBooks, body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(qdrantErrText(out), "auto") {
		t.Fatalf("upstream: %d %v", rec.Code, out)
	}
}

func TestQdrantQueryUnreadableBody(t *testing.T) {
	s, _ := qdrantTestServer(t)
	rec := httptest.NewRecorder()
	s.qdrantQueryPoints(rec, httptest.NewRequest(http.MethodPost, qdrantBooks, errReader{}), "books", time.Now())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("read error: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, qdrantBooks, strings.NewReader(`{"query":[1,2,3]}`))
	req.Body = http.MaxBytesReader(rec, req.Body, 2)
	s.qdrantQueryPoints(rec, req, "books", time.Now())
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the cap: %d", rec.Code)
	}
}

func TestQdrantPointIDIsStable(t *testing.T) {
	a := qdrantPointID("books", "id1", 0)
	if a != qdrantPointID("books", "id1", 0) || a == qdrantPointID("books", "id1", 1) || a == qdrantPointID("book", "sid1", 0) {
		t.Fatal("ids must be stable and distinct")
	}
	if !uuidV8.MatchString(a) {
		t.Fatalf("not a UUID: %s", a)
	}
	// A result without a chunk index is chunk 0, and without a passage its text is the document.
	p := qdrantPoint("books", &VectorSearchResultItem{Document: storage.Doc{ContentMD: "body"}}, true)
	if p["id"] != qdrantPointID("books", "", 0) || p["payload"].(map[string]any)["text"] != "body" {
		t.Fatalf("no chunk index: %v", p)
	}
}
