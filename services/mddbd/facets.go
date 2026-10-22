package main

import (
	"bytes"
	"strings"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// Facet scopes (#311). "returned" counts over the page a search hands back;
// "all" counts over every document that matched, before pagination.
const (
	facetScopeReturned = "returned"
	facetScopeAll      = "all"
)

// FacetResult holds per-key bucket lists keyed by metadata field name.
// Encoded as a JSON object so clients can index it like `facets.category[0]`.
// FacetBucket is reused from aggregation.go to keep a single wire shape across
// /v1/aggregate and the new inline /v1/fts + /v1/hybrid-search facets.
type FacetResult map[string][]FacetBucket

// computeFacets walks the resolved documents once and tallies counts for each
// requested metadata key. Unknown/missing keys produce an empty bucket list
// (not omitted) so UIs can render a stable set of facet groups across queries.
// maxPerKey clamps the per-key bucket list; 0 disables the cap.
func computeFacets(docs []storage.Doc, facetBy []string, maxPerKey int) FacetResult {
	if len(facetBy) == 0 || len(docs) == 0 {
		return nil
	}
	// key → value → count
	raw := make(map[string]map[string]int, len(facetBy))
	for _, k := range facetBy {
		if k == "" {
			continue
		}
		raw[k] = make(map[string]int)
	}
	for _, d := range docs {
		if d.Meta == nil {
			continue
		}
		for k, counts := range raw {
			vals, ok := d.Meta[k]
			if !ok {
				continue
			}
			for _, v := range vals {
				counts[v]++
			}
		}
	}

	return facetResultFrom(raw, maxPerKey)
}

// computeFacetsOverMatches counts facet values over a whole match set by
// walking the metadata index, not the documents: a match set can be the whole
// collection, and decoding every document to read its meta is the expensive
// way to learn what the index already holds.
//
// Index keys are "meta|<coll>|<key>|<value>|<docID>", and a document ID itself
// contains '|', so the boundary between value and ID cannot be found by
// splitting. It is found by trying each '|' until the rest is a matched ID.
func (s *Server) computeFacetsOverMatches(collection string, matches map[string]bool, facetBy []string, maxPerKey int) FacetResult {
	if len(facetBy) == 0 {
		return nil
	}
	raw := make(map[string]map[string]int, len(facetBy))
	_ = s.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		for _, k := range facetBy {
			if k == "" {
				continue
			}
			counts := make(map[string]int)
			raw[k] = counts
			if bIdx == nil || len(matches) == 0 {
				continue
			}
			prefix := []byte("meta|" + collection + "|" + k + "|")
			c := bIdx.Cursor()
			for key, _ := c.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, _ = c.Next() {
				rest := string(key[len(prefix):])
				for i := strings.IndexByte(rest, '|'); i >= 0; {
					if matches[rest[i+1:]] {
						counts[rest[:i]]++
						break
					}
					next := strings.IndexByte(rest[i+1:], '|')
					if next < 0 {
						break
					}
					i += next + 1
				}
			}
		}
		return nil
	})
	return facetResultFrom(raw, maxPerKey)
}

// facetResultFrom turns key → value → count tallies into sorted, capped
// bucket lists.
func facetResultFrom(raw map[string]map[string]int, maxPerKey int) FacetResult {
	out := make(FacetResult, len(raw))
	for k, counts := range raw {
		buckets := make([]FacetBucket, 0, len(counts))
		for v, c := range counts {
			buckets = append(buckets, FacetBucket{Value: v, Count: c})
		}
		sortFacetBuckets(buckets)
		if maxPerKey > 0 && len(buckets) > maxPerKey {
			buckets = buckets[:maxPerKey]
		}
		out[k] = buckets
	}
	return out
}

// sortFacetBuckets orders buckets by count desc, then value asc — stable so
// ties in count don't shuffle between calls.
func sortFacetBuckets(buckets []FacetBucket) {
	// Simple insertion sort — facet cardinality is typically < 100; avoids pulling in sort.Slice for hot paths.
	for i := 1; i < len(buckets); i++ {
		cur := buckets[i]
		j := i - 1
		for j >= 0 && facetLess(cur, buckets[j]) {
			buckets[j+1] = buckets[j]
			j--
		}
		buckets[j+1] = cur
	}
}

func facetLess(a, b FacetBucket) bool {
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.Value < b.Value
}
