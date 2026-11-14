package main

import (
	"errors"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/fts"
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
// Documents indexed with `mddb-cli autoindex --semantic` also carry a
// language server's facts (CODE-010); where they do, the answer comes from
// them (code_symbol_semantic.go, CODE-012) and says so in Precision.
// Implementations are answered only from facts: telling an implementation
// from a namesake takes type information a scanner does not have.

// Symbol relations a lookup can return.
const (
	RelDefinitions     = "definitions"
	RelReferences      = "references"
	RelCallers         = "callers"
	RelCallees         = "callees"
	RelTests           = "tests"
	RelSiblings        = "siblings"
	RelImplementations = "implementations"

	symbolDefaultLimit = 20
	symbolMaxLimit     = 200
	// symbolScanDocs caps the documents read per relation: a name used in
	// thousands of files is a name, not a question.
	symbolScanDocs = 500
)

var allRelations = []string{RelDefinitions, RelReferences, RelCallers, RelCallees, RelTests, RelSiblings, RelImplementations}

// SymbolRequest asks about one symbol in one collection.
type SymbolRequest struct {
	Collection string `json:"collection"`
	// Name is a bare name (Close), or a qualified one (Server.Close) where
	// documents carry semantic facts.
	Name string `json:"name"`
	// Path picks the definition in this file (meta.path) when a name has
	// several (CODE-012).
	Path string `json:"path,omitempty"`
	// Include limits the relations returned; empty means all of them.
	Include []string `json:"include,omitempty"`
	// Limit caps each relation's passages (default 20, max 200, and never
	// above the server's search cap — #368).
	Limit int `json:"limit,omitempty"`
	// IncludeContent adds each passage's text. References always carry
	// their line; definitions, callers and callees carry their whole span
	// only when asked, since that is where the size is.
	IncludeContent bool `json:"includeContent,omitempty"`
}

// SymbolResult is the answer: one list of passages per relation.
type SymbolResult struct {
	Collection      string    `json:"collection"`
	Symbol          string    `json:"symbol"`
	Definitions     []Passage `json:"definitions"`
	References      []Passage `json:"references,omitempty"`
	Callers         []Passage `json:"callers,omitempty"`
	Callees         []Passage `json:"callees,omitempty"`
	Tests           []Passage `json:"tests,omitempty"`
	Siblings        []Passage `json:"siblings,omitempty"`
	Implementations []Passage `json:"implementations,omitempty"`
	// Precision says where the answer came from (CODE-012): see precision.
	Precision string `json:"precision,omitempty"`
	// SemanticSource names the language servers behind the facts used.
	SemanticSource string `json:"semanticSource,omitempty"`
	// Ambiguous: the name has several definitions in the facts and no Path
	// chose one. Candidates lists them; ask again with name and path.
	Ambiguous  bool              `json:"ambiguous,omitempty"`
	Candidates []SymbolCandidate `json:"candidates,omitempty"`
	// Truncated says some relation held more than Limit.
	Truncated   bool `json:"truncated,omitempty"`
	LimitReport      // limit, limitCapped: set when the server's cap lowered the request's limit (#368)
}

var errSymbolRequest = errors.New("collection and name are required")

// normalize validates the request and fills defaults.
func (r *SymbolRequest) normalize() error {
	r.Name, r.Path = strings.TrimSpace(r.Name), strings.TrimSpace(r.Path)
	if r.Collection == "" || r.Name == "" {
		return errSymbolRequest
	}
	if r.Limit <= 0 {
		r.Limit = symbolDefaultLimit
	}
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

// symbolDocs is what one read transaction finds for a lookup.
type symbolDocs struct {
	defs, users []*sourceText
	semantic    semanticAnswer
	cut         bool
}

// LookupSymbol answers a SymbolRequest.
func (s *Server) LookupSymbol(req SymbolRequest) (*SymbolResult, error) {
	if err := req.normalize(); err != nil {
		return nil, err
	}
	res := &SymbolResult{Collection: req.Collection, Symbol: req.Name, Definitions: []Passage{}}
	// #368: the lookup's own bound of 200 per relation, under the server's
	// cap and the collection's maxTopK — the one cap every search shares.
	req.Limit, res.LimitReport = s.CapLimit(req.Collection, req.Limit, symbolMaxLimit)
	found := s.symbolDocuments(req)
	sem := found.semantic

	// The heuristic path covers what the facts do not: definitions in
	// documents without facts, or of names facts do not list (constants,
	// selectors). Where facts define the name, a text match in a document
	// with facts is a namesake — its uses are in its symRefs or nowhere.
	defs := heuristicDefinitions(req, found.defs, sem.defs)
	users := found.users
	if len(sem.defs) > 0 {
		users = withoutFacts(users)
	}
	hDefs := s.symbolDefinitions(req, defs)
	res.Definitions = append(append([]Passage{}, sem.definitions...), hDefs...)
	hRefs := symbolReferences(req, users, callableOnly(res.Definitions))
	refs := append(append([]Passage(nil), sem.references...), hRefs...)
	if req.wants(RelReferences) {
		res.References = refs
	}
	if req.wants(RelCallers) {
		res.Callers = append(append([]Passage(nil), sem.callers...), symbolCallers(req, users, hRefs)...)
	}
	if req.wants(RelTests) {
		res.Tests = filterPassages(refs, func(p Passage) bool { return isTestPath(p.Path) })
		for i := range res.Tests {
			res.Tests[i].Source = RelTests
		}
	}
	var hCallees []Passage
	if req.wants(RelCallees) {
		hCallees = s.symbolCallees(req, defs)
		res.Callees = append(append([]Passage(nil), sem.callees...), hCallees...)
	}
	if req.wants(RelSiblings) {
		res.Siblings = s.symbolSiblings(req, append(defs, sem.documents()...))
	}
	if req.wants(RelImplementations) {
		res.Implementations = sem.implementations
	}
	if !req.wants(RelDefinitions) {
		res.Definitions = []Passage{}
	}
	res.Precision = precision(req, len(sem.defs) > 0, len(hDefs)+len(hRefs)+len(hCallees) > 0)
	res.SemanticSource = sem.source()
	if req.Path == "" && len(sem.defs) > 1 {
		res.Ambiguous, res.Candidates = true, sem.candidates()
	}
	res.Truncated = found.cut || cutRelations(res, req.Limit)
	return res, nil
}

// symbolDocuments reads what the lookup needs in one transaction: the
// documents defining and using the name, and what the facts say about it.
func (s *Server) symbolDocuments(req SymbolRequest) symbolDocs {
	wantUsers := req.wants(RelReferences) || req.wants(RelCallers) || req.wants(RelTests)
	// The extractors leave out calls through a value or a package — x.f(),
	// pkg.F() — because from text alone they cannot tell what f is. So the
	// documents that use a method are found by the full-text index too, which
	// holds every identifier; occurrenceLines then keeps only whole-word uses.
	var textIDs []string
	if wantUsers && s.FTSIndex != nil {
		if hits, err := s.FTSIndex.SearchBM25(req.Collection, req.Name, symbolScanDocs, fts.DefaultBM25); err == nil {
			for _, h := range hits {
				textIDs = append(textIDs, h.DocID)
			}
		}
	}
	var out symbolDocs
	_ = s.DBViewC(req.Collection, func(tx *bolt.Tx) error {
		bIdx, bDocs := tx.Bucket(s.BucketNames.IdxMeta), tx.Bucket(s.BucketNames.Docs)
		if bIdx == nil || bDocs == nil {
			return nil
		}
		defIDs, _ := docIDsWithMeta(tx, bIdx, req.Collection, MetaKeyDefines, req.Name, symbolScanDocs)
		out.defs = loadSourceTexts(tx, bDocs, req.Collection, defIDs)
		r := s.newFactReader(tx, req.Collection)
		out.semantic = semanticRelations(r, req, semanticDefinitions(r, req, out.defs))
		out.cut = r.more
		if wantUsers {
			useIDs, _ := docIDsWithMeta(tx, bIdx, req.Collection, MetaKeyUses, req.Name, symbolScanDocs)
			out.users = loadSourceTexts(tx, bDocs, req.Collection, append(useIDs, textIDs...))
		}
		return nil
	})
	return out
}

// cutRelations trims every relation to limit and reports whether any held
// more.
func cutRelations(res *SymbolResult, limit int) bool {
	cut := false
	for _, list := range []*[]Passage{&res.Definitions, &res.References, &res.Callers, &res.Callees, &res.Tests,
		&res.Siblings, &res.Implementations} {
		if len(*list) > limit {
			*list = (*list)[:limit]
			cut = true
		}
	}
	if len(res.Candidates) > limit {
		res.Candidates, cut = res.Candidates[:limit], true
	}
	return cut
}
