package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/internal/rerank"
)

// RAG-011: a reranking model is configured where embedding models are — the
// environment, overridden by a stored default of kind "rerank" — and its key
// never comes back out.

func TestRerankConfig_EnvAndStoredDefault(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	f := newFakeReranker(t)

	// Nothing configured: off.
	t.Setenv("MDDB_RERANK_PROVIDER", "")
	s.initRerank()
	if s.rerankConfigured() != nil {
		t.Fatal("reranking on with nothing configured")
	}
	// The environment.
	t.Setenv("MDDB_RERANK_PROVIDER", "http")
	t.Setenv("MDDB_RERANK_API_URL", f.URL)
	t.Setenv("MDDB_RERANK_MODEL", "env-model")
	t.Setenv("MDDB_RERANK_MAX_CANDIDATES", "20")
	s.initRerank()
	if got := s.rerankConfigured(); got == nil || got.provider.Model() != "env-model" || got.maxCandidates != 20 || got.source != "environment" {
		t.Fatalf("env: %+v", got)
	}

	// A stored rerank configuration, created and made the default over REST.
	create := covEmbDo(s.handleEmbeddingConfigs, http.MethodPost, "/v1/embedding-configs",
		`{"id":"rr1","name":"Reranker","kind":"rerank","provider":"http","model":"stored-model","apiUrl":"`+f.URL+`","apiKey":"secret","timeoutMs":500,"maxCandidates":10}`)
	if create.Code != http.StatusCreated || strings.Contains(create.Body.String(), "secret") {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	if rec := covEmbDo(s.handleSetDefaultEmbeddingConfig, http.MethodPost, "/v1/embedding-configs/set-default", `{"id":"rr1"}`); rec.Code != http.StatusOK {
		t.Fatalf("set default: %d %s", rec.Code, rec.Body.String())
	}
	if got := s.rerankConfigured(); got.provider.Model() != "stored-model" || got.maxCandidates != 10 || got.source != "stored config" {
		t.Fatalf("stored: %+v", got)
	}
	// It wins over the environment at the next start, too.
	s.initRerank()
	if got := s.rerankConfigured(); got.provider.Model() != "stored-model" {
		t.Fatalf("restart: %+v", got)
	}
	// The embedding default is untouched by a rerank default, and lists apart.
	if _, err := s.GetDefaultEmbeddingConfig(); err == nil {
		t.Fatal("a rerank configuration became the embedding default")
	}
	list := func(query string) []map[string]any {
		rec := covEmbDo(s.handleEmbeddingConfigs, http.MethodGet, "/v1/embedding-configs"+query, "")
		var out struct {
			Configs []map[string]any `json:"configs"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Configs
	}
	if got := list(""); len(got) != 0 {
		t.Fatalf("embedding list shows rerank configs: %v", got)
	}
	if got := list("?kind=embedding"); len(got) != 0 {
		t.Fatalf("kind=embedding: %v", got)
	}
	got := list("?kind=rerank")
	if len(got) != 1 || got[0]["apiKey"] != "" || got[0]["kind"] != "rerank" {
		t.Fatalf("rerank list: %v", got)
	}
	// Read one: no key.
	if rec := covEmbDo(s.handleEmbeddingConfigDetail, http.MethodGet, "/v1/embedding-configs/rr1", ""); strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("get: %s", rec.Body.String())
	}
	// An update without a key keeps the stored one.
	rec := covEmbDo(s.handleEmbeddingConfigDetail, http.MethodPut, "/v1/embedding-configs/rr1",
		`{"name":"Reranker","kind":"rerank","provider":"http","model":"stored-model-2","apiUrl":"`+f.URL+`","isDefault":true}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	if stored, _ := s.GetEmbeddingConfig("rr1"); stored.APIKey != "secret" || stored.Model != "stored-model-2" {
		t.Fatalf("key kept: %+v", stored)
	}
	// An embedding default alongside it: one default per kind.
	if err := s.SaveEmbeddingConfig(&EmbeddingConfig{ID: "e1", Name: "E", Provider: "lexical", Model: "x", Dimensions: 64, IsDefault: true}); err != nil {
		t.Fatal(err)
	}
	if def, err := s.defaultModelConfig(ConfigKindRerank); err != nil || def.ID != "rr1" {
		t.Fatalf("rerank default after an embedding default: %+v %v", def, err)
	}
	if def, err := s.GetDefaultEmbeddingConfig(); err != nil || def.ID != "e1" {
		t.Fatalf("embedding default: %+v %v", def, err)
	}
}

func TestRerankConfig_Validation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	for _, body := range []string{
		`{"id":"a","name":"A","kind":"rerank","provider":"openai","model":"m","apiUrl":"http://127.0.0.1:1"}`,
		`{"id":"a","name":"A","kind":"rerank","provider":"http","model":"m"}`,
		`{"id":"a","name":"A","kind":"sorting","provider":"http","model":"m","apiUrl":"http://127.0.0.1:1"}`,
		`{"id":"a","name":"A","kind":"embedding","provider":"http","model":"m","dimensions":3}`,
		`{"id":"a","name":"A","provider":"openai","model":"m"}`,
	} {
		if rec := covEmbDo(s.handleEmbeddingConfigs, http.MethodPost, "/v1/embedding-configs", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
		if rec := covEmbDo(s.handleEmbeddingConfigDetail, http.MethodPut, "/v1/embedding-configs/a", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("update %s: %d", body, rec.Code)
		}
	}
	// An embedding configuration still validates as it did.
	ok := covEmbDo(s.handleEmbeddingConfigs, http.MethodPost, "/v1/embedding-configs",
		`{"id":"e","name":"E","kind":"embedding","provider":"lexical","model":"m","dimensions":64}`)
	if ok.Code != http.StatusCreated || strings.Contains(ok.Body.String(), `"kind"`) {
		t.Fatalf("embedding: %d %s", ok.Code, ok.Body.String())
	}
	// A stored configuration naming a URL nothing answers on still installs;
	// searches report the failure per call.
	s.InitializeRerankFromConfig(&EmbeddingConfig{Kind: ConfigKindRerank, Provider: "http", APIURL: "ftp://x"})
	if s.rerankConfigured() != nil {
		t.Fatal("an unusable configuration was installed")
	}
	// keepStoredRerankKey: a missing configuration, and an embedding one, are left alone.
	cfg := &EmbeddingConfig{ID: "missing", Kind: ConfigKindRerank}
	s.keepStoredRerankKey(cfg)
	e := &EmbeddingConfig{ID: "e", APIKey: ""}
	s.keepStoredRerankKey(e)
	if cfg.APIKey != "" || e.APIKey != "" {
		t.Fatal("a key appeared")
	}
}

// The stage's arithmetic and its no-op forms.
func TestRerankStage_Units(t *testing.T) {
	var none *rerankRun
	if none.pool(5) != 5 || none.cut(5) != 5 || none.restore(3, 5) != 3 || none.Result() != (RerankResult{}) ||
		none.textFor("c", nil, "x", nil) != "" || len(rerankTrim(none, []int{1, 2, 3}, 1)) != 3 {
		t.Fatal("nil run")
	}
	r := &rerankRun{setup: &rerankSetup{}, candidates: 20, topN: 2}
	if r.pool(5) != 20 || r.pool(30) != 30 || r.cut(5) != 2 || r.cut(1) != 1 || r.restore(9, 5) != 2 {
		t.Fatal("active run")
	}
	r.topN = 0
	if r.cut(7) != 7 || len(rerankTrim(r, []int{1, 2, 3}, 2)) != 2 {
		t.Fatal("topN 0")
	}
	// An answer that leaves candidates out keeps them, in their old order,
	// after the scored ones; items beyond the pool follow.
	got := reorder([]string{"a", "b", "c", "d"}, 3, []rerank.Scored{{Index: 2, Score: 1}})
	if strings.Join(got, "") != "cabd" {
		t.Fatalf("reorder: %v", got)
	}
	long := strings.Repeat("é", maxRerankChars+10)
	if out := r.textFor("c", nil, long, nil); len([]rune(out)) != maxRerankChars {
		t.Fatalf("truncate: %d", len([]rune(out)))
	}
	if (&RerankOptions{}).Validate() != nil || (*RerankOptions)(nil).Validate() != nil {
		t.Fatal("empty options")
	}
	if rerankOptionsFromProto(nil) != nil || rerankOptionsToProto(nil) != nil || rerankReportToProto(nil) != nil {
		t.Fatal("nil conversions")
	}
	// A search with nothing to rerank makes no call.
	s, f := rerankServer(t)
	run := s.newRerankRun("rr", "q", &RerankOptions{Enabled: boolPtrRR(true)}, false)
	if out := rerankItems(context.Background(), s, run, []int{}, rerankAccess[int]{}); len(out) != 0 || f.calls.Load() != 0 {
		t.Fatal("empty list")
	}
}

func boolPtrRR(b bool) *bool { return &b }
