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
	Kind    string  `json:"kind,omitempty"`
	Content string  `json:"content,omitempty"`
	Score   float64 `json:"score,omitempty"`
	// Source says what produced the passage: fts, vector, hybrid, cross, or a
	// symbol relation (definition, reference, caller, callee, test, sibling).
	Source string `json:"source,omitempty"`
	// Tokens estimates Content's size with the context budget's estimator.
	Tokens int `json:"tokens"`
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
}

func newSourceText(doc *storage.Doc) *sourceText {
	st := &sourceText{doc: doc, lines: strings.Split(doc.ContentMD, "\n")}
	if DocumentKind(doc) == fts.KindCode {
		st.spans = codeintel.Spans(CodeLanguage(doc), doc.ContentMD)
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

// passage builds a Passage over lines start..end; withContent includes their
// text.
func (st *sourceText) passage(collection string, start, end int, source string, withContent bool) Passage {
	p := Passage{
		Collection: collection, Key: st.doc.Key, Lang: st.doc.Lang, Path: docPath(st.doc),
		Lines: PassageLines{Start: start, End: end}, Source: source,
	}
	if sp := st.enclosing(start); sp != nil {
		p.Symbol = sp.Symbol
	}
	if withContent {
		p.Content = st.text(start, end)
		p.Tokens = approxTokens(p.Content)
	}
	return p
}
