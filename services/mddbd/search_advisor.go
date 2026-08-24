package main

import (
	"fmt"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// Recommending how to search a collection (SRCH-010).
//
// MDDB offers eight vector algorithms, four ranking algorithms, three fusion
// strategies and four retrieval modes. An agent connecting over MCP has no
// basis for choosing among them: it sees a menu and picks the default, or
// guesses from the names. Every algorithm added since makes the menu longer
// and the choice no easier.
//
// The collection itself has the answer, and the server can read it. Whether
// embeddings exist decides whether vector search is even possible. Document
// length decides whether chunking is worth it. Vocabulary decides whether
// keyword search can discriminate at all. The presence of `defines`/`uses`
// symbols says this is code, where the connection graph reaches documents no
// score can. Near-duplicates say the diversity signal will earn its keep.
//
// So the recommendation is measured, not configured — and it comes with its
// reasons, because "use bm25" is an instruction and "use bm25, because your
// documents average 90 words and short documents are where length
// normalisation matters" is something a caller can disagree with.
//
// This does not choose for anyone. It answers a question, and a caller may
// write the answer into the collection's retrieval profile (RAG-001) or
// ignore it.

// CollectionProfile is what the advisor measured.
type CollectionProfile struct {
	Collection string `json:"collection"`
	Documents  int    `json:"documents"`
	// EmbeddedDocuments is how many have a vector. Zero means vector and
	// hybrid search are unavailable regardless of what anyone configures.
	EmbeddedDocuments int `json:"embeddedDocuments"`
	VectorDimensions  int `json:"vectorDimensions,omitempty"`

	// MedianWords is the median document length. The median rather than the
	// mean: one 400-page manual among ten thousand notes moves a mean and
	// tells you nothing about the collection.
	MedianWords int `json:"medianWords"`
	// LongDocumentRatio is the share of documents above 500 words — the
	// length where returning a whole document starts wasting a prompt.
	LongDocumentRatio float64 `json:"longDocumentRatio"`

	// DistinctTerms is the vocabulary size across the sample. A collection
	// whose documents all use the same fifty words cannot be discriminated by
	// keyword search, however good the ranking algorithm.
	DistinctTerms int `json:"distinctTerms"`

	// TermsPerDocument is the vocabulary divided by the number of documents
	// sampled — how much new vocabulary each document contributes.
	//
	// This, and not the type-token ratio, is what decides whether keyword
	// search can discriminate. Type-token ratio over a pooled corpus falls
	// towards zero as the corpus grows, because vocabulary saturates while
	// tokens keep accumulating: the same text measured across 100 documents
	// and across 100 000 gives wildly different numbers. Every large
	// collection would look repetitive, and the advisor would recommend query
	// expansion for all of them.
	TermsPerDocument float64 `json:"termsPerDocument"`

	// TypeTokenRatio is distinct terms over total terms across the sample.
	// Reported because it is a familiar number, but not used for any decision
	// — see TermsPerDocument for why.
	TypeTokenRatio float64 `json:"typeTokenRatio"`

	// CodeDocuments is how many carry extracted symbols (CODE-004), which is
	// what makes the connection graph and `retrievalMode: "graph"` useful.
	CodeDocuments int `json:"codeDocuments"`

	// EstimatedVectorBytes is what the vectors occupy at float32 — the number
	// that decides whether a quantized index is worth the recall it costs.
	EstimatedVectorBytes int64 `json:"estimatedVectorBytes,omitempty"`

	// Sampled reports how many documents were actually read. A recommendation
	// from a sample should say so.
	Sampled int `json:"sampled"`
}

// SearchRecommendation is the advice, with the reasons for it.
type SearchRecommendation struct {
	Profile CollectionProfile `json:"profile"`

	// SearchType is what to call: "fts", "vector" or "hybrid".
	SearchType string `json:"searchType"`
	// FTSAlgorithm is the ranking algorithm for keyword search.
	FTSAlgorithm string `json:"ftsAlgorithm,omitempty"`
	// VectorAlgorithm is the index to search.
	VectorAlgorithm string `json:"vectorAlgorithm,omitempty"`
	// HybridStrategy and HybridAlpha apply when SearchType is "hybrid".
	HybridStrategy string  `json:"hybridStrategy,omitempty"`
	HybridAlpha    float64 `json:"hybridAlpha,omitempty"`
	// RetrievalMode is the shape of a result.
	RetrievalMode string `json:"retrievalMode,omitempty"`
	// Signals suggests fusion weights when the collection warrants them.
	Signals *FusionSignals `json:"signals,omitempty"`
	// TopK is a starting point, not a rule.
	TopK int `json:"topK,omitempty"`

	// Reasons explains every choice above, one line each. This is the part
	// worth reading: a recommendation nobody can argue with is a
	// recommendation nobody should follow.
	Reasons []string `json:"reasons"`
	// Warnings names what would stop the recommendation working.
	Warnings []string `json:"warnings,omitempty"`

	// Profile is the retrieval profile that would encode this advice, ready to
	// PUT to /v1/collection-config (RAG-001).
	RetrievalProfile *RetrievalProfileDef `json:"retrievalProfile,omitempty"`
}

// advisorSampleLimit is how many documents the profile reads.
//
// A sample, not a scan: the shape of a collection is visible in a few thousand
// documents, and an advisor that reads a million to answer one question is an
// advisor nobody calls twice.
const advisorSampleLimit = 2000

// longDocumentWords is where a document stops fitting comfortably in a prompt
// alongside several others.
const longDocumentWords = 500

// repetitiveTermsPerDocument is how much new vocabulary a document must
// contribute before keyword ranking can tell documents apart.
//
// Two. English prose contributes ten or more even in a large corpus; a
// collection of templated status lines contributes a fraction of one. Below
// this the documents share nearly all their words, exact term matching has
// almost nothing to rank on, and query expansion is what recovers recall.
const repetitiveTermsPerDocument = 2.0

// RecommendSearch measures a collection and says how to search it.
func (s *Server) RecommendSearch(collection string) (*SearchRecommendation, error) {
	if collection == "" {
		return nil, fmt.Errorf("collection is required")
	}

	profile, err := s.profileCollection(collection)
	if err != nil {
		return nil, err
	}
	if profile.Documents == 0 {
		return &SearchRecommendation{
			Profile:    *profile,
			SearchType: "fts",
			Reasons:    []string{"The collection is empty, so there is nothing to measure. Load documents and ask again."},
		}, nil
	}

	return buildRecommendation(profile, s.Embedding != nil), nil
}

// profileCollection reads a sample and measures its shape.
func (s *Server) profileCollection(collection string) (*CollectionProfile, error) {
	p := &CollectionProfile{Collection: collection}

	lengths := make([]int, 0, advisorSampleLimit)
	terms := make(map[string]struct{})
	totalTerms := 0

	err := s.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			return nil
		}
		prefix := []byte("doc|" + collection + "|")
		cur := b.Cursor()
		for k, v := cur.Seek(prefix); k != nil && hasPrefixBytes(k, prefix); k, v = cur.Next() {
			p.Documents++
			if p.Sampled >= advisorSampleLimit {
				continue
			}

			doc, err := loadDoc(v)
			if err != nil || doc == nil {
				continue
			}
			p.Sampled++

			words := strings.Fields(doc.ContentMD)
			lengths = append(lengths, len(words))
			totalTerms += len(words)
			for _, w := range words {
				// Lowercased, so "Deploy" and "deploy" are one term — which is
				// what the FTS tokeniser does, and the point is to predict how
				// well that tokeniser will discriminate.
				terms[strings.ToLower(w)] = struct{}{}
			}

			if len(doc.Meta["defines"]) > 0 || len(doc.Meta["uses"]) > 0 || len(doc.Meta["imports"]) > 0 {
				p.CodeDocuments++
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", collection, err)
	}

	if len(lengths) > 0 {
		sort.Ints(lengths)
		p.MedianWords = lengths[len(lengths)/2]

		long := 0
		for _, n := range lengths {
			if n > longDocumentWords {
				long++
			}
		}
		p.LongDocumentRatio = float64(long) / float64(len(lengths))
	}

	p.DistinctTerms = len(terms)
	if totalTerms > 0 {
		p.TypeTokenRatio = float64(len(terms)) / float64(totalTerms)
	}
	if p.Sampled > 0 {
		p.TermsPerDocument = float64(len(terms)) / float64(p.Sampled)
	}

	s.profileVectors(collection, p)
	return p, nil
}

// profileVectors counts what is embedded.
//
// Read separately from the documents because the two can disagree — a
// collection loaded with skipEmbeddings has documents and no vectors, and that
// difference is exactly what decides whether vector search is available.
func (s *Server) profileVectors(collection string, p *CollectionProfile) {
	if s.VectorIndex == nil {
		return
	}
	p.EmbeddedDocuments = s.VectorIndex.CollectionSize(collection)
	if p.EmbeddedDocuments == 0 {
		return
	}
	if s.Embedding != nil {
		p.VectorDimensions = s.Embedding.Dimensions()
	}
	if p.VectorDimensions > 0 {
		p.EstimatedVectorBytes = int64(p.EmbeddedDocuments) * int64(p.VectorDimensions) * 4
	}
}

// buildRecommendation turns measurements into advice.
//
// Split from the measuring so it can be tested against a profile without a
// database, and so the rules are all in one place where they can be read
// together rather than discovered one branch at a time.
func buildRecommendation(p *CollectionProfile, embeddingConfigured bool) *SearchRecommendation {
	r := &SearchRecommendation{Profile: *p}

	embedded := p.EmbeddedDocuments > 0
	coverage := 0.0
	if p.Documents > 0 {
		coverage = float64(p.EmbeddedDocuments) / float64(p.Documents)
	}

	// --- what kind of search ---

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

	// --- keyword ranking ---

	switch {
	case p.CodeDocuments > p.Sampled/2 && p.Sampled > 0:
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

	// --- vector index ---

	if embedded {
		const gib = 1 << 30
		switch {
		case p.EstimatedVectorBytes > gib:
			r.VectorAlgorithm = "sq4"
			r.Reasons = append(r.Reasons, fmt.Sprintf(
				"sq4 for the vector index: %.1f GB of float32 vectors will not sit in RAM comfortably, and sq4 holds 99.5%% of int8's recall at half its size.",
				float64(p.EstimatedVectorBytes)/gib))
		case p.EstimatedVectorBytes > 256<<20:
			r.VectorAlgorithm = "sq"
			r.Reasons = append(r.Reasons, fmt.Sprintf(
				"sq for the vector index: %.0f MB of vectors is enough to be worth quantizing, and int8 is effectively lossless.",
				float64(p.EstimatedVectorBytes)/(1<<20)))
		case p.EmbeddedDocuments > 50000:
			r.VectorAlgorithm = "hnsw"
			r.Reasons = append(r.Reasons, fmt.Sprintf(
				"hnsw for the vector index: at %d vectors an exact scan starts costing more than the recall it buys.",
				p.EmbeddedDocuments))
		default:
			r.VectorAlgorithm = "flat"
			r.Reasons = append(r.Reasons, fmt.Sprintf(
				"flat for the vector index: %d vectors is small enough that exact search is both fastest and most accurate.",
				p.EmbeddedDocuments))
		}
	}

	// --- fusion ---

	if r.SearchType == "hybrid" {
		r.HybridStrategy = "alpha"
		switch {
		case p.CodeDocuments > p.Sampled/2 && p.Sampled > 0:
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

	// --- result shape ---

	switch {
	case p.CodeDocuments > p.Sampled/2 && p.Sampled > 0:
		r.RetrievalMode = "graph"
		r.Reasons = append(r.Reasons,
			"retrievalMode graph: this is code, so the connection graph reaches the stylesheet a matching script touches — a document no score would have found.")
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

	// --- topK ---

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

	// --- signals ---

	if r.SearchType == "hybrid" && p.Documents > 100 {
		r.Signals = &FusionSignals{Diversity: 0.6}
		r.Reasons = append(r.Reasons,
			"A diversity weight of 0.6: worth having on any collection large enough to contain a document that was copied and edited, which is most of them.")
	}

	r.RetrievalProfile = r.toProfile()
	return r
}

// toProfile renders the recommendation as a retrieval profile (RAG-001), ready
// to store on the collection.
func (r *SearchRecommendation) toProfile() *RetrievalProfileDef {
	p := &RetrievalProfileDef{
		DefaultSearchType: r.SearchType,
		TopK:              r.TopK,
		RetrievalMode:     r.RetrievalMode,
	}
	if r.SearchType == "hybrid" {
		p.HybridStrategy = r.HybridStrategy
		p.HybridAlpha = r.HybridAlpha
		p.HybridAlphaSet = true
	}
	return p
}

// storage.Doc is referenced through loadDoc; the import keeps the dependency
// explicit for readers of this file.
var _ = storage.Doc{}
