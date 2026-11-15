package main

import (
	"fmt"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// DocRef names one document of a collection by its key and language. An
// export request lists them in `keys` to export just those documents
// (FE-014: the panel's "Export selected").
type DocRef struct {
	Key  string `json:"key"`
	Lang string `json:"lang"`
}

// validateDocRefs refuses a key list with an entry that does not name a
// document: both the key and the language are required, as for a delete.
func validateDocRefs(refs []DocRef) error {
	for i, r := range refs {
		if r.Key == "" || r.Lang == "" {
			return fmt.Errorf("keys[%d]: key and lang are required", i)
		}
	}
	return nil
}

// exportSelection reads the documents an export selects inside tx.
//
//   - keys given: those documents, in the order listed, each once; one that
//     does not exist is skipped. A filter, when also given, narrows them
//     further (keys AND filter).
//   - no keys, no filter: the whole collection in key order.
//   - no keys, a filter: the matching documents in ID order.
func exportSelection(tx *bolt.Tx, collection string, filter MetaFilter, keys []DocRef) []storage.Doc {
	bDocs := tx.Bucket([]byte("docs"))
	switch {
	case len(keys) > 0:
		ids := keyedIDs(tx.Bucket([]byte("bykey")), collection, keys)
		if len(filter) > 0 {
			ids = keepIDs(ids, metaFilterIDs(tx, collection, filter))
		}
		return docsByID(bDocs, collection, ids)
	case len(filter) == 0:
		return collectionDocs(bDocs, collection)
	default:
		return docsByID(bDocs, collection, sortedIDs(metaFilterIDs(tx, collection, filter)))
	}
}

// keyedIDs resolves keys to document IDs through the key index, in the order
// listed; a key that names no document is skipped and a document named twice
// (two spellings of one key) is listed once.
func keyedIDs(bByKey *bolt.Bucket, collection string, keys []DocRef) []string {
	ids := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		id := bByKey.Get(storage.ByKeyKey(collection, k.Key, k.Lang))
		if id == nil || seen[string(id)] {
			continue
		}
		seen[string(id)] = true
		ids = append(ids, string(id))
	}
	return ids
}

// keepIDs returns the ids that are in set, preserving their order.
func keepIDs(ids []string, set map[string]bool) []string {
	kept := ids[:0]
	for _, id := range ids {
		if set[id] {
			kept = append(kept, id)
		}
	}
	return kept
}

// mcpGetDocRefs reads a tool argument holding an array of {key, lang}
// objects. An entry that is not an object becomes an empty DocRef, so
// validateDocRefs reports it by index rather than the export dropping it.
func mcpGetDocRefs(m map[string]interface{}, key string) []DocRef {
	items, _ := m[key].([]interface{})
	if len(items) == 0 {
		return nil
	}
	refs := make([]DocRef, len(items))
	for i, item := range items {
		obj, _ := item.(map[string]interface{})
		refs[i] = DocRef{Key: mcpGetString(obj, "key"), Lang: mcpGetString(obj, "lang")}
	}
	return refs
}
