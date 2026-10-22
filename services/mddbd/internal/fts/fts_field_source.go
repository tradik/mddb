package fts

import (
	bolt "go.etcd.io/bbolt"
)

// contentField is the BM25F field for a document's body. Its postings, lengths
// and statistics are those of the main inverted index — the same tokens,
// counted the same way — so it is read from there instead of being stored a
// second time in the per-field index (#317). Storing it twice cost a third of
// every batch's index writes.
//
// Databases written before this change still hold per-field "content" entries;
// nothing reads them, and re-indexing a document removes its own.
const contentField = "content"

// fieldSource is where one BM25F field's data lives.
type fieldSource struct {
	postings *bolt.Bucket // keys: base + term + "|" + docID, value: uint32 tf
	base     string
	lengths  *bolt.Bucket // document length per docID, uint32
	lenKey   func(docID string) []byte
	stats    []byte // encoded collectionStats, nil if none
}

// termPrefix is the key prefix of every posting for term in this field.
func (s fieldSource) termPrefix(term string) []byte {
	return []byte(s.base + term + "|")
}

// fieldSourceFor resolves a field to its buckets. ok is false when the
// buckets it needs do not exist.
func fieldSourceFor(tx *bolt.Tx, collection, field string) (fieldSource, bool) {
	if field == contentField {
		post, rev := tx.Bucket(bucketFTS), tx.Bucket(bucketFTSRev)
		if post == nil || rev == nil {
			return fieldSource{}, false
		}
		return fieldSource{
			postings: post,
			base:     "fts|" + collection + "|",
			lengths:  rev,
			lenKey:   func(docID string) []byte { return ftsMetaKey(collection, docID) },
			stats:    rev.Get(ftsStatKey(collection)),
		}, true
	}
	post, meta, stat := tx.Bucket(bucketFTSF), tx.Bucket(bucketFTSFMeta), tx.Bucket(bucketFTSFStat)
	if post == nil || meta == nil || stat == nil {
		return fieldSource{}, false
	}
	return fieldSource{
		postings: post,
		base:     "ftsf|" + collection + "|" + field + "|",
		lengths:  meta,
		lenKey:   func(docID string) []byte { return ftsfMetaKey(collection, field, docID) },
		stats:    stat.Get(ftsfStatKey(collection, field)),
	}, true
}

// fieldSources resolves every field with a positive weight.
func fieldSources(tx *bolt.Tx, collection string, weights map[string]float64) map[string]fieldSource {
	out := make(map[string]fieldSource, len(weights))
	for field, w := range weights {
		if w <= 0 {
			continue
		}
		if src, ok := fieldSourceFor(tx, collection, field); ok {
			out[field] = src
		}
	}
	return out
}
