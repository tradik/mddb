package fts

import "fmt"

// BM25Params are the two constants of the BM25 formula (SRCH-018). They are
// parameters of the scoring, not of the index, so a query can change them
// without a reindex.
//
// K1 is term-frequency saturation: how much a fifth occurrence still counts.
// B is length normalisation: 1 scores a term in a long document as weaker
// evidence, 0 ignores length.
type BM25Params struct {
	K1 float64 `json:"k1"`
	B  float64 `json:"b"`
}

// DefaultBM25 is Okapi BM25 as commonly used, and what MDDB always used.
var DefaultBM25 = BM25Params{K1: 1.2, B: 0.75}

// Bounds of BM25Params. K1 above 3 lets one repeated word dominate; B is a
// proportion.
const (
	maxBM25K1 = 3.0
)

// Validate rejects values that cannot mean anything.
func (p BM25Params) Validate() error {
	if p.K1 < 0 || p.K1 > maxBM25K1 {
		return fmt.Errorf("bm25K1 must be between 0 and %g, got %g", maxBM25K1, p.K1)
	}
	if p.B < 0 || p.B > 1 {
		return fmt.Errorf("bm25B must be between 0 and 1, got %g", p.B)
	}
	return nil
}

// termScore is one term's BM25 contribution to one document.
func (p BM25Params) termScore(idf, tf, dl, avgdl float64) float64 {
	return idf * (tf * (p.K1 + 1)) / (tf + p.K1*p.norm(dl, avgdl))
}

// norm is the length normalisation 1 - b + b*dl/avgdl.
func (p BM25Params) norm(dl, avgdl float64) float64 {
	return 1 - p.B + p.B*dl/avgdl
}
