package main

import (
	"fmt"
	"strings"
)

// The search advisor's rules (SRCH-010): from a measured CollectionProfile to
// a SearchRecommendation, one decision per function so the rules can be read
// together. The measuring is in search_advisor.go.

// buildRecommendation turns measurements into advice.
//
// Split from the measuring so it can be tested against a profile without a
// database, and so the rules are all in one place where they can be read
// together rather than discovered one branch at a time.
func buildRecommendation(p *CollectionProfile, embeddingConfigured bool) *SearchRecommendation {
	r := &SearchRecommendation{Profile: *p}

	adviseSearchType(r, p, embeddingConfigured)
	adviseKeywordRanking(r, p)
	adviseVectorIndex(r, p)
	adviseFusion(r, p)
	adviseResultShape(r, p)
	adviseTopK(r, p)
	adviseSummaries(r, p) // RAG-009
	adviseConversions(r, p)

	// --- signals ---

	if r.SearchType == "hybrid" && p.Documents > 100 {
		r.Signals = &FusionSignals{Diversity: 0.6}
		r.Reasons = append(r.Reasons,
			"A diversity weight of 0.6: worth having on any collection large enough to contain a document that was copied and edited, which is most of them.")
	}

	r.RetrievalProfile = r.toProfile()
	return r
}

// mostlyCode reports whether more than half of the sampled documents are
// code; several of the rules below advise differently for code.
func (p *CollectionProfile) mostlyCode() bool {
	return p.CodeDocuments > p.Sampled/2 && p.Sampled > 0
}

// adviseSearchType picks what kind of search: keyword only, or hybrid.
func adviseSearchType(r *SearchRecommendation, p *CollectionProfile, embeddingConfigured bool) {
	embedded := p.EmbeddedDocuments > 0
	coverage := 0.0
	if p.Documents > 0 {
		coverage = float64(p.EmbeddedDocuments) / float64(p.Documents)
	}
	switch {
	case !embedded && !embeddingConfigured:
		r.SearchType = "fts"
		r.Reasons = append(r.Reasons,
			"No embedding provider is configured and no document has a vector, so keyword search is the only search available.")
	case !embedded:
		r.SearchType = "fts"
		r.Warnings = append(r.Warnings,
			"An embedding provider is configured but this collection has no vectors. Run a vector reindex to make hybrid search possible.")
		r.Reasons = append(r.Reasons,
			"Keyword search for now: the provider is configured but nothing in this collection is embedded yet.")
	case p.Semantic != nil && !*p.Semantic:
		r.SearchType = "fts"
		r.Reasons = append(r.Reasons, lexicalAdvice)
	case coverage < 0.8:
		r.SearchType = "hybrid"
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"Only %.0f%% of documents are embedded; the rest can only be found by keyword. Reindex before relying on vector recall.",
			coverage*100))
		r.Reasons = append(r.Reasons,
			"Hybrid, so the documents without vectors are still reachable by keyword.")
	default:
		r.SearchType = "hybrid"
		r.Reasons = append(r.Reasons,
			"Hybrid: the collection is fully embedded, and blending keyword with vector beats either alone on almost every corpus.")
	}
}

// adviseKeywordRanking picks the full-text ranking algorithm.
func adviseKeywordRanking(r *SearchRecommendation, p *CollectionProfile) {
	switch {
	case p.mostlyCode():
		r.FTSAlgorithm = "bm25"
		r.Reasons = append(r.Reasons,
			"bm25 for ranking: this is code, where queries are identifiers and exact term matching matters more than query expansion.")
	case p.MedianWords < 150:
		r.FTSAlgorithm = "bm25"
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"bm25 for ranking: a median document of %d words is short, and bm25's length normalisation is what stops a one-line note outranking a real answer.",
			p.MedianWords))
	case p.TermsPerDocument > 0 && p.TermsPerDocument < repetitiveTermsPerDocument:
		r.FTSAlgorithm = "pmisparse"
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"pmisparse for ranking: each document adds only %.1f new terms to the vocabulary, so the documents look alike to an exact-match ranker and query expansion is what recovers the matches it misses.",
			p.TermsPerDocument))
	default:
		r.FTSAlgorithm = "bm25"
		r.Reasons = append(r.Reasons,
			"bm25 for ranking: the general-purpose choice, and better than tf-idf on almost every corpus.")
	}
}

// adviseVectorIndex picks the vector index for a collection searched by
// vector. The thresholds are the query planner's (SRCH-013), so the advice
// and what algorithm "auto" picks cannot disagree.
func adviseVectorIndex(r *SearchRecommendation, p *CollectionProfile) {
	if p.EmbeddedDocuments <= 0 || r.SearchType == "fts" {
		return
	}
	algo, why := vectorTier(p.EmbeddedDocuments, p.EstimatedVectorBytes)
	r.VectorAlgorithm = algo
	r.Reasons = append(r.Reasons, fmt.Sprintf("%s for the vector index: %s.", algo, why))
	if len(p.BuiltIndexes) > 1 {
		r.autoVector = true
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"vectorAlgorithm auto in the profile: %d indexes are built for this collection (%s), so the query planner can choose per query — the index above for a plain query, an exact scan for a selective filter.",
			len(p.BuiltIndexes), strings.Join(p.BuiltIndexes, ", ")))
	}
}

// adviseFusion weighs keyword against vector for a hybrid search.
func adviseFusion(r *SearchRecommendation, p *CollectionProfile) {
	if r.SearchType != "hybrid" {
		return
	}
	r.HybridStrategy = "alpha"
	switch {
	case p.mostlyCode():
		r.HybridAlpha = 0.3
		r.Reasons = append(r.Reasons,
			"alpha 0.3, weighted towards keywords: code queries are identifiers, and an identifier either matches or does not.")
	case p.MedianWords > 300:
		r.HybridAlpha = 0.7
		r.Reasons = append(r.Reasons,
			"alpha 0.7, weighted towards meaning: long prose is asked about in questions, not in its own words.")
	default:
		r.HybridAlpha = 0.5
		r.Reasons = append(r.Reasons, "alpha 0.5: no reason in the data to favour either side.")
	}
}

// adviseResultShape picks what a hit returns: graph, chunk or parent, and
// passages for code.
func adviseResultShape(r *SearchRecommendation, p *CollectionProfile) {
	switch {
	case p.mostlyCode():
		r.RetrievalMode = "graph"
		r.Reasons = append(r.Reasons,
			"retrievalMode graph: this is code, so the connection graph reaches the stylesheet a matching script touches — a document no score would have found.")
		r.Format = FormatPassages
		r.Reasons = append(r.Reasons,
			"format passages: for code, a path, a line range and the definition holding it answer where to edit; the whole file does not.")
	case p.LongDocumentRatio > 0.3:
		r.RetrievalMode = "chunk"
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"retrievalMode chunk: %.0f%% of documents are over %d words, and returning whole ones would spend a prompt on paragraphs nobody asked about.",
			p.LongDocumentRatio*100, longDocumentWords))
	default:
		r.RetrievalMode = "parent"
		r.Reasons = append(r.Reasons,
			"retrievalMode parent: the documents are short enough to return whole.")
	}
}

// adviseTopK picks how many results to return from how long documents are.
func adviseTopK(r *SearchRecommendation, p *CollectionProfile) {
	switch {
	case p.MedianWords > 800:
		r.TopK = 5
		r.Reasons = append(r.Reasons, "topK 5: the documents are long, so few of them fill a context window.")
	case p.MedianWords < 100:
		r.TopK = 20
		r.Reasons = append(r.Reasons, "topK 20: the documents are short, so more of them fit and recall is cheap.")
	default:
		r.TopK = 10
	}
}

// toProfile renders the recommendation as a retrieval profile (RAG-001), ready
// to store on the collection.
func (r *SearchRecommendation) toProfile() *RetrievalProfileDef {
	p := &RetrievalProfileDef{
		DefaultSearchType: r.SearchType,
		TopK:              r.TopK,
		RetrievalMode:     r.RetrievalMode,
		Format:            r.Format,
	}
	if r.SearchType == "hybrid" {
		p.HybridStrategy = r.HybridStrategy
		p.HybridAlpha = r.HybridAlpha
		p.HybridAlphaSet = true
	}
	if r.autoVector {
		p.VectorAlgorithm = AlgorithmAuto
	}
	p.SummaryMode = r.SummaryMode
	return p
}

// summaryAdviceRatio is the share of documents with a summary from which
// summaryMode blend is advised: below it, most documents could only be found
// by their chunks, and blending would rank the few with a summary first.
const summaryAdviceRatio = 0.5

// adviseSummaries reports the share of documents with a summary, and advises
// blend when most have one and there are vectors to search them with.
func adviseSummaries(r *SearchRecommendation, p *CollectionProfile) {
	switch {
	case p.SummaryDocuments == 0:
		return
	case r.SearchType == "fts":
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"%.0f%% of documents carry a summary; with algorithm bm25f and summaryMode blend, keyword search weighs it as the meta.summary field.",
			p.SummaryRatio*100))
	case p.SummaryRatio >= summaryAdviceRatio:
		r.SummaryMode = SummaryModeBlend
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"summaryMode blend: %.0f%% of documents carry a summary, so a general question about a document can match its summary when no chunk answers it. Measure blend against expand with run_eval.",
			p.SummaryRatio*100))
	default:
		r.Reasons = append(r.Reasons, fmt.Sprintf(
			"summaryMode left off: only %.0f%% of documents carry a summary, and blending would favour those few over the rest.",
			p.SummaryRatio*100))
	}
}
