package main

import (
	"strings"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// How a document enters the full-text index, in one place (#326).
//
// Only the bulk path passed a document's kind to the index, so only documents
// written in a batch were tokenised as code. A source file saved with /v1/add,
// updated in place, or rebuilt by fts-reindex (HTTP, gRPC or MCP) was indexed
// as prose: stemmed, stop words dropped, `do_parse` stored as `do` and
// `parse`. The same file was searchable or not depending on how it had been
// written. Every path now builds the same BulkDoc and goes through IndexDocs.

// ftsBulkDoc describes a document for the full-text index: its content, its
// fields for BM25F, and its kind, which selects the tokeniser. Meta keys in
// exclude get no field (#367).
func ftsBulkDoc(doc *storage.Doc, exclude map[string]bool) fts.BulkDoc {
	fields := map[string]string{"content": doc.ContentMD}
	for k, vals := range doc.Meta {
		if len(vals) > 0 && !exclude[k] {
			fields["meta."+k] = strings.Join(vals, " ")
		}
	}
	return fts.BulkDoc{
		DocID:   doc.ID,
		Content: doc.ContentMD,
		Lang:    doc.Lang,
		// CODE-001: source is tokenised differently from prose. The kind
		// comes from meta, or from the key's extension when meta does not say.
		Kind:   DocumentKind(doc),
		Fields: fields,
	}
}

// indexDocumentFTS indexes one document. The index writes only what changed
// since it last indexed the document, and nothing when nothing did (#367);
// rebuild re-indexes it from scratch, for an fts-reindex.
func (s *Server) indexDocumentFTS(collection string, doc *storage.Doc, rebuild bool) error {
	if s.FTSIndex == nil || doc.ContentMD == "" || !s.ftsEnabledFor(collection) {
		return nil
	}
	bd := ftsBulkDoc(doc, s.ftsExcludedMetaKeys(collection))
	bd.Rebuild = rebuild
	return s.FTSIndex.IndexDocs(collection, []fts.BulkDoc{bd})
}
