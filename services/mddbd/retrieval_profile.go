package main

import (
	"errors"
	"fmt"
	"mddb/internal/fts"

	proto "mddb/proto"
)

// Per-collection retrieval profile (RAG-001).
//
// Retrieval settings — search type, topK, granularity, hybrid strategy, context
// budget — were scattered as constants across a dozen files, each path with its
// own number: FTS defaulted to 50 results, vector to 5, hybrid to 10, memory
// recall to 10, cross-search to 10. A caller therefore had to know MDDB's
// internals to get consistent behaviour, and a collection of prose and a
// collection of source could not be configured differently at all.
//
// The right place for retrieval configuration is next to the data, not in every
// client. This puts it in the CollectionConfig that already holds the storage
// backend, quantization and encryption settings.
//
// Precedence is fixed everywhere: **explicit request parameter > collection
// profile > global default**. An existing caller that passes topK sees no
// change, and a collection with no profile behaves byte-for-byte as before —
// which is what makes this safe to ship in a minor release.

// Search types a profile may name.
const (
	SearchTypeFTS    = "fts"
	SearchTypeVector = "vector"
	SearchTypeHybrid = "hybrid"
)

// Hybrid fusion strategies.
const (
	HybridStrategyAlpha = "alpha"
	HybridStrategyRRF   = "rrf"
)

// Bounds. A profile is stored data, so a nonsensical value must be refused at
// write time rather than surfacing as a strange result months later.
const (
	maxProfileTopK               = 1000
	maxProfileContextTokenBudget = 1_000_000
)

// RetrievalProfileDef is the optional retrieval block of a CollectionConfig.
//
// Every field is optional: a zero value means "not configured", so the caller's
// parameter or the global default applies. nil profile = today's behaviour,
// which is why this needs no migration.
type RetrievalProfileDef struct {
	// DefaultSearchType tells clients which search this collection expects —
	// prose usually wants hybrid, an exact-match corpus wants fts. Consumed
	// by clients reading the config; MDDB itself does not reroute a request
	// to a different endpoint because of it.
	DefaultSearchType string `json:"defaultSearchType,omitempty"` // fts|vector|hybrid
	TopK              int    `json:"topK,omitempty"`
	RetrievalMode     string `json:"retrievalMode,omitempty"`  // parent|chunk|window
	HybridStrategy    string `json:"hybridStrategy,omitempty"` // alpha|rrf
	// HybridAlpha weights vector against keyword score. Zero is a meaningful
	// value (pure keyword), so it cannot double as "unset" — see
	// HasHybridAlpha.
	HybridAlpha float64 `json:"hybridAlpha,omitempty"`
	// HybridAlphaSet distinguishes an explicit 0.0 from an absent value.
	HybridAlphaSet bool `json:"hybridAlphaSet,omitempty"`
	// ContextTokenBudget caps the total context a search returns, so a RAG
	// caller cannot be handed more than its model can hold. Approximated as
	// len(text)/4 rather than tokenised: a real tokeniser would tie the
	// budget to one model family, and the point is a guard rail, not
	// accounting.
	ContextTokenBudget int `json:"contextTokenBudget,omitempty"`
	// Oversample is the recall/latency knob (SRCH-005): how many candidates
	// the index is asked for per requested result, before deduplication,
	// merging or rescoring trims them. 0 = unset.
	Oversample float64 `json:"oversample,omitempty"`
	// GroupBy is the collection's default result grouping (SRCH-016), used
	// when a request does not send its own — e.g. one passage per file for
	// a code collection.
	GroupBy *GroupBy `json:"groupBy,omitempty"`
	// Format is the collection's default result format (AGT-002):
	// "passages" for a code collection, where an agent wants places rather
	// than documents. Empty = "results".
	Format string `json:"format,omitempty"`
	// BM25K1 and BM25B replace the BM25 constants for this collection's
	// full-text searches (SRCH-018); nil keeps 1.2 / 0.75. Pointers, because
	// 0 is a meaningful b (no length normalisation).
	BM25K1 *float64 `json:"bm25K1,omitempty"`
	BM25B  *float64 `json:"bm25B,omitempty"`
	// VectorAlgorithm is the collection's default vector index (SRCH-013):
	// "auto" lets the query planner choose per query. Empty = flat.
	VectorAlgorithm string `json:"vectorAlgorithm,omitempty"`
	// SummaryMode is what the documents' client-written summaries do on a
	// search (RAG-009): "off" (or empty) ignores them, "blend" scores a
	// document by the better of its best chunk and its summary, "expand"
	// blends and brings a summary hit's best chunks along as passages.
	SummaryMode string `json:"summaryMode,omitempty"`
	// SummaryWeight is the BM25F weight of the meta.summary field while
	// SummaryMode is not off; nil = 2.0.
	SummaryWeight *float64 `json:"summaryWeight,omitempty"`
	// MaxTopK caps how many results one search of this collection returns,
	// whatever the request asks for (#368); 0 = the server-wide
	// MDDB_SEARCH_MAX_LIMIT alone. It can only lower that cap, never raise
	// it: see search_limit.go.
	MaxTopK int `json:"maxTopK,omitempty"`
}

// HasHybridAlpha reports whether alpha was configured, including an explicit 0.
func (p *RetrievalProfileDef) HasHybridAlpha() bool {
	return p != nil && p.HybridAlphaSet
}

// Validate rejects a profile that cannot mean anything.
func (p *RetrievalProfileDef) Validate() error {
	if p == nil {
		return nil
	}

	switch p.DefaultSearchType {
	case "", SearchTypeFTS, SearchTypeVector, SearchTypeHybrid:
	default:
		return fmt.Errorf("invalid retrieval.defaultSearchType %q: must be fts, vector, or hybrid", p.DefaultSearchType)
	}

	// Reuses the mode check the retrieval paths already apply, so a profile
	// cannot store a mode a request would reject.
	if !validRetrievalMode(p.RetrievalMode) {
		return fmt.Errorf("invalid retrieval.retrievalMode %q: must be parent, chunk, or window", p.RetrievalMode)
	}

	switch p.HybridStrategy {
	case "", HybridStrategyAlpha, HybridStrategyRRF:
	default:
		return fmt.Errorf("invalid retrieval.hybridStrategy %q: must be alpha or rrf", p.HybridStrategy)
	}

	if p.TopK < 0 || p.TopK > maxProfileTopK {
		return fmt.Errorf("invalid retrieval.topK %d: must be between 0 (unset) and %d", p.TopK, maxProfileTopK)
	}
	if p.MaxTopK < 0 || p.MaxTopK > searchMaxLimitCeiling {
		return fmt.Errorf("invalid retrieval.maxTopK %d: must be between 0 (unset) and %d", p.MaxTopK, searchMaxLimitCeiling)
	}
	if p.MaxTopK > 0 && p.TopK > p.MaxTopK {
		return fmt.Errorf("invalid retrieval.topK %d: above the collection's maxTopK %d", p.TopK, p.MaxTopK)
	}
	if p.HybridAlphaSet && (p.HybridAlpha < 0 || p.HybridAlpha > 1) {
		return errors.New("invalid retrieval.hybridAlpha: must be between 0.0 and 1.0")
	}
	if err := ValidateOversample(p.Oversample); err != nil {
		return fmt.Errorf("invalid retrieval.%w", err)
	}
	if !validVectorAlgorithm(p.VectorAlgorithm) {
		return fmt.Errorf("invalid retrieval.vectorAlgorithm %q: %w", p.VectorAlgorithm, errInvalidVectorAlgorithm)
	}
	if err := p.GroupBy.Validate(); err != nil {
		return fmt.Errorf("invalid retrieval.%w", err)
	}
	if err := validateSummarySettings(p.SummaryMode, p.SummaryWeight); err != nil {
		return fmt.Errorf("invalid retrieval.%w", err)
	}
	if err := (PassageOptions{Format: p.Format}).ValidatePassageOptions(); err != nil {
		return fmt.Errorf("invalid retrieval.%w", err)
	}
	if err := (fts.BM25Params{K1: floatOr(p.BM25K1, fts.DefaultBM25.K1), B: floatOr(p.BM25B, fts.DefaultBM25.B)}).Validate(); err != nil {
		return fmt.Errorf("invalid retrieval.%w", err)
	}
	if p.ContextTokenBudget < 0 || p.ContextTokenBudget > maxProfileContextTokenBudget {
		return fmt.Errorf("invalid retrieval.contextTokenBudget %d: must be between 0 (unset) and %d",
			p.ContextTokenBudget, maxProfileContextTokenBudget)
	}
	return nil
}

// RetrievalProfile returns the profile configured for a collection, or nil.
//
// One accessor, so no search path has to know how configs are stored or
// remember to nil-check the manager.
func (s *Server) RetrievalProfile(collection string) *RetrievalProfileDef {
	if s == nil || s.CollectionManager == nil || collection == "" {
		return nil
	}
	cfg, found := s.CollectionManager.Get(collection)
	if !found || cfg == nil {
		return nil
	}
	return cfg.Retrieval
}

// ResolveTopK applies the precedence rule to a result limit, then the
// server's cap (#368).
//
// requested is what the caller asked for (0 = did not ask); fallback is the
// path's own historical default, kept so a collection without a profile
// produces exactly the results it produced before. The result is never above
// CapLimit's ceiling, and the report says when it was lowered to it.
func (s *Server) ResolveTopK(collection string, requested, fallback int) (int, LimitReport) {
	n := fallback
	if requested > 0 {
		n = requested
	} else if p := s.RetrievalProfile(collection); p != nil && p.TopK > 0 {
		n = p.TopK
	}
	return s.CapLimit(collection, n, 0)
}

// ResolveRetrievalMode applies the precedence rule to granularity.
func (s *Server) ResolveRetrievalMode(collection, requested, fallback string) string {
	if requested != "" {
		return requested
	}
	if p := s.RetrievalProfile(collection); p != nil && p.RetrievalMode != "" {
		return p.RetrievalMode
	}
	return fallback
}

// ResolveHybridStrategy applies the precedence rule to fusion strategy.
func (s *Server) ResolveHybridStrategy(collection, requested, fallback string) string {
	if requested != "" {
		return requested
	}
	if p := s.RetrievalProfile(collection); p != nil && p.HybridStrategy != "" {
		return p.HybridStrategy
	}
	return fallback
}

// ResolveHybridAlpha applies the precedence rule to the fusion weight.
//
// requestedSet carries whether the caller sent alpha at all, because 0.0 is a
// legitimate request (pure keyword) and cannot double as "unset".
func (s *Server) ResolveHybridAlpha(collection string, requested float64, requestedSet bool, fallback float64) float64 {
	if requestedSet {
		return requested
	}
	if p := s.RetrievalProfile(collection); p.HasHybridAlpha() {
		return p.HybridAlpha
	}
	return fallback
}

// ContextTokenBudget returns the collection's context cap, 0 when unset.
func (s *Server) ContextTokenBudget(collection string) int {
	if p := s.RetrievalProfile(collection); p != nil {
		return p.ContextTokenBudget
	}
	return 0
}

// ResolveGroupBy applies the precedence rule to result grouping (SRCH-016):
// the request's groupBy, else the collection profile's, else none.
func (s *Server) ResolveGroupBy(collection string, requested *GroupBy) *GroupBy {
	if requested != nil {
		return requested
	}
	if p := s.RetrievalProfile(collection); p != nil {
		return p.GroupBy
	}
	return nil
}

// ResolveBM25 applies request > collection profile > default to each BM25
// parameter separately (SRCH-018).
func (s *Server) ResolveBM25(collection string, k1, b *float64) (fts.BM25Params, error) {
	p := fts.DefaultBM25
	if prof := s.RetrievalProfile(collection); prof != nil {
		p.K1, p.B = floatOr(prof.BM25K1, p.K1), floatOr(prof.BM25B, p.B)
	}
	p.K1, p.B = floatOr(k1, p.K1), floatOr(b, p.B)
	return p, p.Validate()
}

// floatOr is *v, or fallback when v is nil.
func floatOr(v *float64, fallback float64) float64 {
	if v == nil {
		return fallback
	}
	return *v
}

// --- proto conversion (RAG-001) ---

// retrievalProfileFromProto converts a gRPC retrieval block into the stored
// form. A nil message means "not sent" and is handled by the caller.
func retrievalProfileFromProto(p *proto.RetrievalProfileProto) *RetrievalProfileDef {
	if p == nil {
		return nil
	}
	return &RetrievalProfileDef{
		Oversample:         p.Oversample,
		DefaultSearchType:  p.DefaultSearchType,
		TopK:               int(p.TopK),
		RetrievalMode:      p.RetrievalMode,
		HybridStrategy:     p.HybridStrategy,
		HybridAlpha:        p.HybridAlpha,
		HybridAlphaSet:     p.HybridAlphaSet,
		ContextTokenBudget: int(p.ContextTokenBudget),
		GroupBy:            groupByFromProto(p.GroupByKey, p.GroupByPerGroup),
		Format:             p.Format,
		BM25K1:             p.Bm25K1,
		BM25B:              p.Bm25B,
		VectorAlgorithm:    p.VectorAlgorithm,
		SummaryMode:        p.SummaryMode,
		SummaryWeight:      p.SummaryWeight,
		MaxTopK:            int(p.MaxTopK),
	}
}

func groupByFromProto(key string, perGroup int32) *GroupBy {
	if key == "" {
		return nil
	}
	return &GroupBy{Key: key, PerGroup: int(perGroup)}
}

// retrievalProfileToProto converts a stored profile for the wire, nil for nil —
// so a collection without a profile reports one absent field rather than a
// block of zeros a client would have to know to ignore.
func retrievalProfileToProto(p *RetrievalProfileDef) *proto.RetrievalProfileProto {
	if p == nil {
		return nil
	}
	return &proto.RetrievalProfileProto{
		Oversample:         p.Oversample,
		DefaultSearchType:  p.DefaultSearchType,
		TopK:               safeInt32(p.TopK),
		RetrievalMode:      p.RetrievalMode,
		HybridStrategy:     p.HybridStrategy,
		HybridAlpha:        p.HybridAlpha,
		HybridAlphaSet:     p.HybridAlphaSet,
		ContextTokenBudget: safeInt32(p.ContextTokenBudget),
		GroupByKey:         groupByKey(p.GroupBy),
		GroupByPerGroup:    groupByPerGroup(p.GroupBy),
		Format:             p.Format,
		Bm25K1:             p.BM25K1,
		Bm25B:              p.BM25B,
		VectorAlgorithm:    p.VectorAlgorithm,
		SummaryMode:        p.SummaryMode,
		SummaryWeight:      p.SummaryWeight,
		MaxTopK:            safeInt32(p.MaxTopK),
	}
}

func groupByKey(g *GroupBy) string {
	if g == nil {
		return ""
	}
	return g.Key
}

func groupByPerGroup(g *GroupBy) int32 {
	if g == nil {
		return 0
	}
	return safeInt32(g.PerGroup)
}
