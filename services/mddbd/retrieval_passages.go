package main

import (
	"errors"
	"unicode/utf8"

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
	// defaultPassageTokens bounds a passage cut around a hit. Twenty-one
	// lines of code stay under it; twenty-one lines of prose are paragraphs
	// and ran to thousands of tokens for one hit.
	defaultPassageTokens = 500
	maxPassageTokens     = 8000
	// cutMark ends a line cut to fit the bound.
	cutMark = " […]"
)

// PassageOptions selects the passage format on a search request.
type PassageOptions struct {
	// Format is "results" (default: today's shape) or "passages".
	Format string `json:"format,omitempty"`
	// PassageLines is the context kept on each side of a hit in parent mode
	// (default 10, max 200).
	PassageLines int `json:"passageLines,omitempty"`
	// PassageTokens is the most tokens one passage may hold (default 500,
	// max 8000). A passage over it loses context lines, then the end of its
	// hit line, and says so in truncated. Unset, it bounds passages cut
	// around a hit; set, also chunks, which the collection's chunk size
	// bounds otherwise.
	PassageTokens int `json:"passageTokens,omitempty"`
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
	if o.PassageTokens < 0 {
		return errors.New("passageTokens must be 0 or more")
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

// tokenCap is the bound on one passage, in tokens.
func (o PassageOptions) tokenCap() int {
	if o.PassageTokens <= 0 {
		return defaultPassageTokens
	}
	return min(o.PassageTokens, maxPassageTokens)
}

// PassagesResponse is what a search returns with format "passages".
type PassagesResponse struct {
	Passages         []Passage        `json:"passages"`
	Total            int              `json:"total"`
	ContextTruncated bool             `json:"contextTruncated,omitempty"`
	Indexing         *IndexWaitResult `json:"indexing,omitempty"`
	// ImageHits are the images the query matched by their picture, with
	// includeImages and a provider that embeds images (RAG-010).
	ImageHits    []ImageRef `json:"imageHits,omitempty"`
	LimitReport             // limit, limitCapped: set when the server's cap lowered the request's limit (#368)
	RerankResult            // rerank, rerankSkipped (RAG-011)
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
	return passageAround(collection, doc, hitLine(doc.ContentMD, terms), opts.radius(), opts.PassageTokens, score, source)
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

// window is the lines a passage around hit covers: radius lines on each
// side, or the definition holding hit when that is no longer than the window.
// A hit on a Markdown heading starts there and runs down instead: the text
// under the heading is what was asked for, and the lines above it belong to
// the section before.
func (st *sourceText) window(hit, radius int) (start, end int) {
	start, end = max(hit-radius, 1), min(hit+radius, len(st.lines))
	if sp := st.enclosing(hit); sp != nil && sp.EndLine-sp.StartLine <= 2*radius {
		return sp.StartLine, sp.EndLine
	}
	if st.spans == nil && hit >= 1 && hit <= len(st.lines) && strings.HasPrefix(st.lines[hit-1], "#") {
		return hit, min(hit+2*radius, len(st.lines))
	}
	return start, end
}

// passageAround is the window around hit (see window), held to tokenCap
// tokens when that is above zero: context lines go first, then the end of
// the hit line itself, and the passage says it was cut.
func passageAround(collection string, doc *storage.Doc, hit, radius, tokenCap int, score float64, source string) Passage {
	st := newSourceText(doc)
	start, end := st.window(hit, radius)
	p := st.passage(collection, start, end, source, true)
	for tokenCap > 0 && p.Tokens > tokenCap && end > start {
		radius /= 2
		if start, end = st.window(hit, radius); radius == 0 {
			start, end = hit, hit
		}
		p = st.passage(collection, start, end, source, true)
		p.Truncated = true
	}
	if tokenCap > 0 {
		capPassage(&p, tokenCap)
	}
	// The hit's definition names the passage; a hit outside any keeps the
	// name of the one the window starts in (as before CODE-012: a
	// collection without facts must answer byte for byte as it did).
	if sym := st.symbolAt(hit); sym != "" {
		p.Symbol = sym
	}
	p.Score = score
	return p
}

// capPassage cuts a passage's text to tokenCap tokens, on a rune boundary,
// and marks the cut.
func capPassage(p *Passage, tokenCap int) {
	if p.Tokens <= tokenCap {
		return
	}
	cut := tokenCap * 4
	for cut > 0 && !utf8.RuneStart(p.Content[cut]) {
		cut--
	}
	p.Content = p.Content[:cut] + cutMark
	p.Tokens, p.Truncated = approxTokens(p.Content), true
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

// buildPassages turns hits into passages, one per hit, in order, each within
// the options' token bound (see PassageOptions.PassageTokens).
func (s *Server) buildPassages(collection string, hits []passageSource, opts PassageOptions, source string) []Passage {
	out := make([]Passage, 0, len(hits))
	capChunks := opts.PassageTokens > 0
	opts.PassageTokens = opts.tokenCap()
	for i := range hits {
		h := &hits[i]
		doc := s.withSemanticFacts(collection, &h.doc) // names the definition by its qualified name (CODE-012)
		from := len(out)
		switch {
		case h.chunkText != "":
			out = append(out, passageOfChunk(collection, doc, h.chunkText, h.startLine, h.endLine, h.score, source))
		case len(h.chunks) > 0:
			for _, c := range h.chunks {
				out = append(out, s.passageOfChunkIndex(collection, doc, c, h.score, source))
			}
		case h.bestChunk >= 0:
			out = append(out, s.passageOfChunkIndex(collection, doc, h.bestChunk, h.score, source))
		default:
			out = append(out, passageAtHit(collection, doc, h.terms, h.highlights, opts, h.score, source))
			continue
		}
		for k := from; capChunks && k < len(out); k++ {
			capPassage(&out[k], opts.PassageTokens)
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
	return PassageOptions{Format: mcpGetString(m, "format"), PassageLines: mcpGetInt(m, "passage_lines"),
		PassageTokens: mcpGetInt(m, "passage_tokens")}
}

// mcpPassagesJSON renders a passages response the way tools render results.
func mcpPassagesJSON(p *PassagesResponse) string {
	data, _ := json.Marshal(p)
	return string(data)
}

// mcpPassagesFor renders a passages response for a tool call. `fields` on a
// search asks for less than the whole hit; with passages that is the place
// without its text, unless include_content says otherwise — as it drops the
// body of a document in the results format. It used to be ignored here, and
// an agent that asked for keys alone received every passage in full.
func mcpPassagesFor(args map[string]interface{}, p *PassagesResponse) string {
	if includeContent, fields := mcpProjectionArgs(args); len(fields) > 0 && !includeContent {
		bare := *p
		bare.Passages = make([]Passage, len(p.Passages))
		for i, hit := range p.Passages {
			hit.Content, hit.Tokens = "", 0
			bare.Passages[i] = hit
		}
		p = &bare
	}
	return mcpPassagesJSON(p)
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
