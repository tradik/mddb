package main

import (
	"fmt"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/embedding"
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

	// SummaryDocuments is how many sampled documents carry a summary their
	// client wrote (RAG-009), and SummaryRatio their share of the sample:
	// what decides whether summaryMode can help at all.
	SummaryDocuments int     `json:"summaryDocuments"`
	SummaryRatio     float64 `json:"summaryRatio"`

	// ConvertedDocuments is how many sampled documents were converted from
	// a file and scored (GO-046); LowConfidenceDocuments how many of those
	// scored below 0.5, and LowConfidenceRatio their share of the sample.
	ConvertedDocuments     int     `json:"convertedDocuments,omitempty"`
	LowConfidenceDocuments int     `json:"lowConfidenceDocuments,omitempty"`
	LowConfidenceRatio     float64 `json:"lowConfidenceRatio,omitempty"`

	// EstimatedVectorBytes is what the vectors occupy at float32 — the number
	// that decides whether a quantized index is worth the recall it costs.
	EstimatedVectorBytes int64 `json:"estimatedVectorBytes,omitempty"`
	// BuiltIndexes names the vector indexes that can answer for the
	// collection now, without building (SRCH-013). More than one is what
	// makes algorithm "auto" worth having.
	BuiltIndexes []string `json:"builtIndexes,omitempty"`

	// Sampled reports how many documents were actually read. A recommendation
	// from a sample should say so.
	Sampled int `json:"sampled"`

	// Semantic says whether the collection's vectors encode meaning; absent
	// when nothing is embedded. False for the lexical provider (SRCH-014).
	Semantic *bool `json:"semantic,omitempty"`
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
	// Format is "passages" for code, where an agent wants places (AGT-002).
	Format string `json:"format,omitempty"`
	// Signals suggests fusion weights when the collection warrants them.
	Signals *FusionSignals `json:"signals,omitempty"`
	// TopK is a starting point, not a rule.
	TopK int `json:"topK,omitempty"`
	// SummaryMode is "blend" when most documents carry a summary (RAG-009).
	SummaryMode string `json:"summaryMode,omitempty"`

	// Reasons explains every choice above, one line each. This is the part
	// worth reading: a recommendation nobody can argue with is a
	// recommendation nobody should follow.
	Reasons []string `json:"reasons"`
	// Warnings names what would stop the recommendation working.
	Warnings []string `json:"warnings,omitempty"`

	// Profile is the retrieval profile that would encode this advice, ready to
	// PUT to /v1/collection-config (RAG-001).
	RetrievalProfile *RetrievalProfileDef `json:"retrievalProfile,omitempty"`
	// Measured is the evaluation-set run behind the choice, when the
	// collection has a set (SRCH-019).
	Measured *EvalResult `json:"measured,omitempty"`

	// autoVector: the profile should leave the index to the query planner
	// (SRCH-013).
	autoVector bool
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

	rec := buildRecommendation(profile, s.Embedding != nil)
	s.applyMeasurement(rec, collection)
	return rec, nil
}

// profileCollection reads a sample and measures its shape.
func (s *Server) profileCollection(collection string) (*CollectionProfile, error) {
	p := &CollectionProfile{Collection: collection}

	lengths := make([]int, 0, advisorSampleLimit)
	terms := make(map[string]struct{})
	totalTerms := 0

	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
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
			if documentSummary(doc) != "" {
				p.SummaryDocuments++
			}
			p.countConversion(doc.Meta)
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
		p.SummaryRatio = float64(p.SummaryDocuments) / float64(p.Sampled)
		p.LowConfidenceRatio = float64(p.LowConfidenceDocuments) / float64(p.Sampled)
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
		semantic := embedding.IsSemantic(s.Embedding)
		p.Semantic = &semantic
	}
	if p.VectorDimensions > 0 {
		p.EstimatedVectorBytes = int64(p.EmbeddedDocuments) * int64(p.VectorDimensions) * 4
	}
	for name := range s.planInput(planRequest{Collection: collection}).Built {
		p.BuiltIndexes = append(p.BuiltIndexes, name)
	}
	sort.Strings(p.BuiltIndexes)
}

// storage.Doc is referenced through loadDoc; the import keeps the dependency
// explicit for readers of this file.
var _ = storage.Doc{}
