package fts

import (
	"bytes"
	"mddb/internal/binlog"
	"sort"

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
	// meta.<key>); nil means the document has none, and a re-index removes
	// any it had.
	Fields map[string]string
	// Rebuild indexes the document from scratch even when the index says
	// nothing changed (#367). An fts-reindex sets it: a new stemmer, stop
	// word list or field exclusion changes the postings without changing
	// the document.
	Rebuild bool
}

// IndexDocs indexes a batch of documents in a single write transaction,
// writing for each only what changed since it was last indexed (#367, see
// fts_incremental.go).
//
// The plan is made in a read transaction first, so a batch in which nothing
// indexed changed never opens a write transaction. Tokenisation then happens
// before the write transaction opens: it is pure CPU work on the caller's
// data and holding the write lock through it would serialise every other
// writer behind the import for no reason. Inside the write transaction each
// document is planned again, because another writer may have indexed it in
// between; the tokens already computed are reused.
//
// The batch is atomic. A failure part-way rolls the whole batch back rather
// than leaving some documents indexed, which keeps a failed import from
// leaving the index describing documents that were never written.
func (f *FTSIndex) IndexDocs(collection string, docs []BulkDoc) error {
	if len(docs) == 0 {
		return nil
	}
	work := make([]*pendingDoc, len(docs))
	changed := false
	err := f.view(collection, func(tx *bolt.Tx) error {
		for i := range docs {
			work[i] = newPendingDoc(&docs[i])
			work[i].plan = planFor(tx, collection, work[i])
			changed = changed || !work[i].plan.skip()
		}
		return nil
	})
	if err != nil || !changed {
		return err
	}
	for _, w := range work {
		f.prepare(w, w.plan)
	}

	var bo binlog.BinlogOps
	err = f.update(collection, func(tx *bolt.Tx) error {
		var deferred sortedPuts
		seen := make(map[string]bool, len(work))
		for _, w := range work {
			// A document listed twice deletes its earlier postings through
			// the reverse index; they must be in the tree before it does.
			// Its later entry is planned against the earlier one, which is
			// why a document planned as unchanged above is not dropped.
			if seen[w.doc.DocID] {
				if err := deferred.flush(); err != nil {
					return err
				}
			}
			seen[w.doc.DocID] = true
			if err := f.applyInTx(tx, &bo, deferred.put, collection, w, planFor(tx, collection, w)); err != nil {
				return err
			}
		}
		return deferred.flush()
	})
	if err == nil {
		bo.FlushTo(f.binlog)
		f.InvalidatePMI(collection)
	}
	return err
}

// putFunc writes one posting. Single-document indexing writes at once
// (putNow); a batch collects its postings and writes them in key order
// (sortedPuts).
type putFunc func(b *bolt.Bucket, key, value []byte) error

func putNow(b *bolt.Bucket, key, value []byte) error { return b.Put(key, value) }

// sortedPuts defers a batch's postings and writes them grouped by bucket and
// sorted by key (#317). Postings are one key per (term, document), so a batch
// arrives in an order that jumps across the whole tree; bbolt then searches
// from the root for every key. In key order, consecutive writes land on the
// same leaf.
type sortedPuts struct {
	pending []pendingPut
	order   map[*bolt.Bucket]int
}

type pendingPut struct {
	bucket     int
	b          *bolt.Bucket
	key, value []byte
}

func (s *sortedPuts) put(b *bolt.Bucket, key, value []byte) error {
	if s.order == nil {
		s.order = make(map[*bolt.Bucket]int)
	}
	n, ok := s.order[b]
	if !ok {
		n = len(s.order)
		s.order[b] = n
	}
	s.pending = append(s.pending, pendingPut{n, b, key, bytes.Clone(value)})
	return nil
}

func (s *sortedPuts) flush() error {
	sort.Slice(s.pending, func(i, j int) bool {
		if s.pending[i].bucket != s.pending[j].bucket {
			return s.pending[i].bucket < s.pending[j].bucket
		}
		return bytes.Compare(s.pending[i].key, s.pending[j].key) < 0
	})
	for _, p := range s.pending {
		if err := p.b.Put(p.key, p.value); err != nil {
			return err
		}
	}
	s.pending = s.pending[:0]
	return nil
}
