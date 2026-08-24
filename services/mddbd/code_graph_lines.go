package main

import (
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// Line locations for graph edges (CODE-005 step 4, reusing the line index from
// CODE-002).
//
// An agent told "index.html depends on style.css because of .hero-banner" still
// has to find the two lines. Locating them here saves it two searches.
//
// Opt-in, because it is the only part of a traversal that reads document
// content: a graph walk otherwise touches nothing but the metadata index, and
// making every query pay for content it may not want would be the wrong
// default.

// EdgeLines records where an edge's symbol appears on each side.
//
// A symbol usually occurs more than once in a file — a selector is declared
// once but a class is applied wherever the markup repeats — so these are the
// *first* occurrence, a place to jump to rather than a complete answer. Zero
// means the symbol was not found as literal text, which happens when it was
// built at runtime or resolved from a path rather than written out.
type EdgeLines struct {
	FromLine int `json:"fromLine,omitempty"`
	ToLine   int `json:"toLine,omitempty"`
}

// annotateLines fills in the first-occurrence line for both endpoints of every
// edge, loading each document's content at most once.
func (s *Server) annotateLines(collection string, res *GraphResult) {
	if len(res.Edges) == 0 {
		return
	}

	content := s.loadContentFor(collection, res)
	for i := range res.Edges {
		e := &res.Edges[i]
		e.Lines = &EdgeLines{
			FromLine: firstSymbolLine(content[e.From], e.Symbol),
			ToLine:   firstSymbolLine(content[e.To], e.Symbol),
		}
	}
}

func (s *Server) loadContentFor(collection string, res *GraphResult) map[string]string {
	byKey := make(map[string]string, len(res.Nodes))
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(s.BucketNames.Docs)
		if bDocs == nil {
			return nil
		}
		for _, n := range res.Nodes {
			raw := bDocs.Get(storage.DocKey(collection, n.DocID))
			if raw == nil {
				continue
			}
			if doc, err := loadDoc(raw); err == nil {
				byKey[n.Key] = doc.ContentMD
			}
		}
		return nil
	})
	return byKey
}

// firstSymbolLine finds the line a symbol first appears on, 0 if it does not.
//
// The stored symbol and the source text are not always the same string. A
// selector is indexed as `.hero-banner` but a template writes it as
// `class="hero-banner"`, and an import is indexed as the resolved key
// `theme/style.css` while the source says `href="style.css"`. Matching the
// bare identifier bridges both without guessing: a false line number would send
// someone to the wrong place, which is worse than sending them nowhere.
func firstSymbolLine(content, symbol string) int {
	if content == "" || symbol == "" {
		return 0
	}

	for _, needle := range symbolNeedles(symbol) {
		if idx := strings.Index(content, needle); idx >= 0 {
			return fts.NewLineIndex(content).LineAt(idx)
		}
	}
	return 0
}

// symbolNeedles lists the literal forms a symbol may take in source, most
// specific first.
func symbolNeedles(symbol string) []string {
	needles := []string{symbol}

	// `.hero-banner` in a stylesheet is `hero-banner` in a class attribute.
	if bare := strings.TrimLeft(symbol, ".#"); bare != symbol && bare != "" {
		needles = append(needles, bare)
	}
	// A resolved import key (`theme/style.css`) appears in the source as
	// whatever the author wrote — usually just the file name.
	if i := strings.LastIndex(symbol, "/"); i >= 0 && i+1 < len(symbol) {
		needles = append(needles, symbol[i+1:])
	}
	return needles
}
