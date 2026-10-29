package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/fts"
	"mddb/internal/geo"
	"mddb/internal/storage"
	"mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// covGFailEmbedding refuses every request, like a provider that is down.
type covGFailEmbedding struct{ *mockEmbedding }

func (covGFailEmbedding) Embed(context.Context, string, embedding.Role) ([]float32, error) {
	return nil, errors.New("provider down")
}

// covGVectorServer is covGServer with a ready flat index, a mock provider
// and a running embedding worker.
func covGVectorServer(t testing.TB) *Server {
	t.Helper()
	s := covGServer(t)
	s.VectorIndex.SetReady()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	t.Cleanup(s.EmbeddingWorker.Stop)
	return s
}

// covGEmbedded adds a document and waits until its vector is searchable.
func covGEmbedded(t testing.TB, s *Server, coll, key, content string, meta map[string][]string) storage.Doc {
	t.Helper()
	d := addTestDoc(t, s, coll, key, "en", content, meta)
	waitForEmbedding(t, s, coll, d.ID)
	return d
}

// covGHybridServer seeds a geo-aware, embedded blog collection.
func covGHybridServer(t *testing.T) (*Server, []storage.Doc) {
	t.Helper()
	s := covGVectorServer(t)
	s.GeoStore = geo.NewGeoStore(s.DB)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	s.GeoIndex = geo.NewGeoIndex()
	s.GeoIndex.SetReady()
	docs := []storage.Doc{
		covGEmbedded(t, s, "blog", "berlin", "golang meetup in berlin",
			map[string][]string{"tag": {"go"}, "geo_lat": {"52.52"}, "geo_lng": {"13.405"}}),
		covGEmbedded(t, s, "blog", "potsdam", "golang workshop near potsdam",
			map[string][]string{"tag": {"go"}, "geo_lat": {"52.39"}, "geo_lng": {"13.06"}}),
		covGEmbedded(t, s, "blog", "paris", "golang conference in paris",
			map[string][]string{"tag": {"rust"}, "geo_lat": {"48.85"}, "geo_lng": {"2.35"}}),
	}
	return s, docs
}

func covGHybrid(t *testing.T, s *Server, body any) (HybridSearchResponse, *httptest.ResponseRecorder) {
	t.Helper()
	rec := doRequest(t, s.handleHybridSearch, body)
	var resp HybridSearchResponse
	if rec.Code == http.StatusOK {
		covGDecode(t, rec, &resp)
	}
	return resp, rec
}

func TestCovGHybridValidation(t *testing.T) {
	s := covGServer(t)
	covGWantCode(t, covGRaw(s.handleHybridSearch, http.MethodPost, "/", "{"), http.StatusBadRequest)
	for name, tc := range map[string]struct {
		body map[string]any
		code int
	}{
		"missing query":       {map[string]any{"collection": "c"}, http.StatusBadRequest},
		"oversample too big":  {map[string]any{"collection": "c", "query": "q", "oversample": 50}, http.StatusUnprocessableEntity},
		"unknown strategy":    {map[string]any{"collection": "c", "query": "q", "strategy": "vote"}, http.StatusBadRequest},
		"distance needs geo":  {map[string]any{"collection": "c", "query": "q", "sort": "distance"}, http.StatusBadRequest},
		"unknown sort":        {map[string]any{"collection": "c", "query": "q", "sort": "random"}, http.StatusBadRequest},
		"geo index not ready": {map[string]any{"collection": "c", "query": "q", "geo": map[string]any{"lat": 1, "lng": 1, "radiusMeters": 5}}, http.StatusBadRequest},
	} {
		_, rec := covGHybrid(t, s, tc.body)
		if rec.Code != tc.code {
			t.Errorf("%s: status %d, want %d (%s)", name, rec.Code, tc.code, rec.Body.String())
		}
	}

	s.GeoIndex = geo.NewGeoIndex()
	s.GeoIndex.SetReady()
	_, rec := covGHybrid(t, s, map[string]any{"collection": "c", "query": "q",
		"geo": map[string]any{"lat": 200, "lng": 1, "radiusMeters": 5}})
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid geo filter") {
		t.Errorf("want invalid geo filter: %s", rec.Body.String())
	}
}

func TestCovGHybridAlphaAndClamping(t *testing.T) {
	s, _ := covGHybridServer(t)
	for _, tc := range []struct {
		alpha, want float64
	}{{-1, 0}, {0, 0}, {0.25, 0.25}, {7, 1}} {
		resp, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang",
			"alpha": tc.alpha, "fuzzy": 5, "includeContent": true})
		covGWantCode(t, rec, http.StatusOK)
		if resp.Alpha != tc.want {
			t.Errorf("alpha %v: response alpha %v, want %v", tc.alpha, resp.Alpha, tc.want)
		}
		if resp.Total != 3 || resp.Results[0].Document.ContentMD == "" {
			t.Errorf("alpha %v: total %d, content kept %v", tc.alpha, resp.Total, resp.Total > 0 && resp.Results[0].Document.ContentMD != "")
		}
	}
	resp, _ := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "fuzzy": -2})
	if resp.Alpha != 0.5 || resp.Results[0].Document.ContentMD != "" {
		t.Errorf("default alpha %v, content should be stripped", resp.Alpha)
	}
}

func TestCovGHybridStrategies(t *testing.T) {
	s, _ := covGHybridServer(t)
	resp, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "strategy": "rrf", "topK": 2})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Strategy != "rrf" || resp.RRFK != 60 || resp.Alpha != 0 || resp.Total != 2 {
		t.Errorf("rrf: %+v", resp)
	}

	resp, _ = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "strategy": "weighted",
		"alpha": 0.4, "signals": map[string]any{"diversity": 0.6}, "facetBy": []string{"tag"}})
	if resp.Strategy != "weighted" || resp.Alpha != 0.4 || resp.Signals.Diversity != 0.6 {
		t.Errorf("weighted: %+v", resp)
	}
	if len(resp.SignalBreakdown) != resp.Total {
		t.Errorf("one breakdown per result: %d vs %d", len(resp.SignalBreakdown), resp.Total)
	}
	if got := resp.Facets["tag"]; len(got) != 2 {
		t.Errorf("facets over results = %+v", resp.Facets)
	}

	// Weighted with no signal: nothing to explain.
	resp, _ = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "strategy": "weighted"})
	if resp.SignalBreakdown != nil {
		t.Errorf("no signals, no breakdown: %+v", resp.SignalBreakdown)
	}
}

func TestCovGHybridGeo(t *testing.T) {
	s, docs := covGHybridServer(t)
	berlin := map[string]any{"lat": 52.52, "lng": 13.405, "radiusMeters": 60000}

	resp, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "geo": berlin, "sort": "distance"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 2 {
		t.Fatalf("Paris is outside 60 km of Berlin; got %d results", resp.Total)
	}
	if resp.Results[0].Document.ID != docs[0].ID || resp.Results[0].DistanceMeters > resp.Results[1].DistanceMeters {
		t.Errorf("distance sort: %+v", resp.Results)
	}
	if resp.Results[1].DistanceMeters == 0 {
		t.Error("distanceMeters should be attached")
	}

	// topK smaller than the geo set is honoured after the spatial pass.
	resp, _ = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "geo": berlin, "topK": 1})
	if resp.Total != 1 || resp.Results[0].Rank != 1 {
		t.Errorf("geo topK 1: %+v", resp.Results)
	}

	// Nothing in the radius answers empty, without searching.
	resp, rec = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang",
		"geo": map[string]any{"lat": -33.86, "lng": 151.2, "radiusMeters": 1000}})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 0 || resp.Results == nil || resp.DistanceMetric != "cosine" {
		t.Errorf("empty geo: %+v", resp)
	}
}

func TestCovGHybridFilterMetaAndAlgorithms(t *testing.T) {
	s, docs := covGHybridServer(t)
	for _, algo := range []string{"bm25", "bm25f", "pmisparse", "tfidf"} {
		for _, fuzzy := range []int{0, 1} {
			resp, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "algorithm": algo,
				"fuzzy": fuzzy, "filterMeta": map[string][]string{"tag": {"rust"}}, "vectorAlgorithm": "nonsense",
				"distanceMetric": "dot_product"})
			covGWantCode(t, rec, http.StatusOK)
			if resp.Total != 1 || resp.Results[0].Document.ID != docs[2].ID {
				t.Errorf("%s/%d: filter should keep only paris, got %+v", algo, fuzzy, resp.Results)
			}
			if resp.DistanceMetric != "dot_product" {
				t.Errorf("metric echo = %q", resp.DistanceMetric)
			}
		}
	}
	// A filter no document satisfies leaves both halves empty.
	resp, _ := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang",
		"filterMeta": map[string][]string{"tag": {"cobol"}}})
	if resp.Total != 0 {
		t.Errorf("unmatched filter returned %d", resp.Total)
	}
}

func TestCovGHybridKeywordOnlyAndVectorFailures(t *testing.T) {
	s := covGServer(t)
	addTestDoc(t, s, "blog", "a", "en", "golang tips", nil)

	// No provider: the keyword half answers alone.
	resp, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 1 || resp.Results[0].VectorScore != 0 {
		t.Errorf("keyword-only: %+v", resp.Results)
	}

	// A provider whose index is not ready also falls back to keywords.
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	resp, _ = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang"})
	if resp.Total != 1 {
		t.Errorf("index not ready should still answer by keyword, got %d", resp.Total)
	}

	// A failing provider is an error, not a silent half-answer.
	s.VectorIndex.SetReady()
	s.Embedding = covGFailEmbedding{&mockEmbedding{dims: 3, model: "m"}}
	_, rec = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang"})
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "vector search failed") {
		t.Errorf("body: %s", rec.Body.String())
	}

	// Neither half available: an empty, well-formed answer.
	s.Embedding = nil
	s.FTSIndex = nil
	resp, rec = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 0 || resp.Stats != nil { // stats need the tokenizer
		t.Errorf("no halves: %+v", resp)
	}
}

func TestCovGHybridCurationBudgetAndStats(t *testing.T) {
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s, docs := covGHybridServer(t)
	cm := NewCurationManager(s.DB)
	if err := cm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := cm.Set(&CurationRule{ID: "p", Collection: "blog", Query: "golang", Enabled: true,
		Pins: []PinnedDoc{{Key: "paris", Lang: "en", Position: 1}}}); err != nil {
		t.Fatal(err)
	}
	s.CurationManager = cm
	resp, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "topK": 2})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 2 || resp.Results[0].Document.ID != docs[2].ID || resp.Stats != nil {
		t.Errorf("curation+stats off: %+v", resp)
	}

	covGSetConfig(t, s, "blog", &CollectionConfig{Retrieval: &RetrievalProfileDef{ContextTokenBudget: 1}})
	resp, _ = covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang", "includeContent": true})
	if !resp.ContextTruncated || resp.Total != 1 {
		t.Errorf("budget: truncated %v total %d", resp.ContextTruncated, resp.Total)
	}
}

func TestCovGHybridMergeHelpers(t *testing.T) {
	// Equal positive scores normalise to 1, equal zero scores to 0.
	got := mergeAlpha([]fts.FTSResult{{DocID: "a", Score: 2}, {DocID: "b", Score: 2}}, nil, 0, 0)
	for _, it := range got {
		if it.FTSScore != 1 {
			t.Errorf("equal positive scores should normalise to 1, got %v", it.FTSScore)
		}
	}
	got = mergeAlpha([]fts.FTSResult{{DocID: "a", Score: 0}}, nil, 0, 0)
	if got[0].FTSScore != 0 {
		t.Errorf("all-zero scores should normalise to 0, got %v", got[0].FTSScore)
	}
	// Min and max are found wherever they sit.
	got = mergeAlpha([]fts.FTSResult{{DocID: "m", Score: 5}, {DocID: "lo", Score: 1}, {DocID: "hi", Score: 9}},
		[]vector.VectorResult{{DocID: "v", Score: 0.9}}, 0.5, 2)
	if len(got) != 2 || got[0].Document.ID != "hi" {
		t.Errorf("mergeAlpha top2 = %+v", got)
	}
	if alphaOrDefault(nil) != 0.5 || alphaOrDefault(floatPtr(0)) != 0 {
		t.Error("alphaOrDefault")
	}
}

func TestCovGLoadHybridDocsSkipsBadEntries(t *testing.T) {
	s := covGServer(t)
	good := addTestDoc(t, s, "blog", "good", "en", "fine", nil)
	covGPutRaw(t, s, "blog", "corrupt", []byte{0xff, 0x01})
	covGPutDoc(t, s, "blog", &storage.Doc{ID: "old", Key: "old", Lang: "en", ContentMD: "x",
		ExpiresAt: time.Now().Add(-time.Minute).Unix()})
	items := []HybridSearchResultItem{
		{Document: storage.Doc{ID: "ghost"}}, {Document: storage.Doc{ID: "corrupt"}},
		{Document: storage.Doc{ID: "old"}}, {Document: storage.Doc{ID: good.ID}, CombinedScore: 0.7},
	}
	out := s.loadHybridDocs("blog", items, false)
	if len(out) != 1 || out[0].Document.ID != good.ID || out[0].Rank != 1 || out[0].Document.ContentMD != "" {
		t.Errorf("loadHybridDocs = %+v", out)
	}

	// Without a docs bucket there is nothing to load.
	s.BucketNames.Docs = []byte("docs")
	if err := s.DBUpdateC("blog", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) }); err != nil {
		t.Fatal(err)
	}
	if out := s.loadHybridDocs("blog", items, true); len(out) != 0 {
		t.Errorf("no bucket should load nothing, got %d", len(out))
	}
}

func TestCovGHybridStoreFailureIsAnError(t *testing.T) {
	s := covGServer(t)
	addTestDoc(t, s, "blog", "a", "en", "golang", nil)
	closeStorage(t, s, "blog")
	_, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "golang"})
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "FTS search failed") {
		t.Errorf("body: %s", rec.Body.String())
	}
}

func TestCovGHybridPinBeyondTopKIsTrimmed(t *testing.T) {
	s, docs := covGHybridServer(t)
	cm := NewCurationManager(s.DB)
	if err := cm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := cm.Set(&CurationRule{ID: "p", Collection: "blog", Query: "berlin", Enabled: true,
		Pins: []PinnedDoc{{Key: "paris", Lang: "en", Position: 1}}}); err != nil {
		t.Fatal(err)
	}
	s.CurationManager = cm
	resp, rec := covGHybrid(t, s, map[string]any{"collection": "blog", "query": "berlin", "topK": 1})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 1 || resp.Results[0].Document.ID != docs[2].ID {
		t.Errorf("an injected pin must not push the answer past topK: %+v", resp.Results)
	}
}

func TestCovGHybridFilteredKeywordMatchesAreCapped(t *testing.T) {
	s := covGServer(t)
	for i := 0; i < 60; i++ {
		addTestDoc(t, s, "bulk", "k"+strings.Repeat("x", i), "en", "shared words here",
			map[string][]string{"tag": {"all"}})
	}
	res, err := s.runFTSSearch(HybridSearchRequest{Collection: "bulk", Query: "shared", TopK: 1, Algorithm: "bm25",
		FilterMeta: map[string][]string{"tag": {"all"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 50 {
		t.Errorf("filtered keyword candidates should be capped at the oversample floor 50, got %d", len(res))
	}
}

func TestCovGHybridOnlyIDsRestrictsKeywordHalf(t *testing.T) {
	s := covGServer(t)
	a := addTestDoc(t, s, "blog", "a", "en", "golang tips", map[string][]string{"tag": {"go"}})
	b := addTestDoc(t, s, "blog", "b", "en", "golang tricks", map[string][]string{"tag": {"go"}})
	req := HybridSearchRequest{Collection: "blog", Query: "golang", TopK: 5, Algorithm: "bm25"}

	req.onlyIDs = map[string]bool{a.ID: true}
	res, err := s.runFTSSearch(req)
	if err != nil || len(res) != 1 || res[0].DocID != a.ID {
		t.Errorf("onlyIDs alone: %+v %v", res, err)
	}

	// Intersected with a metadata filter.
	req.FilterMeta = map[string][]string{"tag": {"go"}}
	req.onlyIDs = map[string]bool{b.ID: true, "elsewhere": true}
	res, _ = s.runFTSSearch(req)
	if len(res) != 1 || res[0].DocID != b.ID {
		t.Errorf("onlyIDs ∩ filter: %+v", res)
	}

	// An empty intersection searches nothing.
	req.onlyIDs = map[string]bool{"elsewhere": true}
	if res, _ = s.runFTSSearch(req); res != nil {
		t.Errorf("empty intersection should return nil, got %+v", res)
	}
}
