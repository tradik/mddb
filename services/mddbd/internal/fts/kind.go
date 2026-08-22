package fts

// Document kinds (CODE-001).
//
// A document's kind selects how it is tokenised. It travels in the ordinary
// flat meta map — meta["kind"] = ["code"] — rather than in a new document
// type or a schema change, because the flat model is deliberate (issue #187)
// and every transport already carries meta.

const (
	// KindCode marks a document as source, so it is indexed with the
	// code-aware tokeniser instead of the prose one.
	KindCode = "code"

	// MetaKeyKind is the meta key carrying the kind.
	MetaKeyKind = "kind"
	// MetaKeyLanguage is the meta key carrying the source language
	// ("css", "html", "javascript", ...). Inferred from the document key's
	// extension when absent.
	MetaKeyLanguage = "language"
	// MetaKeyPath is the meta key carrying the original file path, for code
	// ingested from a tree whose document keys are slugs.
	MetaKeyPath = "path"
)
