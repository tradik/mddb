package main

import (
	"errors"

	json "mddb/internal/jsonx"
	"strings"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// Passage-first results (AGT-002).
//
// `format: "passages"` on a search returns places instead of documents: for
// each hit, its path, a line range and the text of those lines, the definition
// holding it, its score and its size. One shape from FTS, vector, hybrid and
// cross-collection search, built here by every one of them.
//
// Where the passage is:
//   - chunk and window modes: the chunk, as those modes already cut it;
//   - parent mode, vector: the document's best-scoring chunk;
//   - otherwise: around the first place the query's terms match (the same
//     highlighter FTS uses), else the start of the document. Inside a
//     definition no longer than the window, the definition itself, so a hit
//     in a CSS rule or a short function returns exactly that.

// Result formats.
const (
	FormatResults  = "results"
	FormatPassages = "passages"

	defaultPassageLines = 10
	maxPassageLines     = 200
)

// PassageOptions selects the passage format on a search request.
type PassageOptions struct {
	// Format is "results" (default: today's shape) or "passages".
	Format string `json:"format,omitempty"`
	// PassageLines is the context kept on each side of a hit in parent mode
	// (default 10, max 200).
	PassageLines int `json:"passageLines,omitempty"`
}

// ValidatePassageOptions rejects an unknown format or a negative window.
func (o PassageOptions) ValidatePassageOptions() error {
	switch o.Format {
	case "", FormatResults, FormatPassages:
	default:
		return errors.New("format must be results or passages")
	}
	if o.PassageLines < 0 {
		return errors.New("passageLines must be 0 or more")
	}
	return nil
}

func (o PassageOptions) wantsPassages() bool { return o.Format == FormatPassages }

func (o PassageOptions) radius() int {
	if o.PassageLines <= 0 {
		return defaultPassageLines
	}
	return min(o.PassageLines, maxPassageLines)
}

// PassagesResponse is what a search returns with format "passages".
type PassagesResponse struct {
	Passages         []Passage        `json:"passages"`
	Total            int              `json:"total"`
	ContextTruncated bool             `json:"contextTruncated,omitempty"`
	Indexing         *IndexWaitResult `json:"indexing,omitempty"`
	// ImageHits are the images the query matched by their picture, with
	// includeImages and a provider that embeds images (RAG-010).
	ImageHits   []ImageRef `json:"imageHits,omitempty"`
	LimitReport            // limit, limitCapped: set when the server's cap lowered the request's limit (#368)
}

// ResolveFormat applies the precedence rule to the result format: the
// request's, else the collection profile's, else "results".
func (s *Server) ResolveFormat(collection string, o PassageOptions) PassageOptions {
	if o.Format == "" {
		if p := s.RetrievalProfile(collection); p != nil {
			o.Format = p.Format
		}
	}
	return o
}

// finishPassages applies the collection's context budget to passages, which
// is what the caller receives in this format, and wraps them. Every passage
// response passes here, so this is where passages are counted (AGT-004).
func (s *Server) finishPassages(collection string, passages []Passage, indexing *IndexWaitResult) PassagesResponse {
	if passages == nil {
		passages = []Passage{}
	}
	passages, truncated := applyContextBudget(s, collection, passages, func(p Passage) int { return p.Tokens })
	if s.Gain != nil {
		s.Gain.Hits(passageHits(passages))
	}
	return PassagesResponse{Passages: passages, Total: len(passages), ContextTruncated: truncated, Indexing: indexing}
}

// passageAtHit builds the passage around a document's best hit: the first
// line a term matches, else line 1. A highlight's line is where its fragment
// starts, padded with context, so the hit is located from the terms.
func passageAtHit(collection string, doc *storage.Doc, terms []string, highlights []fts.Highlight, opts PassageOptions, score float64, source string) Passage {
	for _, h := range highlights {
		terms = append(terms, h.MatchedTerms...)
	}
	return passageAround(collection, doc, hitLine(doc.ContentMD, terms), opts.radius(), score, source)
}

// hitLine picks the line where the most distinct terms meet, counting each
// line together with the two on either side, so a passage lands in the
// function that is about the query rather than on the first import that
// shares a word with it. Whole-word matches are tried first, then parts of
// words (a stem matches "classes" by "class"); a fuzzy match "query~indexed"
// is looked up by its indexed form. Ties go to the earlier line.
func hitLine(content string, terms []string) int {
	var clean []string
	for _, t := range terms {
		if i := strings.LastIndexByte(t, '~'); i >= 0 {
			t = t[i+1:]
		}
		if t = strings.ToLower(t); t != "" && !containsString(clean, t) {
			clean = append(clean, t)
		}
	}
	lines := strings.Split(strings.ToLower(content), "\n")
	for _, wholeWord := range []bool{true, false} {
		hits := make([][]bool, len(lines)) // hits[line][term]
		any := false
		for li, line := range lines {
			for ti, t := range clean {
				if (wholeWord && containsWord(line, t)) || (!wholeWord && strings.Contains(line, t)) {
					if hits[li] == nil {
						hits[li] = make([]bool, len(clean))
					}
					hits[li][ti], any = true, true
				}
			}
		}
		if !any {
			continue
		}
		best, bestScore := 0, -1
		for li := range lines {
			if hits[li] == nil {
				continue // a passage is centred on a line that matches
			}
			seen := make([]bool, len(clean))
			score := 0
			for k := max(li-2, 0); k <= min(li+2, len(lines)-1); k++ {
				for ti, h := range hits[k] {
					if h && !seen[ti] {
						seen[ti] = true
						score++
					}
				}
			}
			if score > bestScore {
				best, bestScore = li, score
			}
		}
		return best + 1
	}
	return 1
}

// passageAround is the window of radius lines on each side of hit, or the
// definition holding hit when that is no longer than the window.
func passageAround(collection string, doc *storage.Doc, hit, radius int, score float64, source string) Passage {
	st := newSourceText(doc)
	start, end := max(hit-radius, 1), min(hit+radius, len(st.lines))
	if sp := st.enclosing(hit); sp != nil && sp.EndLine-sp.StartLine <= 2*radius {
		start, end = sp.StartLine, sp.EndLine
	}
	p := st.passage(collection, start, end, source, true)
	if sp := st.enclosing(hit); sp != nil {
		p.Symbol = sp.Symbol
	}
	p.Score = score
	return p
}

// passageOfChunk wraps a chunk or window a search already cut.
func passageOfChunk(collection string, doc *storage.Doc, text string, startLine, endLine int, score float64, source string) Passage {
	st := newSourceText(doc)
	p := st.passage(collection, startLine, endLine, source, false)
	p.Content, p.Tokens, p.Score = text, approxTokens(text), score
	return p
}

// passageOfChunkIndex cuts chunk i of a document, as the collection chunks it.
func (s *Server) passageOfChunkIndex(collection string, doc *storage.Doc, i int, score float64, source string) Passage {
	text, start, end := chunkPassageWithLinesSize(doc.ContentMD, i, 0, s.chunkModeFor(collection, doc), s.chunkSizeFor(collection))
	return passageOfChunk(collection, doc, text, start, end, score, source)
}

// queryTerms are the words of a query, for locating it in a document that
// was found by its vector.
func queryTerms(query string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ToLower(query)) {
		if f = strings.Trim(f, `"'.,;:!?()[]{}`); len(f) > 1 {
			out = append(out, f)
		}
	}
	return out
}

// passageSource is one search hit, whatever the surface, as passage building
// needs it. Surfaces map their own result types onto it.
type passageSource struct {
	doc       storage.Doc
	score     float64
	chunkText string // set when the search already cut a chunk or window
	startLine int
	endLine   int
	bestChunk int // parent-mode vector hit: its best chunk; -1 when unknown
	// chunks are summaryMode expand's chunks for a document its summary
	// matched (RAG-009): one passage each, in place of the one best chunk.
	chunks     []int
	highlights []fts.Highlight
	terms      []string
}

// buildPassages turns hits into passages, one per hit, in order.
func (s *Server) buildPassages(collection string, hits []passageSource, opts PassageOptions, source string) []Passage {
	out := make([]Passage, 0, len(hits))
	for i := range hits {
		h := &hits[i]
		switch {
		case h.chunkText != "":
			out = append(out, passageOfChunk(collection, &h.doc, h.chunkText, h.startLine, h.endLine, h.score, source))
		case len(h.chunks) > 0:
			for _, c := range h.chunks {
				out = append(out, s.passageOfChunkIndex(collection, &h.doc, c, h.score, source))
			}
		case h.bestChunk >= 0:
			out = append(out, s.passageOfChunkIndex(collection, &h.doc, h.bestChunk, h.score, source))
		default:
			out = append(out, passageAtHit(collection, &h.doc, h.terms, h.highlights, opts, h.score, source))
		}
	}
	return out
}

// mcpDocumentToDoc is the reverse of docToMCPDocument, for the fields a
// passage reads.
func mcpDocumentToDoc(d MCPDocument) storage.Doc {
	return storage.Doc{ID: d.ID, Key: d.Key, Lang: d.Lang, Meta: d.Meta, ContentMD: d.ContentMD}
}

// mcpGetPassageOptions reads format and passage_lines (AGT-002).
func mcpGetPassageOptions(m map[string]interface{}) PassageOptions {
	return PassageOptions{Format: mcpGetString(m, "format"), PassageLines: mcpGetInt(m, "passage_lines")}
}

// mcpPassagesJSON renders a passages response the way tools render results.
func mcpPassagesJSON(p *PassagesResponse) string {
	data, _ := json.MarshalIndent(p, "", "  ")
	return string(data)
}

// hybridPassages builds passages from hybrid results, located by the terms
// the full-text half matched, or by the query's words for a result only the
// vector half found. Shared by REST and MCP hybrid search.
func (s *Server) hybridPassages(collection, query string, items []HybridSearchResultItem, opts PassageOptions, indexing *IndexWaitResult, plan *SearchPlan) PassagesResponse {
	words := queryTerms(query)
	hits := make([]passageSource, 0, len(items))
	for _, it := range items {
		terms := it.MatchedTerms
		if len(terms) == 0 {
			terms = words
		}
		hits = append(hits, passageSource{doc: it.Document, score: it.CombinedScore, bestChunk: -1, terms: terms,
			chunks: plan.expandedChunks(it.Document.ID)})
	}
	return s.finishPassages(collection, s.buildPassages(collection, hits, opts, "hybrid"), indexing)
}

// crossPassages builds passages from cross-collection results, each in its
// own collection. There is no single collection profile, so no budget.
func (s *Server) crossPassages(query string, items []CrossSearchResultItem, opts PassageOptions, indexing *IndexWaitResult) PassagesResponse {
	terms := queryTerms(query)
	out := make([]Passage, 0, len(items))
	for _, it := range items {
		out = append(out, s.buildPassages(it.Collection, []passageSource{{doc: it.Document, score: float64(it.Score), bestChunk: -1, terms: terms, chunks: it.chunks}}, opts, "cross")...)
	}
	return s.finishPassages("", out, indexing)
}
