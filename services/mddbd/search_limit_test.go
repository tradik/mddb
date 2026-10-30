package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	proto "mddb/proto"
)

// The server-side result cap (#368): MDDB_SEARCH_MAX_LIMIT and a collection's
// retrieval.maxTopK. These tests cover the cap itself and the REST surfaces;
// search_limit_surfaces_test.go runs the same request over MCP, gRPC and
// GraphQL.

// cappedGold is the golden collection (three documents) with the server cap
// at two, so any request for three results is capped.
func cappedGold(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := goldenServer(t)
	s.SearchMaxLimit = 2
	return s, cleanup
}

// limitOf decodes a JSON body's limit report.
func limitOf(t *testing.T, body []byte) LimitReport {
	t.Helper()
	var rep LimitReport
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("not a JSON object: %s", body)
	}
	return rep
}

// wantCapped requires a 200 whose body reports the limit capped to want.
func wantCapped(t *testing.T, name string, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
	}
	if rep := limitOf(t, rec.Body.Bytes()); !rep.LimitCapped || rep.Limit != want {
		t.Fatalf("%s: limit report %+v, want capped to %d: %s", name, rep, want, rec.Body.String())
	}
}

func TestParseSearchMaxLimit(t *testing.T) {
	for raw, want := range map[string]int{"": defaultSearchMaxLimit, " 50 ": 50, "1": 1, "1000000": searchMaxLimitCeiling} {
		if got, err := parseSearchMaxLimit(raw); err != nil || got != want {
			t.Errorf("parseSearchMaxLimit(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"0", "-5", "abc", "1000001", "10.5"} {
		_, err := parseSearchMaxLimit(raw)
		if err == nil || !strings.Contains(err.Error(), `"`+raw+`"`) || !strings.Contains(err.Error(), searchMaxLimitEnv) {
			t.Errorf("parseSearchMaxLimit(%q) error = %v; want one naming the value and the variable", raw, err)
		}
	}
}

func TestRunServerRejectsBadSearchMaxLimit(t *testing.T) {
	withTrustedProxies(t, "")
	t.Setenv(trustedProxiesEnv, "")
	t.Setenv(searchMaxLimitEnv, "lots")
	c := covAExpectFatal(t, func() { runServer(ServerConfig{}) })
	if c.msg != "startup step failed" || !strings.Contains(c.args, `"lots"`) || !strings.Contains(c.args, searchMaxLimitEnv) {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestCapLimit_ServerCollectionAndEndpoint(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if (&Server{}).searchMaxLimit() != defaultSearchMaxLimit || (*Server)(nil).searchMaxLimit() != defaultSearchMaxLimit {
		t.Fatal("a server without a cap must use the default")
	}
	if n, rep := s.CapLimit("c", 1000, 0); n != 1000 || rep.LimitCapped {
		t.Fatalf("at the default cap: %d %+v", n, rep)
	}
	if n, rep := s.CapLimit("c", 100000, 0); n != 1000 || rep != (LimitReport{Limit: 1000, LimitCapped: true}) {
		t.Fatalf("above the default cap: %d %+v", n, rep)
	}
	if n, rep := s.CapLimit("c", 500, 200); n != 200 || !rep.LimitCapped {
		t.Fatalf("endpoint bound: %d %+v", n, rep)
	}
	capLimitByCollection(t, s)
}

// capLimitByCollection checks a collection's maxTopK lowers the server cap
// and never raises it, and a cross-collection search takes the strictest
// target.
func capLimitByCollection(t *testing.T, s *Server) {
	t.Helper()
	// maxTopK lowers the server cap...
	if err := s.CollectionManager.Set("low", &CollectionConfig{Retrieval: &RetrievalProfileDef{MaxTopK: 5}}); err != nil {
		t.Fatal(err)
	}
	if n, rep := s.CapLimit("low", 50, 0); n != 5 || rep.Limit != 5 {
		t.Fatalf("maxTopK below the server cap: %d %+v", n, rep)
	}
	// ...and never raises it: the operator's server value is the ceiling.
	if err := s.CollectionManager.Set("high", &CollectionConfig{Retrieval: &RetrievalProfileDef{MaxTopK: 5000}}); err != nil {
		t.Fatal(err)
	}
	if n, rep := s.CapLimit("high", 5000, 0); n != 1000 || rep.Limit != 1000 {
		t.Fatalf("maxTopK above the server cap must not raise it: %d %+v", n, rep)
	}
	s.SearchMaxLimit = 3
	if n, _ := s.CapLimit("low", 50, 0); n != 3 {
		t.Fatalf("a server cap below maxTopK wins: %d", n)
	}

	// A cross-collection search takes the strictest target.
	s.SearchMaxLimit = 0
	if n, rep := s.capCrossLimit([]string{"high", "low"}, 50); n != 5 || rep.Limit != 5 {
		t.Fatalf("cross: %d %+v", n, rep)
	}
	if n, rep := s.capCrossLimit([]string{"high"}, 50); n != 50 || rep.LimitCapped {
		t.Fatalf("cross under the cap: %d %+v", n, rep)
	}
}

func TestResolveTopK_CapsDefaultsAndProfiles(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.SearchMaxLimit = 20
	if n, rep := s.ResolveTopK("c", 0, 50); n != 20 || !rep.LimitCapped {
		t.Fatalf("a default above the cap is capped and reported: %d %+v", n, rep)
	}
	if err := s.CollectionManager.Set("p", &CollectionConfig{Retrieval: &RetrievalProfileDef{TopK: 8, MaxTopK: 10}}); err != nil {
		t.Fatal(err)
	}
	if n, rep := s.ResolveTopK("p", 0, 50); n != 8 || rep.LimitCapped {
		t.Fatalf("the profile's topK under maxTopK: %d %+v", n, rep)
	}
	if n, rep := s.ResolveTopK("p", 30, 50); n != 10 || rep.Limit != 10 {
		t.Fatalf("an explicit limit above maxTopK: %d %+v", n, rep)
	}
}

func TestRetrievalProfile_MaxTopKValidationAndProto(t *testing.T) {
	for _, p := range []*RetrievalProfileDef{{MaxTopK: -1}, {MaxTopK: searchMaxLimitCeiling + 1}, {TopK: 20, MaxTopK: 10}} {
		if err := p.Validate(); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
	if err := (&RetrievalProfileDef{TopK: 10, MaxTopK: 10}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (&RetrievalProfileDef{TopK: 50}).Validate(); err != nil {
		t.Fatal("topK without maxTopK:", err)
	}
	back := retrievalProfileFromProto(retrievalProfileToProto(&RetrievalProfileDef{MaxTopK: 25}))
	if back.MaxTopK != 25 {
		t.Fatalf("proto round trip lost maxTopK: %+v", back)
	}
	if pp := retrievalProfileToProto(&RetrievalProfileDef{MaxTopK: 7}); pp.GetMaxTopK() != 7 {
		t.Fatalf("to proto: %v", pp)
	}
	_ = proto.RetrievalProfileProto{}
}

func TestSearchLimit_HelpersAndMCPContent(t *testing.T) {
	if positiveOr(0, 9) != 9 || positiveOr(-1, 9) != 9 || positiveOr(4, 9) != 4 {
		t.Fatal("positiveOr")
	}
	rec := httptest.NewRecorder()
	writeLimitHeaders(rec, LimitReport{})
	if rec.Header().Get("X-Limit-Capped") != "" {
		t.Fatal("headers for an uncapped limit")
	}
	writeLimitHeaders(rec, LimitReport{Limit: 7, LimitCapped: true})
	if rec.Header().Get("X-Limit-Capped") != "true" || rec.Header().Get("X-Limit") != "7" {
		t.Fatalf("headers: %v", rec.Header())
	}
	for _, uncapped := range []string{`{"results":[]}`, `{"limitCapped":false}`, `text with "limitCapped" in it`, `{"document":{"contentMd":"\"limitCapped\": true"}}`} {
		if got := mcpLimitContent(uncapped); got != nil {
			t.Errorf("mcpLimitContent(%s) = %v, want nil", uncapped, got)
		}
	}
	got := mcpLimitContent("{\n  \"limit\": 2,\n  \"limitCapped\": true\n}")
	if got["limitCapped"] != true || got["limit"] != 2 {
		t.Fatalf("mcpLimitContent = %v", got)
	}
}

func TestNormalizeFTSRequest_CapsFacetsAndHighlights(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.SearchMaxLimit = 40
	req := FTSSearchRequest{Collection: "c", Query: "q", MaxHighlights: 500, FragmentSize: 1 << 20}
	if err := s.normalizeFTSRequest(&req); err != nil {
		t.Fatal(err)
	}
	if req.FacetMaxValues != 40 || req.MaxHighlights != maxHighlightFragments || req.FragmentSize != maxHighlightFragmentSize {
		t.Fatalf("facets %d, highlights %d, fragment %d", req.FacetMaxValues, req.MaxHighlights, req.FragmentSize)
	}
	if req.Limit != 40 || !req.limitReport.LimitCapped {
		t.Fatalf("the default limit 50 is above the cap: %d %+v", req.Limit, req.limitReport)
	}
	// Normalising again (handleFTS, then searchFTS) keeps the report.
	if err := s.normalizeFTSRequest(&req); err != nil || !req.limitReport.LimitCapped {
		t.Fatalf("second pass dropped the report: %v %+v", err, req.limitReport)
	}
	small := FTSSearchRequest{Collection: "c", Query: "q", FacetMaxValues: 3, MaxHighlights: 2, FragmentSize: 80}
	if err := s.normalizeFTSRequest(&small); err != nil || small.FacetMaxValues != 3 || small.MaxHighlights != 2 || small.FragmentSize != 80 {
		t.Fatalf("values under the bounds must be kept: %v %+v", err, small)
	}
}

// TestSearchLimit_RESTSurfaces: a request for three results with the cap at
// two gets two, and the body says so.
func TestSearchLimit_RESTSurfaces(t *testing.T) {
	s, cleanup := cappedGold(t)
	defer cleanup()
	for name, tc := range map[string]struct {
		h    http.HandlerFunc
		body map[string]any
	}{
		"fts":              {s.handleFTS, map[string]any{"collection": "gold", "query": "pump water", "limit": 3}},
		"fts-default":      {s.handleFTS, map[string]any{"collection": "gold", "query": "pump water"}},
		"fts-passages":     {s.handleFTS, map[string]any{"collection": "gold", "query": "pump water", "limit": 3, "format": "passages"}},
		"vector":           {s.handleVectorSearch, map[string]any{"collection": "gold", "query": "pump water", "topK": 3}},
		"vector-passages":  {s.handleVectorSearch, map[string]any{"collection": "gold", "query": "pump water", "topK": 3, "format": "passages"}},
		"vector-filtered":  {s.handleVectorSearch, map[string]any{"collection": "gold", "query": "pump", "topK": 3, "filterMeta": map[string]any{"none": []string{"x"}}}},
		"hybrid":           {s.handleHybridSearch, map[string]any{"collection": "gold", "query": "pump water", "topK": 3}},
		"hybrid-passages":  {s.handleHybridSearch, map[string]any{"collection": "gold", "query": "pump water", "topK": 3, "format": "passages"}},
		"hybrid-geo-empty": {s.handleHybridSearch, map[string]any{"collection": "gold", "query": "pump", "topK": 3, "geo": map[string]any{"lat": 1, "lng": 1, "radiusMeters": 10}}},
		"cross":            {s.handleCrossSearch, map[string]any{"targetCollections": []string{"gold"}, "query": "pump water", "topK": 3}},
		"cross-passages":   {s.handleCrossSearch, map[string]any{"targetCollections": []string{"gold"}, "query": "pump water", "topK": 3, "format": "passages"}},
		"cross-fts":        {s.handleCrossSearch, map[string]any{"targetCollections": []string{"gold"}, "query": "pump water", "topK": 3, "searchType": "fts"}},
		"cross-hybrid-p":   {s.handleCrossSearch, map[string]any{"targetCollections": []string{"gold"}, "query": "pump water", "topK": 3, "searchType": "hybrid", "format": "passages"}},
		"symbol":           {s.handleSymbol, map[string]any{"collection": "gold", "name": "pump", "limit": 3}},
	} {
		if name == "hybrid-geo-empty" && s.GeoIndex == nil {
			continue
		}
		rec := doRequest(t, tc.h, tc.body)
		wantCapped(t, name, rec, 2)
		if strings.Contains(name, "passages") || strings.HasSuffix(name, "-p") || name == "symbol" {
			continue
		}
		var out struct {
			Results []json.RawMessage `json:"results"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if len(out.Results) > 2 {
			t.Errorf("%s: %d results above the cap", name, len(out.Results))
		}
	}

	// /v1/search answers a bare array, so the report is in headers.
	rec := doRequest(t, s.handleSearch, map[string]any{"collection": "gold", "limit": 3})
	var page []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || len(page) != 2 {
		t.Fatalf("search: %v %s", err, rec.Body.String())
	}
	if rec.Header().Get("X-Limit-Capped") != "true" || rec.Header().Get("X-Limit") != "2" {
		t.Fatalf("search headers: %v", rec.Header())
	}

	// GET /v1/autocomplete
	req := httptest.NewRequest(http.MethodGet, "/v1/autocomplete?collection=gold&q=p&topN=3", nil)
	rec = httptest.NewRecorder()
	s.handleAutocomplete(rec, req)
	wantCapped(t, "autocomplete", rec, 2)
}

// TestSearchLimit_UnderTheCapUnchanged: with every limit under the cap, the
// answers are the ones the image golden pins, whatever the cap is, and none
// carries a limit report.
func TestSearchLimit_UnderTheCapUnchanged(t *testing.T) {
	s, cleanup := goldenServer(t)
	defer cleanup()
	atDefault := goldenResponses(t, s)
	s.SearchMaxLimit = searchMaxLimitCeiling
	atCeiling := goldenResponses(t, s)
	for name, body := range atDefault {
		if strings.Contains(body, "limitCapped") || strings.Contains(body, "limit_capped") {
			t.Errorf("%s reports a cap under it: %s", name, body)
		}
		if !sameJSON(t, body, atCeiling[name]) {
			t.Errorf("%s differs between caps:\n%s\n%s", name, body, atCeiling[name])
		}
	}
	rec := doRequest(t, s.handleSearch, map[string]any{"collection": "gold", "limit": 3})
	if rec.Header().Get("X-Limit-Capped") != "" {
		t.Fatal("search headers under the cap")
	}
}

// TestSearchLimit_CollectionMaxTopK: the collection's maxTopK lowers the cap
// on REST and gRPC alike, and cannot raise it above the server's.
func TestSearchLimit_CollectionMaxTopK(t *testing.T) {
	s, cleanup := goldenServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("gold", &CollectionConfig{Retrieval: &RetrievalProfileDef{MaxTopK: 1}}); err != nil {
		t.Fatal(err)
	}
	wantCapped(t, "fts maxTopK 1", doRequest(t, s.handleFTS, map[string]any{"collection": "gold", "query": "pump water", "limit": 3}), 1)
	g := &GRPCServer{server: s}
	gf, err := g.FTS(t.Context(), &proto.FTSRequest{Collection: "gold", Query: "pump water", Limit: 3})
	if err != nil || !gf.LimitCapped || gf.Limit != 1 || len(gf.Results) != 1 {
		t.Fatalf("grpc fts maxTopK 1: %v %+v", err, gf)
	}

	if err := s.CollectionManager.Set("gold", &CollectionConfig{Retrieval: &RetrievalProfileDef{MaxTopK: 50}}); err != nil {
		t.Fatal(err)
	}
	s.SearchMaxLimit = 2
	wantCapped(t, "fts maxTopK above the server cap", doRequest(t, s.handleFTS, map[string]any{"collection": "gold", "query": "pump water", "limit": 3}), 2)
}
