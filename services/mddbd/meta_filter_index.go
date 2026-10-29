package main

import (
	"bytes"
	"sort"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// Index evaluation of a MetaFilter (SRCH-017).
//
// Every operator is answered from the idxmeta bucket, whose keys are
// "meta|<collection>|<key>|<value>|<docID>". Nothing here reads a document
// body: in and prefix seek straight to their range, the other value operators
// walk the entries of one key, and only not / exists:false — which have to
// know which documents lack something — list document keys, never values.

// metaFilterIDs returns the IDs of documents matching the filter. A nil filter
// is not a restriction; callers decide that before calling (see allowedDocIDs).
func metaFilterIDs(tx *bolt.Tx, collection string, f MetaFilter) map[string]bool {
	bIdx := tx.Bucket([]byte("idxmeta"))
	var universe map[string]bool
	all := func() map[string]bool {
		if universe == nil {
			universe = collectionDocIDs(tx, collection)
		}
		return universe
	}

	var result map[string]bool
	for _, key := range f.sortedKeys() {
		set := conditionIDs(bIdx, collection, key, f[key], all)
		if result == nil {
			result = set
		} else {
			result = intersectSets(result, set)
		}
		if len(result) == 0 {
			return map[string]bool{}
		}
	}
	if result == nil {
		return map[string]bool{}
	}
	return result
}

// conditionIDs returns the documents meeting one key's condition.
func conditionIDs(bIdx *bolt.Bucket, collection, key string, c MetaCondition, all func() map[string]bool) map[string]bool {
	var set map[string]bool // nil = no constraint yet (every document)
	if c.hasValuePredicate() {
		set = valueMatches(bIdx, collection, key, c)
	}
	if c.Exists != nil {
		withKey := valueMatches(bIdx, collection, key, MetaCondition{})
		if *c.Exists {
			set = narrow(set, withKey)
		} else {
			set = subtract(orAll(set, all), withKey)
		}
	}
	if len(c.Not) > 0 {
		excluded := valueMatches(bIdx, collection, key, MetaCondition{In: c.Not, hasIn: true})
		set = subtract(orAll(set, all), excluded)
	}
	if set == nil {
		return all()
	}
	return set
}

// valueMatches returns documents with at least one value of key satisfying
// the value operators. A condition without value operators returns every
// document that has the key at all.
func valueMatches(bIdx *bolt.Bucket, collection, key string, c MetaCondition) map[string]bool {
	out := map[string]bool{}
	if bIdx == nil {
		return out
	}
	keyPrefix := []byte("meta|" + collection + "|" + key + "|")

	// "in" with nothing else is a set of exact seeks — the filterMeta path.
	if (c.hasIn || len(c.In) > 0) && c.onlyIn() {
		for _, v := range c.In {
			p := storage.MetaKeyPrefix(collection, key, v)
			cur := bIdx.Cursor()
			for k, _ := cur.Seek(p); k != nil && bytes.HasPrefix(k, p); k, _ = cur.Next() {
				out[string(k[len(p):])] = true
			}
		}
		return out
	}

	// A prefix narrows the walk to its own range of the index.
	seek := keyPrefix
	if c.Prefix != "" {
		seek = append(append([]byte{}, keyPrefix...), c.Prefix...)
	}
	cur := bIdx.Cursor()
	for k, _ := cur.Seek(seek); k != nil && bytes.HasPrefix(k, seek); k, _ = cur.Next() {
		value, id, ok := splitMetaEntry(k[len(keyPrefix):], collection)
		if ok && c.matchValue(value) {
			out[id] = true
		}
	}
	return out
}

// onlyIn reports whether "in" is the condition's only value operator.
func (c MetaCondition) onlyIn() bool {
	return c.Prefix == "" && c.Contains == "" && c.Gt == nil && c.Gte == nil && c.Lt == nil && c.Lte == nil
}

// splitMetaEntry splits "<value>|<docID>". A document ID is
// "<collection>|<key>|<lang>", so the boundary is the first "|<collection>|"
// after the value. A value that itself contains "|<collection>|" would be
// cut short; collection names are not something meta values carry.
func splitMetaEntry(rest []byte, collection string) (value, id string, ok bool) {
	i := bytes.Index(rest, []byte("|"+collection+"|"))
	if i < 0 {
		return "", "", false
	}
	return string(rest[:i]), string(rest[i+1:]), true
}

// collectionDocIDs lists the IDs of every document in the collection from the
// document keys alone.
func collectionDocIDs(tx *bolt.Tx, collection string) map[string]bool {
	out := map[string]bool{}
	b := tx.Bucket([]byte("docs"))
	if b == nil {
		return out
	}
	prefix := []byte("doc|" + collection + "|")
	cur := b.Cursor()
	for k, _ := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cur.Next() {
		out[string(k[len(prefix):])] = true
	}
	return out
}

func orAll(set map[string]bool, all func() map[string]bool) map[string]bool {
	if set == nil {
		return all()
	}
	return set
}

func narrow(set, by map[string]bool) map[string]bool {
	if set == nil {
		return by
	}
	return intersectSets(set, by)
}

func intersectSets(a, b map[string]bool) map[string]bool {
	if len(b) < len(a) {
		a, b = b, a
	}
	out := make(map[string]bool, len(a))
	for id := range a {
		if b[id] {
			out[id] = true
		}
	}
	return out
}

func subtract(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a))
	for id := range a {
		if !b[id] {
			out[id] = true
		}
	}
	return out
}

// allowedDocIDs resolves filterMeta and filter (ANDed) into the set of
// documents a search may return. active is false when neither is given, which
// means "no restriction" — distinct from an empty set, which means "nothing".
func (s *Server) allowedDocIDs(collection string, filterMeta map[string][]string, filter MetaFilter) (ids map[string]bool, active bool) {
	f := mergeFilters(filterMeta, filter)
	if len(f) == 0 {
		return nil, false
	}
	ids = map[string]bool{}
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		ids = metaFilterIDs(tx, collection, f)
		return nil
	})
	return ids, true
}

// sortedIDs returns the set's members in a stable order, so a filtered walk
// loads documents in the same order on every call.
func sortedIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// metaKeys lists every metadata key of a collection with its distinct
// values, in index order — the one implementation behind REST /v1/meta-keys,
// MCP get_meta_keys and gRPC GetMetaKeys, which were three copies. A value
// may contain '|': it is split off the document ID with splitMetaEntry, not
// cut at its first '|' as the copies did.
func (s *Server) metaKeys(collection string) map[string][]string {
	meta := map[string][]string{}
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx == nil {
			return nil
		}
		prefix := []byte("meta|" + collection + "|")
		seen := map[string]map[string]bool{}
		cur := bIdx.Cursor()
		for k, _ := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cur.Next() {
			rest := k[len(prefix):]
			sep := bytes.IndexByte(rest, '|')
			if sep < 0 {
				continue
			}
			mk := string(rest[:sep])
			mv, _, ok := splitMetaEntry(rest[sep+1:], collection)
			if !ok {
				continue
			}
			if seen[mk] == nil {
				seen[mk] = map[string]bool{}
			}
			if !seen[mk][mv] {
				seen[mk][mv] = true
				meta[mk] = append(meta[mk], mv)
			}
		}
		return nil
	})
	return meta
}
