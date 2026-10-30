package fts

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"mddb/internal/binlog"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// Incremental re-indexing (#367).
//
// Every update used to delete and rewrite all of a document's postings, even
// when only one meta key had changed or nothing had. Postings are one key per
// (term, document), so on a database larger than memory the rewrite is
// hundreds of random page reads per update: 2-3 s a document on spinning
// disks, against a few milliseconds for the keys that actually change.
//
// The index now remembers what it indexed. Each document carries a signature:
// a hash of its content, language and kind, and a hash of each field's text.
// IndexDocs compares the incoming document with that signature and does the
// least work that leaves the index exactly as a full re-index would:
//
//   - nothing indexed changed: nothing is written;
//   - content, language and kind unchanged: only the fields whose text
//     changed are rewritten, and the postings of fields that are gone are
//     removed;
//   - anything else, or no signature (a document indexed by an older
//     version, or not at all): the document is re-indexed from scratch.
//
// The decision is taken against the index, not against the caller's idea of
// the previous document, so every write path — single, batch, ingest, sync —
// gets it without passing anything, and a caller whose copy of the old
// document is stale cannot leave the index out of step.
//
// The signature is not written to the binlog. A replica applies the fts and
// ftsrev buckets it receives but not the per-field ones, so a replicated
// signature would claim fields the replica never indexed; without one, the
// replica re-indexes a document fully the first time it indexes it itself.

// docSignature is what the index holds for a document, in hashes.
type docSignature struct {
	// Base covers what every posting depends on: content, language, kind.
	Base string `json:"b"`
	// Fields maps each named field except content to a hash of its text.
	Fields map[string]string `json:"f,omitempty"`
}

// ftsSigKey is the signature's key in the ftsrev bucket.
func ftsSigKey(collection, docID string) []byte {
	return []byte(fmt.Sprintf("ftssig|%s|%s", collection, docID))
}

// textHash is the first 8 bytes of a SHA-256 over the parts, each prefixed
// with its length so that no two different lists hash alike by concatenation.
func textHash(parts ...string) string {
	h := sha256.New()
	var n [binary.MaxVarintLen64]byte
	for _, p := range parts {
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(p)))])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// signatureOf is the signature a document will have once indexed.
func signatureOf(d *BulkDoc) docSignature {
	sig := docSignature{Base: textHash(d.Content, d.Lang, d.Kind)}
	for field, text := range d.Fields {
		if field == contentField {
			continue // part of Base: BM25F reads it from the main index
		}
		if sig.Fields == nil {
			sig.Fields = make(map[string]string, len(d.Fields))
		}
		sig.Fields[field] = textHash(text)
	}
	return sig
}

// loadSignature reads a document's signature; ok is false when it has none
// or it cannot be read, which both mean "re-index from scratch".
func loadSignature(tx *bolt.Tx, collection, docID string) (docSignature, bool) {
	var sig docSignature
	b := tx.Bucket(bucketFTSRev)
	if b == nil {
		return sig, false
	}
	raw := b.Get(ftsSigKey(collection, docID))
	if raw == nil || json.Unmarshal(raw, &sig) != nil {
		return sig, false
	}
	return sig, true
}

// putSignature stores a document's signature.
func putSignature(tx *bolt.Tx, collection, docID string, sig docSignature) error {
	raw, _ := json.Marshal(sig) // strings and a map of strings always encode
	return tx.Bucket(bucketFTSRev).Put(ftsSigKey(collection, docID), raw)
}

// indexPlan is the work one document needs.
type indexPlan struct {
	full   bool
	fields []string // full == false: the fields to rewrite or remove
}

func (p indexPlan) skip() bool { return !p.full && len(p.fields) == 0 }

// planFor compares a document with what the index holds for it.
func planFor(tx *bolt.Tx, collection string, w *pendingDoc) indexPlan {
	if w.doc.Rebuild {
		return indexPlan{full: true}
	}
	old, ok := loadSignature(tx, collection, w.doc.DocID)
	if !ok || old.Base != w.sig.Base {
		return indexPlan{full: true}
	}
	var fields []string
	for field, h := range w.sig.Fields {
		if old.Fields[field] != h {
			fields = append(fields, field)
		}
	}
	for field := range old.Fields {
		if _, kept := w.sig.Fields[field]; !kept {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	return indexPlan{fields: fields}
}

// pendingDoc is one document on its way into the index. Tokens are computed
// on first use and kept, so the work a plan needs can be done before the
// write transaction opens and a plan that changes inside it costs nothing
// twice.
type pendingDoc struct {
	doc       *BulkDoc
	sig       docSignature
	plan      indexPlan
	contentOK bool
	terms     map[string]int
	positions map[string][]uint32
	fields    map[string]map[string]int
}

func newPendingDoc(d *BulkDoc) *pendingDoc {
	return &pendingDoc{doc: d, sig: signatureOf(d), fields: make(map[string]map[string]int)}
}

// tokenize splits text with the tokeniser the document's kind calls for.
func (f *FTSIndex) tokenize(w *pendingDoc, text string) map[string]int {
	if w.doc.Kind == KindCode {
		return TokenizeCode(text)
	}
	return f.TokenizeLang(text, w.doc.Lang)
}

// content returns the document's terms and positions.
func (f *FTSIndex) content(w *pendingDoc) (map[string]int, map[string][]uint32) {
	if !w.contentOK && w.doc.Content != "" {
		w.terms = f.tokenize(w, w.doc.Content)
		if w.doc.Kind == KindCode {
			w.positions = f.TokenizePositionsCode(w.doc.Content)
		} else {
			w.positions = f.TokenizePositionsLang(w.doc.Content, w.doc.Lang)
		}
	}
	w.contentOK = true
	return w.terms, w.positions
}

// fieldTokens returns a field's tokens; a field the document no longer has
// has none.
func (f *FTSIndex) fieldTokens(w *pendingDoc, field string) map[string]int {
	tokens, done := w.fields[field]
	if !done {
		if text, ok := w.doc.Fields[field]; ok {
			tokens = f.tokenize(w, text)
		}
		w.fields[field] = tokens
	}
	return tokens
}

// prepare tokenises what a plan needs.
func (f *FTSIndex) prepare(w *pendingDoc, p indexPlan) {
	if !p.full {
		for _, field := range p.fields {
			f.fieldTokens(w, field)
		}
		return
	}
	f.content(w)
	for field := range w.doc.Fields {
		if field != contentField {
			f.fieldTokens(w, field)
		}
	}
}

// errFieldRevUnreadable means a document's field reverse index cannot be
// decoded, so its old field postings cannot be found one field at a time.
var errFieldRevUnreadable = errors.New("fts: field reverse index unreadable")

// applyInTx brings one document's postings in line with it and records its
// signature.
func (f *FTSIndex) applyInTx(tx *bolt.Tx, bo *binlog.BinlogOps, put putFunc, collection string, w *pendingDoc, p indexPlan) error {
	if p.skip() {
		return nil
	}
	if !p.full {
		f.prepare(w, p)
		tokens := make(map[string]map[string]int, len(p.fields))
		for _, field := range p.fields {
			tokens[field] = w.fields[field]
		}
		err := replaceFieldsInTx(tx, put, collection, w.doc.DocID, tokens)
		if errors.Is(err, errFieldRevUnreadable) {
			p = indexPlan{full: true}
		} else if err != nil {
			return err
		}
	}
	if p.full {
		if err := f.reindexInTx(tx, bo, put, collection, w); err != nil {
			return err
		}
	}
	return putSignature(tx, collection, w.doc.DocID, w.sig)
}

// reindexInTx indexes one document from scratch: everything it had is
// removed first, so what remains is exactly what indexing it into an empty
// index would write.
func (f *FTSIndex) reindexInTx(tx *bolt.Tx, bo *binlog.BinlogOps, put putFunc, collection string, w *pendingDoc) error {
	f.prepare(w, indexPlan{full: true})
	docID := w.doc.DocID
	if err := f.removeInTx(tx, bo, collection, docID); err != nil {
		return err
	}
	terms, positions := f.content(w)
	if len(terms) > 0 {
		if err := f.indexTermsInTx(tx, bo, put, collection, docID, terms); err != nil {
			return err
		}
	}
	if len(positions) > 0 {
		if err := f.indexPositionsInTx(tx, put, collection, docID, positions); err != nil {
			return err
		}
	}
	fields := make(map[string]map[string]int, len(w.fields))
	for field, tokens := range w.fields {
		if len(tokens) > 0 {
			fields[field] = tokens
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return f.indexFieldTokensInTx(tx, put, collection, docID, fields)
}

// replaceFieldsInTx rewrites the named fields of one document's per-field
// index and leaves its other fields as they are. A field with no tokens loses
// its postings, length and share of the field statistics.
func replaceFieldsInTx(tx *bolt.Tx, put putFunc, collection, docID string, tokens map[string]map[string]int) error {
	bF, bMeta, bStat, bRev := tx.Bucket(bucketFTSF), tx.Bucket(bucketFTSFMeta), tx.Bucket(bucketFTSFStat), tx.Bucket(bucketFTSFRev)
	if bF == nil || bMeta == nil || bStat == nil || bRev == nil {
		return nil
	}
	revKey := ftsfRevKey(collection, docID)
	var entries []fieldTermEntry
	if raw := bRev.Get(revKey); raw != nil {
		if err := json.Unmarshal(raw, &entries); err != nil {
			return errFieldRevUnreadable
		}
	}

	kept := entries[:0]
	for _, e := range entries {
		if _, replaced := tokens[e.Field]; replaced {
			_ = bF.Delete(ftsfKey(collection, e.Field, e.Term, docID))
		} else {
			kept = append(kept, e)
		}
	}
	for field, fieldTokens := range tokens {
		dropFieldLength(tx, collection, field, docID)
		written, err := writeFieldInTx(put, bF, bMeta, bStat, collection, field, docID, fieldTokens)
		if err != nil {
			return err
		}
		kept = append(kept, written...)
	}
	return putFieldRev(bRev, revKey, kept)
}
