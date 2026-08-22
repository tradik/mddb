package fts

import (
	"sort"
	"strings"
	"unicode"
)

// --- Defaults ---

// These live as constants so the handler and the public extractor share
// the same values. Tuned for typical markdown body text: 150 chars is
// roughly two printed lines, 3 fragments fits a compact search card.
const (
	defaultHighlightFragmentSize = 150
	defaultHighlightMaxFragments = 3
	defaultHighlightTag          = "<mark>"
)

// Highlight is one snippet of text extracted around a search hit. Offsets
// are byte positions into the original document content — clients that
// need to do further markup on the same bytes (e.g. re-tokenize to apply
// syntax colors) can map directly without re-searching.
type Highlight struct {
	Fragment     string   `json:"fragment"`
	MatchedTerms []string `json:"matchedTerms,omitempty"`
	StartOffset  int      `json:"startOffset"`
	EndOffset    int      `json:"endOffset"`
	// StartLine and EndLine are 1-based and inclusive, derived from the byte
	// offsets above (CODE-002). They are what makes a result actionable for
	// code: "css/style.css lines 41-58" is a neighbourhood an agent can edit,
	// where a document name alone means reading the file to find the place.
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine"`
}

// HighlightOptions tunes the extractor. A zero value produces the same
// output as calling with all defaults — callers can set just the fields
// they care about.
type HighlightOptions struct {
	MaxFragments int    // per-document fragment cap (default 3)
	FragmentSize int    // approximate char count per fragment (default 150)
	OpenTag      string // e.g. "<mark>" (default "<mark>")
	CloseTag     string // derived from OpenTag when empty ("<x>" → "</x>")
}

// normalizedOptions fills in defaults and derives the close tag when the
// caller supplied only an open tag. Returns a copy so we never mutate
// caller state.
func normalizedOptions(opts HighlightOptions) HighlightOptions {
	if opts.MaxFragments <= 0 {
		opts.MaxFragments = defaultHighlightMaxFragments
	}
	if opts.FragmentSize <= 0 {
		opts.FragmentSize = defaultHighlightFragmentSize
	}
	if opts.OpenTag == "" {
		opts.OpenTag = defaultHighlightTag
	}
	if opts.CloseTag == "" {
		opts.CloseTag = deriveCloseTag(opts.OpenTag)
	}
	return opts
}

// deriveCloseTag converts "<mark>" → "</mark>", "**" → "**",
// "[h]" → "[/h]" etc. Recognizes the common HTML-style pattern and
// otherwise mirrors the input (covering markdown bolding and wrapping
// delimiters with no separate close).
func deriveCloseTag(openTag string) string {
	if len(openTag) < 2 || openTag[0] != '<' || openTag[len(openTag)-1] != '>' {
		return openTag
	}
	inner := openTag[1 : len(openTag)-1]
	if space := strings.IndexByte(inner, ' '); space > 0 {
		inner = inner[:space] // strip attributes like <mark class="x">
	}
	return "</" + inner + ">"
}

// --- Public extractor ---

// ExtractHighlights finds up to MaxFragments snippets of `content` where
// any of `terms` occurs, wraps each match with OpenTag/CloseTag, and
// returns the fragments. Matching is case-insensitive on Unicode word
// boundaries — so "run" matches "Run" and "RUN" but not the "run" inside
// "runner". Works uniformly for every FTS mode because it only needs the
// raw content plus the matched-term list.
//
// Fragment selection strategy:
//  1. Find every hit's char offset in the content.
//  2. Merge hits whose surrounding windows would overlap.
//  3. Sort merged clusters by hit count, keep the top MaxFragments.
//  4. Trim each cluster to at most FragmentSize bytes, snap to word
//     boundaries, wrap matches, emit.
func ExtractHighlights(content string, terms []string, opts HighlightOptions) []Highlight {
	opts = normalizedOptions(opts)
	if content == "" || len(terms) == 0 {
		return nil
	}
	hits := findTermHits(content, terms)
	if len(hits) == 0 {
		return nil
	}
	clusters := clusterHits(hits, opts.FragmentSize)
	// Rank clusters: more hits first so the most informative snippet shows.
	// Ties are broken by earliest offset so repeated calls are deterministic.
	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i]) != len(clusters[j]) {
			return len(clusters[i]) > len(clusters[j])
		}
		return clusters[i][0].start < clusters[j][0].start
	})
	if len(clusters) > opts.MaxFragments {
		clusters = clusters[:opts.MaxFragments]
	}
	// Built once for the document and shared by every fragment: the scan is
	// linear in the content, the lookups logarithmic.
	lines := NewLineIndex(content)

	out := make([]Highlight, 0, len(clusters))
	for _, cluster := range clusters {
		h := renderFragment(content, cluster, opts)
		h.StartLine, h.EndLine = lines.Range(h.StartOffset, h.EndOffset)
		out = append(out, h)
	}
	// Re-sort fragments by document order so UIs can display them in reading
	// flow rather than by relevance rank.
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartOffset < out[j].StartOffset
	})
	return out
}

// --- Hit detection ---

// termHit is a single occurrence of one query term in the content. Both
// offsets are byte positions, so len() arithmetic on content slices works
// without Unicode awareness.
type termHit struct {
	term  string
	start int
	end   int
}

// findTermHits scans `content` once per unique normalized term and
// collects every word-boundary match. Matching is case-insensitive; the
// term and content are both folded to lowercase before comparison but
// offsets refer to the original byte positions.
func findTermHits(content string, terms []string) []termHit {
	lowerContent := strings.ToLower(content)
	unique := make(map[string]struct{}, len(terms))
	for _, t := range terms {
		t = strings.ToLower(strings.TrimSpace(t))
		// The FTS evaluator tags NOT-branch contributions with a sentinel
		// marker (see fts_query_expr.go). Skip it — we'd otherwise scan
		// for a non-printable character and find no hits.
		if t == "" || t == negatedMarker {
			continue
		}
		unique[t] = struct{}{}
	}

	var hits []termHit
	for term := range unique {
		searchFrom := 0
		for {
			idx := strings.Index(lowerContent[searchFrom:], term)
			if idx < 0 {
				break
			}
			start := searchFrom + idx
			end := start + len(term)
			if isWordBoundary(lowerContent, start, end) {
				hits = append(hits, termHit{term: term, start: start, end: end})
			}
			searchFrom = start + 1
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].start < hits[j].start })
	return hits
}

// isWordBoundary returns true when a substring at [start, end) sits
// between non-alphanumeric characters (or at the text edges). Matches
// the standard "\b...\b" regex intuition without paying the regex cost.
func isWordBoundary(content string, start, end int) bool {
	before := start == 0 || !isWordChar(rune(content[start-1]))
	after := end == len(content) || !isWordChar(rune(content[end]))
	return before && after
}

// isWordChar classifies letters, digits and underscore as part of a word —
// identical to the standard regex "\w" class. ASCII-only for speed; real
// Unicode letter classes would require iterating runes, which doubles
// work for the hot loop. Good enough for highlight boundaries.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// --- Clustering ---

// clusterHits groups nearby hits so one fragment can show multiple
// adjacent matches rather than emitting N tiny fragments for N adjacent
// words. Two hits join the same cluster when their char-distance is
// smaller than the fragment size.
func clusterHits(hits []termHit, fragSize int) [][]termHit {
	if len(hits) == 0 {
		return nil
	}
	var out [][]termHit
	cur := []termHit{hits[0]}
	for _, h := range hits[1:] {
		if h.start-cur[len(cur)-1].end <= fragSize {
			cur = append(cur, h)
		} else {
			out = append(out, cur)
			cur = []termHit{h}
		}
	}
	out = append(out, cur)
	return out
}

// --- Rendering ---

// renderFragment slices `content` around a cluster, snaps the edges to
// word boundaries, wraps each matched span with the configured tags, and
// returns the resulting Highlight.
func renderFragment(content string, cluster []termHit, opts HighlightOptions) Highlight {
	first := cluster[0]
	last := cluster[len(cluster)-1]
	padding := (opts.FragmentSize - (last.end - first.start)) / 2
	if padding < 0 {
		padding = 0
	}

	startOff := first.start - padding
	if startOff < 0 {
		startOff = 0
	}
	endOff := last.end + padding
	if endOff > len(content) {
		endOff = len(content)
	}

	// Snap to the nearest preceding / following word boundary so fragments
	// don't begin or end mid-word. Keeps truncation visually clean.
	startOff = snapLeft(content, startOff)
	endOff = snapRight(content, endOff)

	fragment := buildFragment(content, startOff, endOff, cluster, opts)
	matched := uniqueLowercase(cluster)

	return Highlight{
		Fragment:     fragment,
		MatchedTerms: matched,
		StartOffset:  startOff,
		EndOffset:    endOff,
	}
}

// buildFragment stitches together the prefix (raw content up to the first
// hit), each hit wrapped in open/close tags, interleaved with the raw
// content between consecutive hits, and finally the suffix.
func buildFragment(content string, startOff, endOff int, cluster []termHit, opts HighlightOptions) string {
	var b strings.Builder
	b.Grow((endOff - startOff) + len(cluster)*(len(opts.OpenTag)+len(opts.CloseTag)))
	cursor := startOff
	for _, h := range cluster {
		if h.start < cursor {
			continue // defensive; shouldn't happen with properly sorted input
		}
		if h.start > endOff {
			break
		}
		b.WriteString(content[cursor:h.start])
		b.WriteString(opts.OpenTag)
		b.WriteString(content[h.start:h.end])
		b.WriteString(opts.CloseTag)
		cursor = h.end
	}
	if cursor < endOff {
		b.WriteString(content[cursor:endOff])
	}

	// Add ellipsis markers when the fragment is a slice of a larger doc.
	prefix := ""
	suffix := ""
	if startOff > 0 {
		prefix = "…"
	}
	if endOff < len(content) {
		suffix = "…"
	}
	if prefix == "" && suffix == "" {
		return b.String()
	}
	return prefix + b.String() + suffix
}

// snapLeft advances `i` rightward until it sits at a word boundary,
// so the fragment doesn't begin mid-word. Caps the walk at FragmentSize
// to stop pathological inputs (one-long-token docs) from spinning.
func snapLeft(content string, i int) int {
	if i <= 0 {
		return 0
	}
	cap := i + defaultHighlightFragmentSize/2
	if cap > len(content) {
		cap = len(content)
	}
	for j := i; j < cap; j++ {
		if !isWordChar(rune(content[j-1])) || !isWordChar(rune(content[j])) {
			return j
		}
	}
	return i
}

// snapRight moves `i` leftward to the nearest word boundary so the
// fragment doesn't cut mid-word either.
func snapRight(content string, i int) int {
	if i >= len(content) {
		return len(content)
	}
	floor := i - defaultHighlightFragmentSize/2
	if floor < 0 {
		floor = 0
	}
	for j := i; j > floor; j-- {
		if j >= len(content) {
			continue
		}
		if !isWordChar(rune(content[j])) || (j > 0 && !isWordChar(rune(content[j-1]))) {
			return j
		}
	}
	return i
}

// uniqueLowercase collects the distinct matched terms present in a cluster
// so the Highlight.MatchedTerms slice reflects what the fragment actually
// shows, not the whole query term list.
func uniqueLowercase(cluster []termHit) []string {
	seen := make(map[string]struct{}, len(cluster))
	out := make([]string, 0, len(cluster))
	for _, h := range cluster {
		if _, ok := seen[h.term]; ok {
			continue
		}
		seen[h.term] = struct{}{}
		out = append(out, h.term)
	}
	return out
}
