package main

// Where the gain counters (AGT-004) meet the search paths: each finished
// result list is turned into one gainSize per hit.

// countHits records a finished result list, one gainSize per result.
func countHits[T any](s *Server, results []T, size func(T) gainSize) {
	if s.Gain == nil || len(results) == 0 {
		return
	}
	hits := make([]gainSize, len(results))
	for i, r := range results {
		hits[i] = size(r)
	}
	s.Gain.Hits(hits)
}

// wholeDoc is a hit returned as its whole document.
func wholeDoc(content string) gainSize {
	n := approxTokens(content)
	return gainSize{Returned: n, Full: n}
}

// passageHits counts passages per document: several passages from one
// document avoid that document once, minus all of them.
func passageHits(passages []Passage) []gainSize {
	idx := map[string]int{}
	var out []gainSize
	for _, p := range passages {
		key := p.Collection + "|" + p.Key + "|" + p.Lang
		i, seen := idx[key]
		if !seen {
			i = len(out)
			idx[key] = i
			out = append(out, gainSize{})
		}
		out[i].Returned += p.Tokens
		out[i].Full = max(out[i].Full, p.docTokens)
	}
	return out
}

// chunkHit is a vector hit in chunk or window mode: the chunk, plus the
// document when the caller asked for content too.
func chunkHit(chunkText, returnedContent string, docTokens int) gainSize {
	if chunkText == "" {
		return wholeDoc(returnedContent)
	}
	return gainSize{Returned: approxTokens(chunkText) + approxTokens(returnedContent), Full: docTokens}
}

// countCross counts a cross-collection search. Its passages, when asked
// for, are counted where every passage response is finished.
func (s *Server) countCross(algorithm string, items []CrossSearchResultItem, passages bool) {
	s.Gain.Search(gainKind{Type: "cross", Algorithm: algorithm})
	if !passages {
		countHits(s, items, func(it CrossSearchResultItem) gainSize { return wholeDoc(it.Document.ContentMD) })
	}
}
