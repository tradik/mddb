package main

import (
	"bytes"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// metaValueHit is one metadata-index entry found by value prefix.
type metaValueHit struct {
	Value string // the whole meta value
	DocID string
}

// metaValuesWithPrefix returns the index entries of metaKey whose value
// starts with valuePrefix — "every symRefs value naming a.go#Run" — at most
// limit of them, and whether there were more (CODE-010, for CODE-012).
//
// An entry is meta|coll|key|value|docID, and document IDs contain `|`
// (genID), so the value ends at the FIRST `|` after the prefix: fact values
// never contain one (the contract forbids it). A prefix holding `|` could
// therefore match nothing sensible and finds nothing.
func metaValuesWithPrefix(bIdx *bolt.Bucket, collection, metaKey, valuePrefix string, limit int) ([]metaValueHit, bool) {
	if bIdx == nil || strings.Contains(valuePrefix, "|") {
		return nil, false
	}
	seek := storage.MetaValuePrefix(collection, metaKey, valuePrefix)
	valueAt := len(seek) - len(valuePrefix)
	var hits []metaValueHit
	c := bIdx.Cursor()
	for k, _ := c.Seek(seek); k != nil && bytes.HasPrefix(k, seek); k, _ = c.Next() {
		rest := k[valueAt:]
		end := bytes.IndexByte(rest[len(valuePrefix):], '|')
		if end < 0 {
			continue // not an index entry this layout writes
		}
		if len(hits) >= limit {
			return hits, true
		}
		end += len(valuePrefix)
		hits = append(hits, metaValueHit{Value: string(rest[:end]), DocID: string(rest[end+1:])})
	}
	return hits, false
}
