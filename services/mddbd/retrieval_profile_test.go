package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/fts"
	proto "mddb/proto"
)

// RAG-001. Retrieval settings were scattered as constants across a dozen files;
// these pin the precedence rule that replaces them — request parameter >
// collection profile > that path's historical default — and, above all, that a
// collection without a profile behaves exactly as before.

func profileServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)
	srv.CollectionManager = NewCollectionManager(srv.DB)
	if err := srv.CollectionManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	return srv, cleanup
}

func setProfile(t *testing.T, srv *Server, collection string, p *RetrievalProfileDef) {
	t.Helper()
	if err := srv.CollectionManager.Set(collection, &CollectionConfig{Type: "default", Retrieval: p}); err != nil {
		t.Fatal(err)
	}
}

// The whole point: existing collections must not move.
func TestResolveFallsBackWithoutAProfile(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	if got := srv.ResolveTopK("unconfigured", 0, 50); got != 50 {
		t.Errorf("topK = %d, want the path default 50", got)
	}
	if got := srv.ResolveRetrievalMode("unconfigured", "", "parent"); got != "parent" {
		t.Errorf("mode = %q, want parent", got)
	}
	if got := srv.ResolveHybridStrategy("unconfigured", "", "alpha"); got != "alpha" {
		t.Errorf("strategy = %q, want alpha", got)
	}
	if got := srv.ResolveHybridAlpha("unconfigured", 0, false, 0.5); got != 0.5 {
		t.Errorf("alpha = %v, want 0.5", got)
	}
	if got := srv.ContextTokenBudget("unconfigured"); got != 0 {
		t.Errorf("budget = %d, want 0 (unset)", got)
	}
}

// A collection configured with an empty profile is the same as no profile —
// every field is optional.
func TestEmptyProfileChangesNothing(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	setProfile(t, srv, "empty", &RetrievalProfileDef{})

	if got := srv.ResolveTopK("empty", 0, 50); got != 50 {
		t.Errorf("topK = %d, want the path default 50", got)
	}
	if got := srv.ResolveHybridStrategy("empty", "", "alpha"); got != "alpha" {
		t.Errorf("strategy = %q, want alpha", got)
	}
}

func TestProfileBeatsThePathDefault(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	setProfile(t, srv, "docs", &RetrievalProfileDef{
		TopK:           40,
		RetrievalMode:  RetrievalModeChunk,
		HybridStrategy: HybridStrategyRRF,
		HybridAlpha:    0.8,
		HybridAlphaSet: true,
	})

	if got := srv.ResolveTopK("docs", 0, 50); got != 40 {
		t.Errorf("topK = %d, want the profile's 40", got)
	}
	if got := srv.ResolveRetrievalMode("docs", "", "parent"); got != RetrievalModeChunk {
		t.Errorf("mode = %q, want chunk", got)
	}
	if got := srv.ResolveHybridStrategy("docs", "", "alpha"); got != HybridStrategyRRF {
		t.Errorf("strategy = %q, want rrf", got)
	}
	if got := srv.ResolveHybridAlpha("docs", 0, false, 0.5); got != 0.8 {
		t.Errorf("alpha = %v, want the profile's 0.8", got)
	}
}

// An existing client that passes its own parameters must not notice that
// profiles exist at all.
func TestRequestBeatsTheProfile(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	setProfile(t, srv, "docs", &RetrievalProfileDef{
		TopK:           40,
		RetrievalMode:  RetrievalModeChunk,
		HybridStrategy: HybridStrategyRRF,
		HybridAlpha:    0.8,
		HybridAlphaSet: true,
	})

	if got := srv.ResolveTopK("docs", 7, 50); got != 7 {
		t.Errorf("topK = %d, want the caller's 7", got)
	}
	if got := srv.ResolveRetrievalMode("docs", RetrievalModeWindow, "parent"); got != RetrievalModeWindow {
		t.Errorf("mode = %q, want window", got)
	}
	if got := srv.ResolveHybridStrategy("docs", HybridStrategyAlpha, "alpha"); got != HybridStrategyAlpha {
		t.Errorf("strategy = %q, want alpha", got)
	}
	// 0.0 is a legitimate request (pure keyword) and must beat the profile.
	if got := srv.ResolveHybridAlpha("docs", 0, true, 0.5); got != 0 {
		t.Errorf("alpha = %v, want the caller's explicit 0", got)
	}
}

// A profile with alpha 0.0 is configured, not unset.
func TestExplicitZeroAlphaInProfileIsHonoured(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()
	setProfile(t, srv, "keyword", &RetrievalProfileDef{HybridAlpha: 0, HybridAlphaSet: true})

	if got := srv.ResolveHybridAlpha("keyword", 0, false, 0.5); got != 0 {
		t.Errorf("alpha = %v, want the profile's explicit 0", got)
	}

	// Without the flag, 0 means "not configured".
	setProfile(t, srv, "unset", &RetrievalProfileDef{HybridAlpha: 0})
	if got := srv.ResolveHybridAlpha("unset", 0, false, 0.5); got != 0.5 {
		t.Errorf("alpha = %v, want the default 0.5", got)
	}
}

func TestResolversSurviveAMissingManager(t *testing.T) {
	srv := &Server{}
	if got := srv.ResolveTopK("any", 0, 50); got != 50 {
		t.Errorf("topK = %d without a collection manager, want the fallback", got)
	}
	if p := srv.RetrievalProfile("any"); p != nil {
		t.Errorf("profile = %+v, want nil", p)
	}
	if p := (*Server)(nil).RetrievalProfile("any"); p != nil {
		t.Error("a nil server returned a profile")
	}
	if got := srv.RetrievalProfile(""); got != nil {
		t.Error("an empty collection name returned a profile")
	}
}

// A profile is stored data: a value that cannot mean anything must be refused
// at write time rather than surfacing as a strange result months later.
func TestProfileValidation(t *testing.T) {
	valid := []*RetrievalProfileDef{
		nil,
		{},
		{DefaultSearchType: SearchTypeHybrid, TopK: 40, RetrievalMode: RetrievalModeChunk,
			HybridStrategy: HybridStrategyRRF, ContextTokenBudget: 8000},
		{HybridAlpha: 0, HybridAlphaSet: true},
		{HybridAlpha: 1, HybridAlphaSet: true},
	}
	for i, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("valid profile %d rejected: %v", i, err)
		}
	}

	invalid := map[string]*RetrievalProfileDef{
		"unknown search type": {DefaultSearchType: "magic"},
		"unknown mode":        {RetrievalMode: "sideways"},
		"unknown strategy":    {HybridStrategy: "vibes"},
		"negative topK":       {TopK: -1},
		"absurd topK":         {TopK: maxProfileTopK + 1},
		"alpha above one":     {HybridAlpha: 1.5, HybridAlphaSet: true},
		"alpha below zero":    {HybridAlpha: -0.1, HybridAlphaSet: true},
		"negative budget":     {ContextTokenBudget: -1},
		"absurd budget":       {ContextTokenBudget: maxProfileContextTokenBudget + 1},
	}
	for name, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestHasHybridAlpha(t *testing.T) {
	if (*RetrievalProfileDef)(nil).HasHybridAlpha() {
		t.Error("a nil profile claims to configure alpha")
	}
	if (&RetrievalProfileDef{HybridAlpha: 0.7}).HasHybridAlpha() {
		t.Error("alpha without the flag counts as configured")
	}
	if !(&RetrievalProfileDef{HybridAlphaSet: true}).HasHybridAlpha() {
		t.Error("the flag was ignored")
	}
}

// --- context budget ---

func TestApproxTokens(t *testing.T) {
	cases := map[string]int{"": 0, "a": 1, "abcd": 1, "abcde": 2, "12345678": 2}
	for in, want := range cases {
		if got := approxTokens(in); got != want {
			t.Errorf("approxTokens(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestBudgetCut(t *testing.T) {
	cases := []struct {
		name      string
		sizes     []int
		budget    int
		keep      int
		truncated bool
	}{
		{"no budget keeps everything", []int{100, 100, 100}, 0, 3, false},
		{"everything fits", []int{10, 10, 10}, 100, 3, false},
		{"exactly fits", []int{10, 10, 10}, 30, 3, false},
		{"tail is dropped", []int{10, 10, 10}, 25, 2, true},
		{"only the first fits", []int{10, 10}, 15, 1, true},
		{"nothing to cut", nil, 100, 0, false},
		// Returning nothing would read as "no matches" rather than as a
		// budget too small for the corpus.
		{"first result is kept even when oversized", []int{500}, 10, 1, false},
		{"oversized first, rest dropped", []int{500, 10}, 10, 1, true},
	}
	for _, c := range cases {
		keep, truncated := budgetCut(c.sizes, c.budget)
		if keep != c.keep || truncated != c.truncated {
			t.Errorf("%s: keep=%d truncated=%v, want keep=%d truncated=%v",
				c.name, keep, truncated, c.keep, c.truncated)
		}
	}
}

func TestApplyContextBudget(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	// Four results of ~25 tokens each (100 bytes).
	results := []string{
		string(make([]byte, 100)), string(make([]byte, 100)),
		string(make([]byte, 100)), string(make([]byte, 100)),
	}
	tokensOf := func(s string) int { return approxTokens(s) }

	// No profile: untouched.
	got, truncated := applyContextBudget(srv, "none", results, tokensOf)
	if len(got) != 4 || truncated {
		t.Errorf("without a profile: %d results, truncated=%v", len(got), truncated)
	}

	setProfile(t, srv, "capped", &RetrievalProfileDef{ContextTokenBudget: 60})
	got, truncated = applyContextBudget(srv, "capped", results, tokensOf)
	if len(got) != 2 || !truncated {
		t.Errorf("with a 60-token budget: %d results, truncated=%v; want 2 and true", len(got), truncated)
	}

	// An empty result list must not be reported as truncated.
	got, truncated = applyContextBudget(srv, "capped", []string{}, tokensOf)
	if len(got) != 0 || truncated {
		t.Errorf("empty input: %d results, truncated=%v", len(got), truncated)
	}
}

// --- proto round-trip ---

func TestRetrievalProfileProtoRoundTrip(t *testing.T) {
	original := &RetrievalProfileDef{
		DefaultSearchType:  SearchTypeHybrid,
		TopK:               40,
		RetrievalMode:      RetrievalModeWindow,
		HybridStrategy:     HybridStrategyRRF,
		HybridAlpha:        0.25,
		HybridAlphaSet:     true,
		ContextTokenBudget: 8000,
	}
	back := retrievalProfileFromProto(retrievalProfileToProto(original))
	if *back != *original {
		t.Errorf("round trip changed the profile:\n %+v\n %+v", original, back)
	}

	// nil for nil: a collection without a profile must report an absent
	// field, not a block of zeros a client has to know to ignore.
	if retrievalProfileToProto(nil) != nil {
		t.Error("a nil profile became a proto message")
	}
	if retrievalProfileFromProto(nil) != nil {
		t.Error("a nil message became a profile")
	}
}

// --- the gRPC overwrite bug ---

// CollectionConfigProto carries 7 of ~15 fields, and Set writes whatever it is
// handed: building a fresh struct erased everything gRPC cannot express. The
// worst case is Encrypted, whose false value goes straight to the encryptor, so
// the next document in an encrypted collection was written as plaintext.
func TestGRPCSetConfigPreservesFieldsItCannotExpress(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	if err := srv.CollectionManager.Set("secure", &CollectionConfig{
		Type:            "default",
		Description:     "before",
		StorageBackend:  "s3",
		StorageConfig:   &StorageConfigDef{Endpoint: "s3.example.com", Bucket: "docs"},
		Quantization:    "int8",
		DiskOnlyVectors: true,
		Encrypted:       true,
		MaxRevisions:    5,
		SpellCorrect:    true,
		Retrieval:       &RetrievalProfileDef{TopK: 40},
	}); err != nil {
		t.Fatal(err)
	}

	g := &GRPCServer{server: srv}
	if _, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{
		Collection:  "secure",
		Type:        "default",
		Description: "after",
	}); err != nil {
		t.Fatal(err)
	}

	got, found := srv.CollectionManager.Get("secure")
	if !found {
		t.Fatal("the config disappeared")
	}
	if got.Description != "after" {
		t.Errorf("the update did not apply: description = %q", got.Description)
	}
	if !got.Encrypted {
		t.Error("a gRPC description update disabled at-rest encryption")
	}
	if got.StorageBackend != "s3" || got.StorageConfig == nil {
		t.Errorf("storage backend was erased: %q / %+v", got.StorageBackend, got.StorageConfig)
	}
	if got.Quantization != "int8" || !got.DiskOnlyVectors {
		t.Errorf("vector settings were erased: %q / %v", got.Quantization, got.DiskOnlyVectors)
	}
	if !got.SpellCorrect {
		t.Error("spell correction was erased")
	}
	if got.Retrieval == nil || got.Retrieval.TopK != 40 {
		t.Errorf("the retrieval profile was erased: %+v", got.Retrieval)
	}
}

func TestGRPCSetConfigWritesTheProfile(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	g := &GRPCServer{server: srv}
	if _, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{
		Collection: "docs",
		Type:       "default",
		Retrieval: &proto.RetrievalProfileProto{
			DefaultSearchType:  SearchTypeHybrid,
			TopK:               40,
			ContextTokenBudget: 8000,
		},
	}); err != nil {
		t.Fatal(err)
	}

	p := srv.RetrievalProfile("docs")
	if p == nil || p.TopK != 40 || p.ContextTokenBudget != 8000 {
		t.Fatalf("profile not stored: %+v", p)
	}

	// And it comes back out.
	resp, err := g.GetCollectionConfig(context.Background(), &proto.GetCollectionConfigRequest{Collection: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config == nil || resp.Config.Retrieval == nil || resp.Config.Retrieval.TopK != 40 {
		t.Errorf("the profile did not survive the round trip: %+v", resp.Config)
	}
}

func TestGRPCSetConfigRejectsAnInvalidProfile(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	g := &GRPCServer{server: srv}
	_, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{
		Collection: "docs",
		Retrieval:  &proto.RetrievalProfileProto{TopK: -5},
	})
	if err == nil {
		t.Error("a negative topK was accepted")
	}
}

// The REST config request must carry every field CollectionConfig holds:
// PUT replaces the stored config, so a field missing from the request is a
// field silently cleared on every save.
func TestRESTSetConfigCarriesEveryField(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	body := `{"collection":"docs","type":"default","description":"d","icon":"i","color":"#fff",
		"trackAccess":true,"trackHot":true,"spellCorrect":true,"spellLang":"pl",
		"maxRevisions":3,"encrypted":false,
		"retrieval":{"topK":40,"defaultSearchType":"hybrid","contextTokenBudget":8000}}`
	req := httptest.NewRequest("PUT", "/v1/collection-config", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleCollectionConfigSet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	cfg, found := srv.CollectionManager.Get("docs")
	if !found {
		t.Fatal("config not stored")
	}
	// The panel has sent these four since it was written; the handler
	// ignored them.
	if !cfg.TrackAccess || !cfg.TrackHot || !cfg.SpellCorrect || cfg.SpellLang != "pl" {
		t.Errorf("tracking/spell settings were dropped: %+v", cfg)
	}
	if cfg.Retrieval == nil || cfg.Retrieval.TopK != 40 || cfg.Retrieval.ContextTokenBudget != 8000 {
		t.Errorf("retrieval profile was dropped: %+v", cfg.Retrieval)
	}
}

func TestRESTSetConfigRejectsAnInvalidProfile(t *testing.T) {
	srv, cleanup := profileServer(t)
	defer cleanup()

	body := `{"collection":"docs","retrieval":{"defaultSearchType":"magic"}}`
	req := httptest.NewRequest("PUT", "/v1/collection-config", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleCollectionConfigSet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("an unknown search type gave %d, want 400: %s", w.Code, w.Body.String())
	}
}

// RAG-001 caps the context a collection hands back, and the MCP paths — the
// agents the cap exists for — resolved topK from the profile, returned its
// responsePrompt, and never applied the budget. An agent that asked for
// document bodies was the one caller that could be handed more than its model
// holds.
func TestMCPSearchesRespectTheContextBudget(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	// Ten documents of roughly 100 characters each — about 25 tokens apiece.
	body := strings.Repeat("restart the service carefully ", 4)
	docs := make([]*proto.BatchDocument, 0, 10)
	for i := 0; i < 10; i++ {
		docs = append(docs, makeBatchDoc(fmt.Sprintf("doc-%d", i), "en", body, nil, false))
	}
	srv.FTSIndex = fts.NewFTSIndex(srv.DB)
	if err := srv.FTSIndex.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "runbooks", docs); err != nil {
		t.Fatal(err)
	}

	if err := srv.CollectionManager.Set("runbooks", &CollectionConfig{
		Type: "default",
		Retrieval: &RetrievalProfileDef{
			TopK: 10,
			// Room for roughly two documents.
			ContextTokenBudget: 60,
		},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := client.FTSSearch(context.Background(), &MCPFTSSearchRequest{
		Collection: "runbooks",
		Query:      "restart",
		// The budget caps the content a search hands over. Without
		// includeContent an MCP result carries no body at all (GO-022), so
		// there is nothing to cap and nothing to cut.
		IncludeContent: true,
	})
	if err != nil {
		t.Fatalf("FTSSearch: %v", err)
	}

	if len(resp.Results) == 0 {
		t.Skip("the FTS index returned nothing for this fixture")
	}
	if len(resp.Results) >= 10 {
		t.Errorf("the budget was ignored: %d results came back under a 60-token cap", len(resp.Results))
	}
	if !resp.ContextTruncated {
		t.Error("results were dropped and contextTruncated was not set — the caller cannot tell it holds a partial answer")
	}
	// Total must describe what the caller received, not what was found before
	// the cut.
	if resp.Total != len(resp.Results) {
		t.Errorf("total = %d but %d results were returned", resp.Total, len(resp.Results))
	}
}

// A collection with no budget must be unaffected.
func TestMCPSearchWithoutABudgetIsUnchanged(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()

	docs := []*proto.BatchDocument{
		makeBatchDoc("a", "en", "restart the service", nil, false),
		makeBatchDoc("b", "en", "restart the other service", nil, false),
	}
	srv.FTSIndex = fts.NewFTSIndex(srv.DB)
	if err := srv.FTSIndex.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "plain", docs); err != nil {
		t.Fatal(err)
	}

	resp, err := client.FTSSearch(context.Background(), &MCPFTSSearchRequest{
		Collection: "plain", Query: "restart",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContextTruncated {
		t.Error("a collection with no budget reported truncation")
	}
}
