package main

import (
	"strings"

	"mddb/internal/codeintel"
	"mddb/internal/fts"
	"mddb/internal/storage"
)

// Passages (AGT-002, CODE-008).
//
// An agent working on code needs one shape from every search: which file,
// which lines, inside which definition, and the text of those lines. Results
// used to carry them in four places — meta.path, startLine, chunkText and the
// document — differently on each endpoint, and without the definition at all.
// A Passage is that shape, and every surface that returns places in source
// builds it here, with one set of rules.

// PassageLines is a 1-based, inclusive line range.
type PassageLines struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Passage is a place in a document: where it is and, optionally, what it says.
type Passage struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang,omitempty"`
	// Path is the source path (meta.path), or the key when there is none.
	Path  string       `json:"path"`
	Lines PassageLines `json:"lines"`
	// Symbol is the innermost definition holding the first line (CODE-008);
	// empty for prose and for lines outside any definition.
	Symbol string `json:"symbol,omitempty"`
	// Kind is the definition's kind when the passage is one (function, type…).
	Kind string `json:"kind,omitempty"`
	// Container is the type or class holding the definition, from semantic
	// facts (CODE-012); empty at top level and without facts.
	Container string `json:"container,omitempty"`
	// Target is the definition a symbol lookup's passage belongs to, as
	// "<path>#<qualified name>", when the lookup answered from semantic facts
	// (CODE-012): with two definitions of one name it says which one each
	// reference, caller, callee or implementation is about.
	Target  string  `json:"target,omitempty"`
	Content string  `json:"content,omitempty"`
	Score   float64 `json:"score,omitempty"`
	// Source says what produced the passage: fts, vector, hybrid, cross, or a
	// symbol relation (definition, reference, caller, callee, test, sibling).
	Source string `json:"source,omitempty"`
	// Tokens estimates Content's size with the context budget's estimator.
	Tokens int `json:"tokens"`
	// Images are the document's images, with includeImages (RAG-010).
	Images []ImageRef `json:"images,omitempty"`
	// docTokens is the whole document's estimate, for the gain counters
	// (AGT-004): what the caller would have read without the passage.
	docTokens int
}

// docPath is where a document came from: meta.path, else its key.
func docPath(doc *storage.Doc) string {
	if p := firstMeta(doc.Meta, fts.MetaKeyPath); p != "" {
		return p
	}
	return doc.Key
}

// sourceText is a document prepared for line lookups: its lines and, for
// code, its definition spans. Built once per document per request.
type sourceText struct {
	doc   *storage.Doc
	lines []string
	spans []codeintel.Span
	// facts are the document's semantic facts (CODE-010) when it carries
	// them: the definition holding a line is then the language server's, by
	// its qualified name (CODE-012).
	facts *SemanticFacts
}

func newSourceText(doc *storage.Doc) *sourceText {
	st := &sourceText{doc: doc, lines: strings.Split(doc.ContentMD, "\n")}
	if DocumentKind(doc) == fts.KindCode {
		st.spans = codeintel.Spans(CodeLanguage(doc), doc.ContentMD)
		if facts, ok := decodeSemanticFacts(doc.Meta); ok {
			st.facts = &facts
		}
	}
	return st
}

// text returns lines start..end (1-based, inclusive), clamped to the document.
func (st *sourceText) text(start, end int) string {
	start = max(start, 1)
	end = min(end, len(st.lines))
	if start > end {
		return ""
	}
	return strings.Join(st.lines[start-1:end], "\n")
}

// enclosing is the innermost definition holding line, or nil.
func (st *sourceText) enclosing(line int) *codeintel.Span {
	return codeintel.SymbolAt(st.spans, line)
}

// definitionAt is the innermost definition the facts place around line, or
// nil without facts or outside every definition.
func (st *sourceText) definitionAt(line int) *SymbolDefFact {
	if st.facts == nil {
		return nil
	}
	var best *SymbolDefFact
	for i := range st.facts.Defs {
		d := &st.facts.Defs[i]
		if d.Start <= line && line <= d.End && (best == nil || d.End-d.Start < best.End-best.Start) {
			best = d
		}
	}
	return best
}

// symbolAt names the innermost definition holding line: the facts' qualified
// name (Server.LookupSymbol) when the document has them, else the scanner's.
func (st *sourceText) symbolAt(line int) string {
	if d := st.definitionAt(line); d != nil {
		return d.Name
	}
	if sp := st.enclosing(line); sp != nil {
		return sp.Symbol
	}
	return ""
}

// passage builds a Passage over lines start..end; withContent includes their
// text.
func (st *sourceText) passage(collection string, start, end int, source string, withContent bool) Passage {
	p := Passage{
		Collection: collection, Key: st.doc.Key, Lang: st.doc.Lang, Path: docPath(st.doc),
		Lines: PassageLines{Start: start, End: end}, Source: source, docTokens: approxTokens(st.doc.ContentMD),
	}
	p.Symbol = st.symbolAt(start)
	if withContent {
		p.Content = st.text(start, end)
		p.Tokens = approxTokens(p.Content)
	}
	return p
}
