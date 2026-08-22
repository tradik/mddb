package fts

import (
	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// Bulk indexing (GO-027).
//
// Indexing a document touches three indexes — terms, positions and fields —
// and each entry point opened its own write transaction. A bulk import
// therefore paid three BoltDB commits per document: measured on this tree,
// 1000 documents took 4.2s with indexing against 21ms without it, so the
// commits, not the work, were the cost.
//
// IndexDocs runs the same per-document bodies inside one transaction for a
// whole batch. It is the same code the single-document path uses, so the two
// cannot drift apart; only the transaction boundary moves.

// BulkDoc is one document to index, already carrying everything the three
// indexes need.
type BulkDoc struct {
	DocID   string
	Content string
	Lang    string
	// Kind selects the tokeniser. "code" uses the source-aware one (CODE-001),
	// which keeps identifiers whole, emits their parts, and neither stems nor
	// drops keywords; anything else is prose. It comes from the document's
	// meta["kind"], so the convention lives in the data rather than in a new
	// document type.
	Kind string
	// Fields are the named, separately-searchable texts (content plus
	// meta.<key>); nil skips field indexing for this document.
	Fields map[string]string
}

// IndexDocs indexes a batch of documents in a single write transaction.
//
// Tokenisation happens before the transaction opens: it is pure CPU work on
// the caller's data and holding the write lock through it would serialise
// every other writer behind the import for no reason.
//
// The batch is atomic. A failure part-way rolls the whole batch back rather
// than leaving some documents indexed, which keeps a failed import from
// leaving the index describing documents that were never written.
func (f *FTSIndex) IndexDocs(collection string, docs []BulkDoc) error {
	if len(docs) == 0 {
		return nil
	}

	type prepared struct {
		docID     string
		terms     map[string]int
		positions map[string][]uint32
		fields    map[string]map[string]int
	}

	batch := make([]prepared, 0, len(docs))
	for _, d := range docs {
		p := prepared{docID: d.DocID}
		isCode := d.Kind == KindCode
		if d.Content != "" {
			if isCode {
				p.terms = TokenizeCode(d.Content)
				p.positions = f.TokenizePositionsCode(d.Content)
			} else {
				p.terms = f.TokenizeLang(d.Content, d.Lang)
				p.positions = f.TokenizePositionsLang(d.Content, d.Lang)
			}
		}
		if len(d.Fields) > 0 {
			p.fields = make(map[string]map[string]int, len(d.Fields))
			for field, text := range d.Fields {
				var tokens map[string]int
				if isCode {
					tokens = TokenizeCode(text)
				} else {
					tokens = f.TokenizeLang(text, d.Lang)
				}
				if len(tokens) > 0 {
					p.fields[field] = tokens
				}
			}
		}
		batch = append(batch, p)
	}

	var bo binlog.BinlogOps
	err := f.db.Update(func(tx *bolt.Tx) error {
		for _, p := range batch {
			if len(p.terms) > 0 {
				if err := f.indexTermsInTx(tx, &bo, collection, p.docID, p.terms); err != nil {
					return err
				}
			}
			if len(p.positions) > 0 {
				if err := f.indexPositionsInTx(tx, collection, p.docID, p.positions); err != nil {
					return err
				}
			}
			if len(p.fields) > 0 {
				if err := f.indexFieldTokensInTx(tx, collection, p.docID, p.fields); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		bo.FlushTo(f.binlog)
		f.InvalidatePMI(collection)
	}
	return err
}
