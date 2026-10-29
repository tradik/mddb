package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/embedding"
	json "mddb/internal/jsonx"
)

// SRCH-014: the lexical provider runs every vector path with no network and no
// model, and every surface that describes it says it does not encode meaning.

// lexicalServer is a test server embedding with the lexical provider, holding
// three short documents on unrelated subjects.
func lexicalServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	s.VectorIndex.SetReady()
	s.Embedding = embedding.NewLexicalProvider(512)
	s.startEmbeddingWorker(s.Embedding)
	t.Cleanup(s.EmbeddingWorker.Stop)
	for key, text := range map[string]string{
		"replication": "# Replication\n\nA follower replica copies every write from the leader.",
		"backup":      "# Backup\n\nBackups are written to a tarball and restored with one command.",
		"tls":         "# TLS\n\nServe the API over HTTPS with your own certificate and key.",
	} {
		doc := addTestDoc(t, s, "docs", key, "en", text, nil)
		waitForEmbedding(t, s, "docs", doc.ID)
	}
	return s
}

// lexicalTopKey posts body to handler and returns the key of the first result.
func lexicalTopKey(t *testing.T, handler http.HandlerFunc, body string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			Document struct {
				Key string `json:"key"`
			} `json:"document"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Results) == 0 {
		t.Fatalf("no results (%v): %s", err, rec.Body.String())
	}
	return resp.Results[0].Document.Key
}

func TestLexicalVectorPathsWorkWithoutAModel(t *testing.T) {
	s := lexicalServer(t)
	cases := map[string]struct {
		handler http.HandlerFunc
		body    string
	}{
		// "replicaton" and "folower" are typos: only shared n-grams find it.
		"vector":       {s.handleVectorSearch, `{"collection":"docs","query":"replicaton folower","topK":3}`},
		"vector graph": {s.handleVectorSearch, `{"collection":"docs","query":"replicaton folower","topK":3,"retrievalMode":"graph"}`},
		"vector mmr":   {s.handleVectorSearch, `{"collection":"docs","query":"replicaton folower","topK":3,"mmr":true}`},
		"vector hnsw":  {s.handleVectorSearch, `{"collection":"docs","query":"replicaton folower","topK":3,"algorithm":"hnsw"}`},
		"hybrid":       {s.handleHybridSearch, `{"collection":"docs","query":"replica leader","topK":3}`},
		"cross":        {s.handleCrossSearch, `{"query":"replicaton folower","targetCollections":["docs"],"topK":3}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := lexicalTopKey(t, c.handler, c.body); got != "replication" {
				t.Errorf("top result %q, want replication", got)
			}
		})
	}
}

func TestLexicalAdvisorRecommendsKeywordSearch(t *testing.T) {
	s := lexicalServer(t)
	rec, err := s.RecommendSearch("docs")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Profile.Semantic == nil || *rec.Profile.Semantic {
		t.Fatalf("profile semantic = %v, want false", rec.Profile.Semantic)
	}
	if rec.SearchType != "fts" || rec.VectorAlgorithm != "" || rec.HybridStrategy != "" {
		t.Errorf("recommendation = %s/%q/%q, want plain fts", rec.SearchType, rec.VectorAlgorithm, rec.HybridStrategy)
	}
	if rec.Reasons[0] != lexicalAdvice {
		t.Errorf("first reason = %q", rec.Reasons[0])
	}
}

func TestAdvisorReportsSemanticForAModel(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	defer s.EmbeddingWorker.Stop()
	doc := addTestDoc(t, s, "notes", "a", "en", "some words here", nil)
	waitForEmbedding(t, s, "notes", doc.ID)

	rec, err := s.RecommendSearch("notes")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Profile.Semantic == nil || !*rec.Profile.Semantic || rec.SearchType != "hybrid" {
		t.Errorf("semantic %v, search %s; want true, hybrid", rec.Profile.Semantic, rec.SearchType)
	}
}

func TestConfigReportsLexicalAsNotSemantic(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_EMBEDDING_PROVIDER", embedding.ProviderLexical)
	t.Setenv("MDDB_EMBEDDING_DIMENSIONS", "1024")

	vc := s.vectorConfig()
	if vc == nil || vc.Semantic || vc.Model != embedding.LexicalModel || vc.Dimensions != 1024 || vc.APIURL != "" {
		t.Fatalf("vectorConfig = %+v", vc)
	}

	rec := httptest.NewRecorder()
	s.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	if !strings.Contains(rec.Body.String(), `"semantic":false`) {
		t.Errorf("/config does not say semantic:false: %s", rec.Body.String())
	}
}

func TestConfigReportsModelsAsSemantic(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_EMBEDDING_PROVIDER", "ollama")
	if vc := s.vectorConfig(); vc == nil || !vc.Semantic {
		t.Errorf("ollama vectorConfig = %+v, want semantic", vc)
	}

	t.Setenv("MDDB_EMBEDDING_PROVIDER", "")
	if vc := s.vectorConfig(); vc != nil {
		t.Errorf("nothing configured, got %+v", vc)
	}
	s.DetectedEmbedding = &embedding.DetectedProvider{Name: "ollama", Model: "nomic-embed-text", Dimensions: 768}
	if vc := s.vectorConfig(); vc == nil || !vc.Semantic || vc.Source != "autodetected" {
		t.Errorf("autodetected vectorConfig = %+v", vc)
	}
}

func TestStoredLexicalConfig(t *testing.T) {
	s, cleanup := newTestServerForEmbeddingConfig(t)
	defer cleanup()

	payload := `{"id":"lex","name":"Lexical","provider":"lexical","model":"lexical-ngram-v1","dimensions":768}`
	rec := httptest.NewRecorder()
	s.handleCreateEmbeddingConfig(rec, httptest.NewRequest(http.MethodPost, "/v1/embedding-configs", strings.NewReader(payload)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	cfg, err := s.GetEmbeddingConfig("lex")
	if err != nil {
		t.Fatal(err)
	}
	s.InitializeEmbeddingFromConfig(cfg)
	defer s.EmbeddingWorker.Stop()
	if s.Embedding == nil || s.Embedding.Dimensions() != 768 || embedding.IsSemantic(s.Embedding) {
		t.Errorf("stored lexical config built %v", s.Embedding)
	}

	rec = httptest.NewRecorder()
	s.handleCreateEmbeddingConfig(rec, httptest.NewRequest(http.MethodPost, "/v1/embedding-configs",
		strings.NewReader(`{"id":"x","name":"X","provider":"hashing","model":"m","dimensions":8}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "'lexical'") {
		t.Errorf("unknown provider: %d %s", rec.Code, rec.Body.String())
	}
}
