package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/binlog"
	"mddb/internal/storage"
)

// Document keys and letter case (DOC-016, DOC-017).
//
// `genID` lowercases ASCII when it builds a document's identifier, so
// `README.md`, `readme.md` and `ReadMe.md` are one document. That is
// deliberate and reasonable for URL-shaped keys.
//
// Since DOC-017 the `bykey` index follows the same rule: storage.ByKeyKey
// normalises the key and the language, and the schema migration
// (internal/migrate) rewrote the entries older releases stored as written.
// Any spelling therefore finds the document, and a document has one index
// entry however many spellings were used.
//
// What stays here is the write path's report: a batch that carries two
// spellings of one key writes one document, the last one winning, and the
// response says so instead of absorbing it silently.

// keysCollideOnCase reports whether two distinct keys name one document.
func keysCollideOnCase(a, b string) bool {
	return a != b && storage.NormalizeKey(a) == storage.NormalizeKey(b)
}

// findCaseCollisions groups input keys that differ only in letter case.
//
// Returns one entry per collision, each listing the spellings involved in the
// order they arrived — so a report can say which write replaced which.
func findCaseCollisions(keys []string) [][]string {
	order := make([]string, 0, len(keys))
	byNormalized := make(map[string][]string, len(keys))

	for _, key := range keys {
		normalized := storage.NormalizeKey(key)
		if _, seen := byNormalized[normalized]; !seen {
			order = append(order, normalized)
		}
		// A key repeated verbatim is an ordinary overwrite, not a collision of
		// spellings, so only distinct spellings are recorded.
		if !containsString(byNormalized[normalized], key) {
			byNormalized[normalized] = append(byNormalized[normalized], key)
		}
	}

	var collisions [][]string
	for _, normalized := range order {
		if spellings := byNormalized[normalized]; len(spellings) > 1 {
			collisions = append(collisions, spellings)
		}
	}
	return collisions
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// describeKeyCollisions renders the case collisions among a batch's keys as
// messages fit for an API response.
//
// Returns nil when there are none, so the field stays out of the response
// entirely rather than appearing as an empty list on every import.
func describeKeyCollisions(docs []IngestDocumentHTTP) []string {
	keys := make([]string, 0, len(docs))
	for _, d := range docs {
		if d.Key != "" {
			keys = append(keys, d.Key)
		}
	}

	collisions := findCaseCollisions(keys)
	if len(collisions) == 0 {
		return nil
	}

	out := make([]string, 0, len(collisions))
	for _, spellings := range collisions {
		out = append(out, fmt.Sprintf(
			"%s name one document (keys are case-insensitive); the last write wins",
			strings.Join(quoteAll(spellings), " and ")))
	}
	return out
}

func quoteAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strconv.Quote(v)
	}
	return out
}

// deleteCollectionByKeyEntries removes every key-index entry for a collection.
//
// DOC-016: a collection-wide delete does not need to guess which spellings
// were written. Everything under the collection's prefix is going, so the
// prefix is what gets cleared — no orphan can survive a pass that does not
// consult a key at all.
func deleteCollectionByKeyEntries(bucket *bolt.Bucket, collection string, bo *binlog.BinlogOps) error {
	if bucket == nil {
		return nil
	}
	prefix := storage.ByKeyCollectionPrefix(collection)

	// Collected first: deleting through a cursor mid-iteration is not
	// something bbolt guarantees.
	var doomed [][]byte
	cursor := bucket.Cursor()
	for k, _ := cursor.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cursor.Next() {
		doomed = append(doomed, append([]byte(nil), k...))
	}

	for _, k := range doomed {
		if err := bucket.Delete(k); err != nil {
			return err
		}
		if bo != nil {
			bo.Delete("bykey", k)
		}
	}
	return nil
}
