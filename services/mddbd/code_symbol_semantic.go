package main

import (
	"sort"
	"strings"
)

// Symbol lookup from semantic facts (CODE-012).
//
// When documents carry facts from a language server (CODE-010), a name is
// resolved to definitions by their qualified names, and every relation is
// read from the facts instead of guessed from text: two methods called Close
// on two types get two separate reference lists, and implementations — which
// a scanner cannot tell from namesakes — become answerable.

// semanticDef is one definition the facts name, in the document holding it.
type semanticDef struct {
	st  *sourceText
	def SymbolDefFact
}

// target is how facts elsewhere point at this definition.
func (d semanticDef) target() string { return docPath(d.st.doc) + "#" + d.def.Name }

// passageOf builds the passage over a definition the facts describe.
func passageOf(collection string, st *sourceText, def SymbolDefFact, source string, withContent bool) Passage {
	p := st.passage(collection, def.Start, def.End, source, withContent)
	p.Symbol, p.Kind, p.Container = def.Name, def.Kind, def.Container
	return p
}

// matchesSymbol reports whether a qualified name answers a request's name:
// the same name, or one ending in ".name" — Close finds Conn.Close,
// Server.LookupSymbol finds pkg.Server.LookupSymbol — or, past a PHP
// namespace, in `\name`: Kernel finds App\Http\Kernel (CODE-013).
func matchesSymbol(qualified, name string) bool {
	return qualified == name || strings.HasSuffix(qualified, "."+name) || strings.HasSuffix(qualified, `\`+name)
}

// bareName is the last segment of a qualified name.
func bareName(name string) string { return name[strings.LastIndexByte(name, '.')+1:] }

// semanticDefinitions resolves a name to the definitions the facts hold,
// ordered by path and line. Candidates come from three places: documents
// whose symDefs hold the name exactly (a top-level name, or a qualified one),
// documents the defines index lists under its last segment (a method, whose
// qualified name starts with its type), and the document at req.Path.
// defined is the second, already loaded for the heuristic path when the name
// has no dots.
func semanticDefinitions(r *factReader, req SymbolRequest, defined []*sourceText) []semanticDef {
	var out []semanticDef
	seen := map[string]bool{}
	for _, st := range definitionCandidates(r, req, defined) {
		if st.facts == nil || seen[st.doc.ID] || (req.Path != "" && docPath(st.doc) != req.Path) {
			continue
		}
		seen[st.doc.ID] = true
		for _, d := range st.facts.Defs {
			if matchesSymbol(d.Name, req.Name) {
				out = append(out, semanticDef{st: st, def: d})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if pi, pj := docPath(out[i].st.doc), docPath(out[j].st.doc); pi != pj {
			return pi < pj
		}
		return out[i].def.Start < out[j].def.Start
	})
	return out
}

// definitionCandidates gathers the documents that may define req.Name, from
// the three places semanticDefinitions describes; duplicates are dropped by
// the caller.
func definitionCandidates(r *factReader, req SymbolRequest, defined []*sourceText) []*sourceText {
	candidates := append([]*sourceText{}, defined...)
	if bare := bareName(req.Name); bare != req.Name && bare != "" && r.bIdx != nil {
		ids, _ := docIDsWithMeta(nil, r.bIdx, req.Collection, MetaKeyDefines, bare, symbolScanDocs)
		candidates = append(candidates, r.load(ids)...)
	}
	for _, h := range r.scan(MetaKeySymDefs, req.Name+" ") {
		if st := r.doc(h.DocID); st != nil {
			candidates = append(candidates, st)
		}
	}
	// The defines index lists what the scanner finds, which misses interface
	// methods and names past its cap in a large file; a path names the
	// document whose facts to read (CODE-013).
	if req.Path != "" {
		if st := r.atPath(req.Path); st != nil {
			candidates = append(candidates, st)
		}
	}
	return candidates
}

// semanticAnswer is what the facts say about the definitions found, each
// relation grouped per definition (Passage.Target).
type semanticAnswer struct {
	defs                             []semanticDef
	definitions, references, callers []Passage
	callees, implementations         []Passage
	sources                          map[string]bool
}

func (a *semanticAnswer) used(st *sourceText) {
	a.sources[st.facts.Source] = true
}

// semanticRelations reads every relation of defs from the facts.
func semanticRelations(r *factReader, req SymbolRequest, defs []semanticDef) semanticAnswer {
	a := semanticAnswer{defs: defs, sources: map[string]bool{}}
	for _, d := range defs {
		a.used(d.st)
		a.definitions = append(a.definitions, passageOf(req.Collection, d.st, d.def, RelDefinitions, req.IncludeContent))
		if req.wants(RelReferences) || req.wants(RelCallers) || req.wants(RelTests) {
			a.addReferences(r, req, d)
		}
		if req.wants(RelCallees) {
			a.addCallees(r, req, d)
		}
		if req.wants(RelImplementations) {
			a.addImplementations(r, req, d)
		}
	}
	return a
}

// addReferences adds every use of d, and the definitions holding them as its
// callers.
func (a *semanticAnswer) addReferences(r *factReader, req SymbolRequest, d semanticDef) {
	callers := map[string]bool{}
	for _, use := range r.refsTo(d.target()) {
		a.used(use.st)
		for _, line := range use.lines {
			if use.st == d.st && line == d.def.Start {
				continue // the declaration, where a server lists it among the uses
			}
			p := use.st.passage(req.Collection, line, line, RelReferences, true)
			p.Content = strings.TrimSpace(p.Content)
			p.Tokens, p.Target = approxTokens(p.Content), d.target()
			a.references = append(a.references, p)
			if c := use.st.definitionAt(line); c != nil && !callers[use.st.doc.ID+"#"+c.Name] {
				callers[use.st.doc.ID+"#"+c.Name] = true
				p := passageOf(req.Collection, use.st, *c, RelCallers, req.IncludeContent)
				p.Target = d.target()
				a.callers = append(a.callers, p)
			}
		}
	}
}

// addCallees adds the definitions d's span references: what it calls, and
// the types it names, wherever the facts place them.
func (a *semanticAnswer) addCallees(r *factReader, req SymbolRequest, d semanticDef) {
	var found []Passage
	for _, ref := range d.st.facts.Refs {
		self := ref.Path+"#"+ref.Name == d.target()
		receiver := ref.Path == docPath(d.st.doc) && ref.Name == d.def.Container // a method names its own type
		if self || receiver || !anyLineIn(ref.Lines, d.def.Start, d.def.End) {
			continue
		}
		at := d.st
		if ref.Path != docPath(d.st.doc) {
			at = r.atPath(ref.Path)
		}
		if at == nil {
			continue // defined where no document has facts: nothing exact to say
		}
		if def := at.defNamed(ref.Name); def != nil {
			a.used(at)
			p := passageOf(req.Collection, at, *def, RelCallees, req.IncludeContent)
			p.Target = d.target()
			found = append(found, p)
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].Symbol < found[j].Symbol })
	a.callees = append(a.callees, found...)
}

// addImplementations adds the types implementing d, at their definitions.
func (a *semanticAnswer) addImplementations(r *factReader, req SymbolRequest, d semanticDef) {
	for _, use := range r.implsOf(d.target()) {
		a.used(use.st)
		def := use.st.defNamed(use.local)
		if def == nil {
			// The type's own definition fell to the fact limit: place it where
			// its name first appears.
			line := firstSymbolLine(use.st.doc.ContentMD, bareName(use.local))
			if line == 0 {
				continue
			}
			def = &SymbolDefFact{Name: use.local, Start: line, End: line}
		}
		p := passageOf(req.Collection, use.st, *def, RelImplementations, req.IncludeContent)
		p.Target = d.target()
		a.implementations = append(a.implementations, p)
	}
}

func anyLineIn(lines []int, start, end int) bool {
	for _, l := range lines {
		if start <= l && l <= end {
			return true
		}
	}
	return false
}

// source names the language servers the answer came from, e.g. gopls/0.24.0.
func (a semanticAnswer) source() string {
	var out []string
	for s := range a.sources {
		out = append(out, s)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// candidates lists the definitions an ambiguous name could mean.
func (a semanticAnswer) candidates() []SymbolCandidate {
	out := make([]SymbolCandidate, 0, len(a.defs))
	for _, d := range a.defs {
		out = append(out, SymbolCandidate{Path: docPath(d.st.doc), Name: d.def.Name, Kind: d.def.Kind})
	}
	return out
}
