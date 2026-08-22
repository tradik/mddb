package main

import (
	"errors"
	"fmt"

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
	if p.HybridAlphaSet && (p.HybridAlpha < 0 || p.HybridAlpha > 1) {
		return errors.New("invalid retrieval.hybridAlpha: must be between 0.0 and 1.0")
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

// ResolveTopK applies the precedence rule to a result limit.
//
// requested is what the caller asked for (0 = did not ask); fallback is the
// path's own historical default, kept so a collection without a profile
// produces exactly the results it produced before.
func (s *Server) ResolveTopK(collection string, requested, fallback int) int {
	if requested > 0 {
		return requested
	}
	if p := s.RetrievalProfile(collection); p != nil && p.TopK > 0 {
		return p.TopK
	}
	return fallback
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

// --- proto conversion (RAG-001) ---

// retrievalProfileFromProto converts a gRPC retrieval block into the stored
// form. A nil message means "not sent" and is handled by the caller.
func retrievalProfileFromProto(p *proto.RetrievalProfileProto) *RetrievalProfileDef {
	if p == nil {
		return nil
	}
	return &RetrievalProfileDef{
		DefaultSearchType:  p.DefaultSearchType,
		TopK:               int(p.TopK),
		RetrievalMode:      p.RetrievalMode,
		HybridStrategy:     p.HybridStrategy,
		HybridAlpha:        p.HybridAlpha,
		HybridAlphaSet:     p.HybridAlphaSet,
		ContextTokenBudget: int(p.ContextTokenBudget),
	}
}

// retrievalProfileToProto converts a stored profile for the wire, nil for nil —
// so a collection without a profile reports one absent field rather than a
// block of zeros a client would have to know to ignore.
func retrievalProfileToProto(p *RetrievalProfileDef) *proto.RetrievalProfileProto {
	if p == nil {
		return nil
	}
	return &proto.RetrievalProfileProto{
		DefaultSearchType:  p.DefaultSearchType,
		TopK:               safeInt32(p.TopK),
		RetrievalMode:      p.RetrievalMode,
		HybridStrategy:     p.HybridStrategy,
		HybridAlpha:        p.HybridAlpha,
		HybridAlphaSet:     p.HybridAlphaSet,
		ContextTokenBudget: safeInt32(p.ContextTokenBudget),
	}
}
