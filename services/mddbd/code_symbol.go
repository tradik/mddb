package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/sliceutil"
	"mddb/internal/storage"
)

// Symbol lookup (CODE-008).
//
// code_graph answers "which documents connect"; this answers the question an
// agent changing a function asks: where is it defined (path and lines), where
// is it used and from inside which definition, what does it call, which tests
// touch it, and what else lives next to it.
//
// Everything is derived at query time from what is already indexed: the
// documents that define and use the name come from the metadata index
// (defines/uses, CODE-004), and their spans from codeintel.Spans over their
// content. There is no symbol table to keep in step with writes, replicate,
// restore or rebuild — a reindex cannot make it disagree with the documents,
// because it is the documents.
//
// Out of scope until there is evidence it is needed: "implementations"
// (interfaces, traits). Telling an implementation from a namesake takes type
// information a scanner does not have.

// Symbol relations a lookup can return.
const (
	RelDefinitions = "definitions"
	RelReferences  = "references"
	RelCallers     = "callers"
	RelCallees     = "callees"
	RelTests       = "tests"
	RelSiblings    = "siblings"

	symbolDefaultLimit = 20
	symbolMaxLimit     = 200
	// symbolScanDocs caps the documents read per relation: a name used in
	// thousands of files is a name, not a question.
	symbolScanDocs = 500
)

var allRelations = []string{RelDefinitions, RelReferences, RelCallers, RelCallees, RelTests, RelSiblings}

// SymbolRequest asks about one symbol in one collection.
type SymbolRequest struct {
	Collection string `json:"collection"`
	Name       string `json:"name"`
	// Include limits the relations returned; empty means all of them.
	Include []string `json:"include,omitempty"`
	// Limit caps each relation's passages (default 20, max 200).
	Limit int `json:"limit,omitempty"`
	// IncludeContent adds each passage's text. References always carry
	// their line; definitions, callers and callees carry their whole span
	// only when asked, since that is where the size is.
	IncludeContent bool `json:"includeContent,omitempty"`
}

// SymbolResult is the answer: one list of passages per relation.
type SymbolResult struct {
	Collection  string    `json:"collection"`
	Symbol      string    `json:"symbol"`
	Definitions []Passage `json:"definitions"`
	References  []Passage `json:"references,omitempty"`
	Callers     []Passage `json:"callers,omitempty"`
	Callees     []Passage `json:"callees,omitempty"`
	Tests       []Passage `json:"tests,omitempty"`
	Siblings    []Passage `json:"siblings,omitempty"`
	// Truncated says some relation held more than Limit.
	Truncated bool `json:"truncated,omitempty"`
}

var errSymbolRequest = errors.New("collection and name are required")

// normalize validates the request and fills defaults.
func (r *SymbolRequest) normalize() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Collection == "" || r.Name == "" {
		return errSymbolRequest
	}
	if r.Limit <= 0 {
		r.Limit = symbolDefaultLimit
	}
	r.Limit = min(r.Limit, symbolMaxLimit)
	for _, inc := range r.Include {
		if !containsString(allRelations, inc) {
			return fmt.Errorf("unknown relation %q (known: %s)", inc, strings.Join(allRelations, ", "))
		}
	}
	return nil
}

func (r *SymbolRequest) wants(rel string) bool {
	return len(r.Include) == 0 || containsString(r.Include, rel)
}

// LookupSymbol answers a SymbolRequest.
func (s *Server) LookupSymbol(req SymbolRequest) (*SymbolResult, error) {
	if err := req.normalize(); err != nil {
		return nil, err
	}
	res := &SymbolResult{Collection: req.Collection, Symbol: req.Name, Definitions: []Passage{}}
	wantUsers := req.wants(RelReferences) || req.wants(RelCallers) || req.wants(RelTests)
	// The extractors leave out calls through a value or a package — x.f(),
	// pkg.F() — because from text alone they cannot tell what f is. So the
	// documents that use a method are found by the full-text index too, which
	// holds every identifier; occurrenceLines then keeps only whole-word uses.
	var textIDs []string
	if wantUsers && s.FTSIndex != nil {
		if hits, err := s.FTSIndex.SearchBM25(req.Collection, req.Name, symbolScanDocs); err == nil {
			for _, h := range hits {
				textIDs = append(textIDs, h.DocID)
			}
		}
	}
	var defs, users []*sourceText
	_ = s.DBViewC(req.Collection, func(tx *bolt.Tx) error {
		bIdx, bDocs := tx.Bucket(s.BucketNames.IdxMeta), tx.Bucket(s.BucketNames.Docs)
		if bIdx == nil || bDocs == nil {
			return nil
		}
		defIDs, _ := docIDsWithMeta(tx, bIdx, req.Collection, MetaKeyDefines, req.Name, symbolScanDocs)
		defs = loadSourceTexts(tx, bDocs, req.Collection, defIDs)
		if wantUsers {
			useIDs, _ := docIDsWithMeta(tx, bIdx, req.Collection, MetaKeyUses, req.Name, symbolScanDocs)
			users = loadSourceTexts(tx, bDocs, req.Collection, append(useIDs, textIDs...))
		}
		return nil
	})

	res.Definitions = s.symbolDefinitions(req, defs)
	refs := symbolReferences(req, users, callableOnly(res.Definitions))
	if req.wants(RelReferences) {
		res.References = refs
	}
	if req.wants(RelCallers) {
		res.Callers = symbolCallers(req, users, refs)
	}
	if req.wants(RelTests) {
		res.Tests = filterPassages(refs, func(p Passage) bool { return isTestPath(p.Path) })
		for i := range res.Tests {
			res.Tests[i].Source = RelTests
		}
	}
	if req.wants(RelCallees) {
		res.Callees = s.symbolCallees(req, defs)
	}
	if req.wants(RelSiblings) {
		res.Siblings = s.symbolSiblings(req, defs)
	}
	if !req.wants(RelDefinitions) {
		res.Definitions = []Passage{}
	}
	for _, list := range []*[]Passage{&res.Definitions, &res.References, &res.Callers, &res.Callees, &res.Tests, &res.Siblings} {
		if len(*list) > req.Limit {
			*list = (*list)[:req.Limit]
			res.Truncated = true
		}
	}
	return res, nil
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
