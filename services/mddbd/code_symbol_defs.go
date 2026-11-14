package main

import (
	"sort"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/sliceutil"
	"mddb/internal/storage"
)

// Definitions of a symbol lookup (CODE-008), and how the heuristic and the
// semantic answers meet (CODE-012).

// Precision of a symbol lookup's answer (CODE-012).
const (
	// PrecisionSemantic: every definition and use came from semantic facts.
	PrecisionSemantic = "semantic"
	// PrecisionMixed: facts for some documents, text matching for others.
	PrecisionMixed = "mixed"
	// PrecisionHeuristic: text matching only.
	PrecisionHeuristic = "heuristic"
)

// SymbolCandidate is one definition an ambiguous name may mean: ask again
// with this name and path.
type SymbolCandidate struct {
	Path string `json:"path"`
	Name string `json:"name"` // qualified, e.g. Conn.Close
	Kind string `json:"kind"`
}

// precision says where an answer came from. A heuristic answer to a request
// that uses nothing added with facts (path, implementations) leaves it out,
// so a collection without facts answers exactly as it did before them.
func precision(req SymbolRequest, semantic, heuristic bool) string {
	switch {
	case semantic && heuristic:
		return PrecisionMixed
	case semantic:
		return PrecisionSemantic
	case req.Path != "" || containsString(req.Include, RelImplementations):
		return PrecisionHeuristic
	}
	return ""
}

// heuristicDefinitions keeps the defining documents the facts did not
// answer for, in the file Path names when it names one. A document with facts
// stays when its facts do not list the name: they cover functions, types and
// methods, not constants or variables.
func heuristicDefinitions(req SymbolRequest, defs []*sourceText, sem []semanticDef) []*sourceText {
	answered := map[string]bool{}
	for _, d := range sem {
		answered[d.st.doc.ID] = true
	}
	var out []*sourceText
	for _, st := range defs {
		if !answered[st.doc.ID] && (req.Path == "" || docPath(st.doc) == req.Path) {
			out = append(out, st)
		}
	}
	return out
}

// withoutFacts drops the documents that carry facts.
func withoutFacts(in []*sourceText) []*sourceText {
	var out []*sourceText
	for _, st := range in {
		if st.facts == nil {
			out = append(out, st)
		}
	}
	return out
}

// documents are the documents holding the semantic definitions, once each.
func (a semanticAnswer) documents() []*sourceText {
	var out []*sourceText
	seen := map[string]bool{}
	for _, d := range a.defs {
		if !seen[d.st.doc.ID] {
			seen[d.st.doc.ID] = true
			out = append(out, d.st)
		}
	}
	return out
}

// loadSourceTexts loads the documents with these IDs once each, in a stable
// order.
func loadSourceTexts(tx *bolt.Tx, bDocs *bolt.Bucket, collection string, ids []string) []*sourceText {
	ids = sliceutil.Unique(ids)
	sort.Strings(ids)
	out := make([]*sourceText, 0, len(ids))
	for _, id := range ids {
		if doc, err := loadDoc(bDocs.Get(storage.DocKey(collection, id))); err == nil && doc != nil {
			out = append(out, newSourceText(doc))
		}
	}
	return out
}

// symbolDefinitions places the name in each defining document: at its span,
// or — for a language without spans — on the line it first appears.
func (s *Server) symbolDefinitions(req SymbolRequest, defs []*sourceText) []Passage {
	out := []Passage{}
	for _, st := range defs {
		found := false
		for _, sp := range st.spans {
			if sp.Symbol == req.Name {
				p := st.passage(req.Collection, sp.StartLine, sp.EndLine, RelDefinitions, req.IncludeContent)
				p.Symbol, p.Kind = sp.Symbol, sp.Kind
				out = append(out, p)
				found = true
			}
		}
		if !found {
			if line := firstSymbolLine(st.doc.ContentMD, req.Name); line > 0 {
				out = append(out, st.passage(req.Collection, line, line, RelDefinitions, req.IncludeContent))
			}
		}
	}
	return out
}

func filterPassages(in []Passage, keep func(Passage) bool) []Passage {
	var out []Passage
	for _, p := range in {
		if keep(p) {
			out = append(out, p)
		}
	}
	return out
}

// callableOnly reports whether every definition found is a function or a
// method: then a use is a call, and a variable or parameter that happens to
// share the name is not one.
func callableOnly(defs []Passage) bool {
	for _, d := range defs {
		if d.Kind != "function" && d.Kind != "method" {
			return false
		}
	}
	return len(defs) > 0
}
