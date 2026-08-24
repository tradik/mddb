package main

import (
	"context"
	"strings"
	"testing"

	proto "mddb/proto"
)

// SRCH-010. The advisor's job is to answer a question an agent cannot answer
// for itself, so the tests check that it gives *different* answers to
// different collections — an advisor that always says the same thing is a
// default with extra steps.

func reasonsMention(rec *SearchRecommendation, substr string) bool {
	for _, r := range rec.Reasons {
		if strings.Contains(strings.ToLower(r), strings.ToLower(substr)) {
			return true
		}
	}
	return false
}

func seedAdvisor(t *testing.T, srv *Server, collection string, docs []*proto.BatchDocument) {
	t.Helper()
	resp, err := NewBatchProcessor(srv, 4).ProcessBatch(context.Background(), collection, docs)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failed > 0 {
		t.Fatalf("seeding failed: %v", resp.Errors)
	}
}

func TestAdvisorRecommendsKeywordSearchWithoutEmbeddings(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	seedAdvisor(t, srv, "prose", []*proto.BatchDocument{
		makeBatchDoc("a.md", "en", "a note about certificate rotation and the proxy reload that follows", nil, false),
		makeBatchDoc("b.md", "en", "another note about draining a node before maintenance begins", nil, false),
	})

	rec, err := srv.RecommendSearch("prose")
	if err != nil {
		t.Fatal(err)
	}

	if rec.SearchType != "fts" {
		t.Errorf("searchType = %q with nothing embedded, want fts", rec.SearchType)
	}
	if rec.VectorAlgorithm != "" {
		t.Errorf("a vector algorithm was recommended with no vectors: %q", rec.VectorAlgorithm)
	}
	if !reasonsMention(rec, "no embedding provider") && !reasonsMention(rec, "keyword search is the only") {
		t.Errorf("the reason does not explain why vector search is unavailable: %v", rec.Reasons)
	}
	if len(rec.Reasons) == 0 {
		t.Error("a recommendation arrived with no reasons")
	}
}

// Code collections should get different advice from prose ones — this is the
// whole point of measuring rather than defaulting.
func TestAdvisorRecognisesCode(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	docs := []*proto.BatchDocument{
		makeBatchDoc("css/site.css", "en", ".cart { padding: 1rem; }\n.total { font-weight: 700; }\n",
			map[string]*proto.MetaValues{"language": {Values: []string{"css"}}}, false),
		makeBatchDoc("js/cart.js", "en", "export function total(items) { return items.length; }\n",
			map[string]*proto.MetaValues{"language": {Values: []string{"javascript"}}}, false),
		makeBatchDoc("templates/cart.html", "en", "<div class=\"cart\"><span class=\"total\"></span></div>\n",
			map[string]*proto.MetaValues{"language": {Values: []string{"html"}}}, false),
	}
	seedAdvisor(t, srv, "theme", docs)

	rec, err := srv.RecommendSearch("theme")
	if err != nil {
		t.Fatal(err)
	}

	if rec.Profile.CodeDocuments == 0 {
		t.Fatal("no document was recognised as code despite extracted symbols")
	}
	if rec.RetrievalMode != "graph" {
		t.Errorf("retrievalMode = %q for a code collection, want graph", rec.RetrievalMode)
	}
	if rec.FTSAlgorithm != "bm25" {
		t.Errorf("ftsAlgorithm = %q for code, want bm25", rec.FTSAlgorithm)
	}
	if !reasonsMention(rec, "code") {
		t.Errorf("nothing in the reasons mentions that this is code: %v", rec.Reasons)
	}
}

func TestAdvisorRecommendsChunkingForLongDocuments(t *testing.T) {
	long := strings.Repeat("a sentence of about eight words in it ", 120) // ~960 words

	profile := &CollectionProfile{
		Collection: "manuals", Documents: 500, Sampled: 500,
		MedianWords: 960, LongDocumentRatio: 0.9,
		DistinctTerms: 4000, TermsPerDocument: 8, TypeTokenRatio: 0.3,
	}
	_ = long

	rec := buildRecommendation(profile, false)

	if rec.RetrievalMode != "chunk" {
		t.Errorf("retrievalMode = %q for long documents, want chunk", rec.RetrievalMode)
	}
	if rec.TopK != 5 {
		t.Errorf("topK = %d for long documents, want fewer", rec.TopK)
	}
	if !reasonsMention(rec, "long") && !reasonsMention(rec, "over 500 words") {
		t.Errorf("the reasons do not mention document length: %v", rec.Reasons)
	}
}

func TestAdvisorRecommendsQuantizationWhenVectorsAreLarge(t *testing.T) {
	cases := map[string]struct {
		embedded int
		dims     int
		want     string
	}{
		// The bands: under 256 MB of float32 vectors an exact scan is fine;
		// above that int8 is effectively lossless; above a gigabyte, halving
		// again matters more than the 0.5% of recall it costs.
		"small collection":   {1000, 384, "flat"},
		"many small vectors": {80000, 384, "hnsw"},
		"half a gigabyte":    {200000, 768, "sq"},
		"over a gigabyte":    {400000, 1536, "sq4"},
	}

	for name, c := range cases {
		p := &CollectionProfile{
			Collection: "vectors", Documents: c.embedded, Sampled: 100,
			EmbeddedDocuments: c.embedded, VectorDimensions: c.dims,
			EstimatedVectorBytes: int64(c.embedded) * int64(c.dims) * 4,
			MedianWords:          200, DistinctTerms: 5000, TermsPerDocument: 8, TypeTokenRatio: 0.3,
		}
		rec := buildRecommendation(p, true)
		if rec.VectorAlgorithm != c.want {
			t.Errorf("%s (%d vectors × %d dims = %.0f MB): recommended %q, want %q",
				name, c.embedded, c.dims,
				float64(p.EstimatedVectorBytes)/(1<<20), rec.VectorAlgorithm, c.want)
		}
	}
}

func TestAdvisorWarnsAboutPartialEmbedding(t *testing.T) {
	// Half the collection embedded is the state a reindex leaves behind when
	// it fails halfway, and the failure is invisible from a search.
	p := &CollectionProfile{
		Collection: "half", Documents: 1000, Sampled: 1000,
		EmbeddedDocuments: 400, VectorDimensions: 384,
		MedianWords: 200, DistinctTerms: 5000, TermsPerDocument: 8, TypeTokenRatio: 0.3,
	}

	rec := buildRecommendation(p, true)

	if len(rec.Warnings) == 0 {
		t.Fatal("a half-embedded collection produced no warning")
	}
	if !strings.Contains(strings.ToLower(strings.Join(rec.Warnings, " ")), "embedded") {
		t.Errorf("the warning does not name the problem: %v", rec.Warnings)
	}
	if rec.SearchType != "hybrid" {
		t.Errorf("searchType = %q; hybrid keeps the unembedded documents reachable", rec.SearchType)
	}
}

func TestAdvisorTiltsAlphaByContent(t *testing.T) {
	code := buildRecommendation(&CollectionProfile{
		Documents: 500, Sampled: 500, CodeDocuments: 400,
		EmbeddedDocuments: 500, VectorDimensions: 384,
		MedianWords: 80, DistinctTerms: 3000, TermsPerDocument: 8, TypeTokenRatio: 0.4,
	}, true)

	prose := buildRecommendation(&CollectionProfile{
		Documents: 500, Sampled: 500,
		EmbeddedDocuments: 500, VectorDimensions: 384,
		MedianWords: 600, DistinctTerms: 8000, TermsPerDocument: 8, TypeTokenRatio: 0.3,
	}, true)

	if code.HybridAlpha >= prose.HybridAlpha {
		t.Errorf("code alpha %.2f is not below prose alpha %.2f; identifiers should favour keywords",
			code.HybridAlpha, prose.HybridAlpha)
	}
}

func TestAdvisorProducesAStorableProfile(t *testing.T) {
	rec := buildRecommendation(&CollectionProfile{
		Documents: 500, Sampled: 500,
		EmbeddedDocuments: 500, VectorDimensions: 384,
		MedianWords: 200, DistinctTerms: 5000, TermsPerDocument: 8, TypeTokenRatio: 0.3,
	}, true)

	if rec.RetrievalProfile == nil {
		t.Fatal("no retrieval profile was produced")
	}
	p := rec.RetrievalProfile
	if p.DefaultSearchType != rec.SearchType {
		t.Errorf("profile searchType %q disagrees with the recommendation %q", p.DefaultSearchType, rec.SearchType)
	}
	if p.TopK != rec.TopK || p.RetrievalMode != rec.RetrievalMode {
		t.Errorf("profile disagrees with the recommendation: %+v", p)
	}
	// An explicit alpha must be marked as set, or a 0.5 would be read as
	// "unspecified" by the precedence rule.
	if p.HybridStrategy != "" && !p.HasHybridAlpha() {
		t.Error("the profile carries an alpha that will be read as unset")
	}
}

func TestAdvisorOnAnEmptyCollection(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	rec, err := srv.RecommendSearch("nothing-here")
	if err != nil {
		t.Fatalf("an empty collection returned an error: %v", err)
	}
	if rec.Profile.Documents != 0 {
		t.Errorf("documents = %d", rec.Profile.Documents)
	}
	if len(rec.Reasons) == 0 {
		t.Error("an empty collection produced no explanation")
	}
	if !reasonsMention(rec, "empty") {
		t.Errorf("the reason does not say the collection is empty: %v", rec.Reasons)
	}
}

func TestAdvisorRejectsAMissingCollectionName(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	if _, err := srv.RecommendSearch(""); err == nil {
		t.Error("an empty collection name was accepted")
	}
}

func TestAdvisorApplyStoresTheProfile(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	seedAdvisor(t, srv, "apply", []*proto.BatchDocument{
		makeBatchDoc("a.md", "en", "a short note about certificates", nil, false),
	})

	// A response prompt that has nothing to do with retrieval must survive
	// the write: rebuilding the config from a partial view is how those get
	// silently cleared.
	if err := srv.CollectionManager.Set("apply", &CollectionConfig{
		Type: "default", ResponsePrompt: "Answer in numbered steps.",
	}); err != nil {
		t.Fatal(err)
	}

	rec, err := srv.RecommendSearch("apply")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.applyRecommendation("apply", rec); err != nil {
		t.Fatal(err)
	}

	cfg, found := srv.CollectionManager.Get("apply")
	if !found || cfg == nil {
		t.Fatal("the configuration disappeared")
	}
	if cfg.Retrieval == nil {
		t.Fatal("the retrieval profile was not stored")
	}
	if cfg.Retrieval.DefaultSearchType != rec.SearchType {
		t.Errorf("stored searchType %q, recommended %q", cfg.Retrieval.DefaultSearchType, rec.SearchType)
	}
	if cfg.ResponsePrompt != "Answer in numbered steps." {
		t.Errorf("applying the recommendation cleared the response prompt: %q", cfg.ResponsePrompt)
	}
}

// Every recommendation must explain itself. An agent handed "use bm25" cannot
// tell a measured answer from a coin flip.
func TestEveryRecommendationCarriesReasons(t *testing.T) {
	profiles := map[string]*CollectionProfile{
		"code":       {Documents: 100, Sampled: 100, CodeDocuments: 90, MedianWords: 50, DistinctTerms: 900, TermsPerDocument: 8, TypeTokenRatio: 0.4},
		"prose":      {Documents: 100, Sampled: 100, MedianWords: 700, DistinctTerms: 9000, TermsPerDocument: 8, TypeTokenRatio: 0.3},
		"repetitive": {Documents: 100, Sampled: 100, MedianWords: 300, DistinctTerms: 200, TermsPerDocument: 8, TypeTokenRatio: 0.02},
		"embedded":   {Documents: 100, Sampled: 100, EmbeddedDocuments: 100, VectorDimensions: 384, MedianWords: 200, DistinctTerms: 3000, TermsPerDocument: 8, TypeTokenRatio: 0.3},
	}

	for name, p := range profiles {
		rec := buildRecommendation(p, p.EmbeddedDocuments > 0)
		if len(rec.Reasons) < 3 {
			t.Errorf("%s: only %d reasons for %d decisions", name, len(rec.Reasons), 4)
		}
		if rec.SearchType == "" {
			t.Errorf("%s: no search type recommended", name)
		}
		if rec.FTSAlgorithm == "" {
			t.Errorf("%s: no ranking algorithm recommended", name)
		}
		for _, reason := range rec.Reasons {
			if !strings.Contains(reason, ":") && !strings.Contains(reason, ",") {
				t.Errorf("%s: reason reads as a bare instruction: %q", name, reason)
			}
		}
	}
}

func TestAdvisorPicksQueryExpansionForRepetitiveText(t *testing.T) {
	// 150 distinct terms across 1000 documents: each contributes 0.15 new
	// words, so the documents are near-identical to an exact-match ranker.
	rec := buildRecommendation(&CollectionProfile{
		Documents: 1000, Sampled: 1000,
		MedianWords: 300, DistinctTerms: 150, TermsPerDocument: 0.15, TypeTokenRatio: 0.01,
	}, false)

	if rec.FTSAlgorithm != "pmisparse" {
		t.Errorf("ftsAlgorithm = %q for a repetitive vocabulary, want pmisparse", rec.FTSAlgorithm)
	}
	if !reasonsMention(rec, "vocabulary") {
		t.Errorf("the reasons do not mention the vocabulary: %v", rec.Reasons)
	}
}
