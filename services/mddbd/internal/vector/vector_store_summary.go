package vector

import (
	"bytes"
	"fmt"
	"mddb/internal/binlog"
	"mddb/internal/boltx"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Summary vectors (RAG-009).
//
// A document may carry a summary its client wrote, embedded as one vector of
// its own. It lives in the vectors bucket — so it moves with the collection
// in the per-collection layout, replicates through the same binlog bucket and
// is in every backup — but under its own key prefix, "vsum|", which nothing
// that walks chunk vectors ("vec|") ever sees: index loading, chunk reuse,
// stale-chunk cleanup, counts and provenance stay exactly what they were.

// SummaryKeyPrefix starts every summary vector key: vsum|collection|docID.
const SummaryKeyPrefix = "vsum|"

// SummaryKey builds the key of a document's summary vector.
func SummaryKey(collection, docID string) []byte {
	return []byte(SummaryKeyPrefix + collection + "|" + docID)
}

// IsSummaryKey reports whether a vectors-bucket key holds a summary vector.
func IsSummaryKey(key []byte) bool {
	return bytes.HasPrefix(key, []byte(SummaryKeyPrefix))
}

// PutSummary stores a document's summary vector. summaryHash is the hash of
// the summary text, which is what lets an unchanged summary skip the
// provider on its next write (as RAG-003 does for chunks).
func (vs *VectorStore) PutSummary(collection, docID string, vector []float32, model, summaryHash string) error {
	return vs.putDocVector(SummaryKey(collection, docID), collection, docID, vector, model, summaryHash)
}

// GetSummary returns a document's summary vector record, nil when it has none.
func (vs *VectorStore) GetSummary(collection, docID string) *EmbeddingRecord {
	return vs.getDocVector(collection, SummaryKey(collection, docID))
}

// DeleteSummary removes a document's summary vector, if it has one.
func (vs *VectorStore) DeleteSummary(collection, docID string) error {
	return vs.deleteDocVector(collection, SummaryKey(collection, docID))
}

// LoadSummaries returns a collection's summary vectors keyed by document ID.
func (vs *VectorStore) LoadSummaries(collection string) map[string][]float32 {
	return vs.loadDocVectors(collection, SummaryKey(collection, ""))
}

// SummaryVectors reads the summary vectors of the given documents in one read
// transaction — the disk half of a disk-only collection's summary search.
func (vs *VectorStore) SummaryVectors(collection string, docIDs []string) map[string][]float32 {
	return vs.docVectors(collection, docIDs, func(id string) []byte { return SummaryKey(collection, id) })
}

// One vector per document (RAG-009 summaries, RAG-010 images): a record under
// a key of its own prefix, read and written the same way whatever it holds.

// putDocVector stores a document-level vector record under key.
func (vs *VectorStore) putDocVector(key []byte, collection, docID string, vector []float32, model, hash string) error {
	data := MarshalEmbeddingRecord(&EmbeddingRecord{
		DocID: docID, Vector: vector, Model: model, Dimensions: len(vector),
		CreatedAt: time.Now().Unix(), ContentHash: hash,
	})
	err := boltx.Update(vs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return fmt.Errorf("vectors bucket not found")
		}
		return b.Put(key, data)
	})
	if err == nil && vs.binlog != nil {
		_ = vs.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "vectors", Key: bytes.Clone(key), Value: bytes.Clone(data)})
	}
	return err
}

// getDocVector returns the record under key, nil when there is none.
func (vs *VectorStore) getDocVector(collection string, key []byte) *EmbeddingRecord {
	var rec *EmbeddingRecord
	_ = boltx.View(vs.rt(), collection, func(tx *bolt.Tx) error {
		if b := tx.Bucket(vs.bucketName); b != nil {
			if v := b.Get(key); v != nil {
				rec, _ = UnmarshalEmbeddingRecord(v)
			}
		}
		return nil
	})
	return rec
}

// deleteDocVector removes the record under key, if there is one.
func (vs *VectorStore) deleteDocVector(collection string, key []byte) error {
	removed := false
	err := boltx.Update(vs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil || b.Get(key) == nil {
			return nil
		}
		removed = true
		return b.Delete(key)
	})
	if err == nil && removed && vs.binlog != nil {
		_ = vs.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "vectors", Key: bytes.Clone(key)})
	}
	return err
}

// loadDocVectors returns the vectors under prefix keyed by document ID.
func (vs *VectorStore) loadDocVectors(collection string, prefix []byte) map[string][]float32 {
	out := map[string][]float32{}
	_ = boltx.View(vs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			if rec, err := UnmarshalEmbeddingRecord(v); err == nil {
				out[string(k[len(prefix):])] = rec.Vector
			}
		}
		return nil
	})
	return out
}

// docVectors reads the vectors of the given documents in one transaction.
func (vs *VectorStore) docVectors(collection string, docIDs []string, key func(string) []byte) map[string][]float32 {
	out := make(map[string][]float32, len(docIDs))
	_ = boltx.View(vs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		for _, id := range docIDs {
			if v := b.Get(key(id)); v != nil {
				if rec, err := UnmarshalEmbeddingRecord(v); err == nil {
					out[id] = rec.Vector
				}
			}
		}
		return nil
	})
	return out
}

// DocumentChunkVectors returns a document's stored chunk vectors in chunk
// order, read from disk: the chunks of a document found by its summary, for
// a collection whose float32 vectors are not in RAM.
func (vs *VectorStore) DocumentChunkVectors(collection, docID string) [][]float32 {
	var out [][]float32
	_ = boltx.View(vs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		for i := 0; ; i++ {
			v := b.Get(buildChunkKey(collection, docID, i))
			if v == nil {
				return nil
			}
			var rec *EmbeddingRecord
			var err error
			if isQuantizedRecord(v) {
				rec, _, err = unmarshalEmbeddingRecordQuantized(v)
			} else {
				rec, err = UnmarshalEmbeddingRecord(v)
			}
			if err != nil {
				return nil
			}
			out = append(out, rec.Vector)
		}
	})
	return out
}
