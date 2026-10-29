package vector

import (
	"bytes"
	"mddb/internal/boltx"

	"mddb/internal/binlog"

	bolt "go.etcd.io/bbolt"
)

// DeleteCollection removes every stored vector of a collection — chunked and
// legacy records alike, the summary vectors (RAG-009) and the image vectors
// (RAG-010) — and records each
// deletion in the binlog so that followers drop them too (#331). It returns
// how many records it removed.
func (vs *VectorStore) DeleteCollection(collection string) (int, error) {
	prefixes := [][]byte{[]byte("vec|" + collection + "|"), SummaryKey(collection, ""), ImageKey(collection, "")}
	var bo binlog.BinlogOps
	removed := 0
	err := boltx.Update(vs.rt(), collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		// Collected first: deleting under a live cursor skips keys.
		var keys [][]byte
		c := b.Cursor()
		for _, prefix := range prefixes {
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				keys = append(keys, append([]byte(nil), k...))
			}
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
			bo.Delete("vectors", k)
		}
		removed = len(keys)
		return nil
	})
	if err != nil {
		return 0, err
	}
	bo.FlushTo(vs.binlog)
	return removed, nil
}
