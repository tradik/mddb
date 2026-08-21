package main

import (
	"bytes"
	"errors"
	"mddb/internal/storage"
	"net/http"
	"sort"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
)

// --- Request / Response types ---

// AggregateRequest is the request body for POST /v1/aggregate.
type AggregateRequest struct {
	Collection   string              `json:"collection"`
	FilterMeta   map[string][]string `json:"filterMeta,omitempty"`   // optional pre-filter (same as search)
	Facets       []FacetRequest      `json:"facets,omitempty"`       // metadata facet aggregations
	Histograms   []HistogramRequest  `json:"histograms,omitempty"`   // date/numeric histograms
	MaxFacetSize int                 `json:"maxFacetSize,omitempty"` // max values per facet (default 50)
}

// FacetRequest asks for value counts on a metadata key.
type FacetRequest struct {
	Field   string `json:"field"`             // metadata key (e.g. "category", "author")
	OrderBy string `json:"orderBy,omitempty"` // "count" (default) or "value"
}

// HistogramRequest asks for a date or numeric histogram.
type HistogramRequest struct {
	Field    string `json:"field"`              // "addedAt" or "updatedAt"
	Interval string `json:"interval,omitempty"` // "day", "week", "month" (default), "year"
}

// AggregateResponse is the response for POST /v1/aggregate.
type AggregateResponse struct {
	Collection string                       `json:"collection"`
	TotalDocs  int                          `json:"totalDocs"`
	Facets     map[string][]FacetBucket     `json:"facets,omitempty"`
	Histograms map[string][]HistogramBucket `json:"histograms,omitempty"`
	DurationMs int64                        `json:"durationMs"`
}

// FacetBucket is a single value + count pair in a facet result.
type FacetBucket struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// HistogramBucket is a single time range + count pair.
type HistogramBucket struct {
	Key   string `json:"key"`  // formatted date label (e.g. "2026-03")
	From  int64  `json:"from"` // unix timestamp start
	To    int64  `json:"to"`   // unix timestamp end (exclusive)
	Count int    `json:"count"`
}

// --- Handler ---

func (s *Server) handleAggregate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req AggregateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}

	// Auth
	if s.AuthManager != nil && req.Collection != "" {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("aggregate", req.Collection)
	}

	resp, err := s.aggregate(&req)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, resp)
}

// resolveMetaFilter returns the set of document IDs matching the metadata filter.
func (s *Server) resolveMetaFilter(tx *bolt.Tx, collection string, filterMeta map[string][]string) map[string]bool {
	bIdx := tx.Bucket([]byte("idxmeta"))
	if bIdx == nil {
		return nil
	}

	var sets [][]string
	for mk, mvals := range filterMeta {
		var ids []string
		seen := make(map[string]bool)
		for _, mv := range mvals {
			prefix := storage.MetaKeyPrefix(collection, mk, mv)
			c := bIdx.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				id := string(k[len(prefix):])
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		sets = append(sets, ids)
	}

	if len(sets) == 0 {
		return nil
	}

	// Intersect
	result := make(map[string]bool)
	for _, id := range sets[0] {
		result[id] = true
	}
	for _, set := range sets[1:] {
		next := make(map[string]bool)
		for _, id := range set {
			if result[id] {
				next[id] = true
			}
		}
		result = next
	}
	return result
}

// computeFacet scans the idxmeta bucket and counts distinct values for a metadata key.
func (s *Server) computeFacet(bIdx *bolt.Bucket, collection, field string, allowedIDs map[string]bool, maxSize int, orderBy string) []FacetBucket {
	prefix := []byte("meta|" + collection + "|" + field + "|")
	counts := make(map[string]int)

	c := bIdx.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		rest := string(k[len(prefix):])
		parts := strings.SplitN(rest, "|", 2)
		if len(parts) < 2 {
			continue
		}
		value, docID := parts[0], parts[1]
		if allowedIDs != nil && !allowedIDs[docID] {
			continue
		}
		counts[value]++
	}

	buckets := make([]FacetBucket, 0, len(counts))
	for v, c := range counts {
		buckets = append(buckets, FacetBucket{Value: v, Count: c})
	}

	// Sort
	if orderBy == "value" {
		sort.Slice(buckets, func(i, j int) bool { return buckets[i].Value < buckets[j].Value })
	} else {
		sort.Slice(buckets, func(i, j int) bool { return buckets[i].Count > buckets[j].Count })
	}

	if len(buckets) > maxSize {
		buckets = buckets[:maxSize]
	}
	return buckets
}

// computeHistogram groups documents by time interval on addedAt or updatedAt.
func (s *Server) computeHistogram(bDocs *bolt.Bucket, collection, field, interval string, allowedIDs map[string]bool) []HistogramBucket {
	if interval == "" {
		interval = "month"
	}

	type timeDoc struct {
		ts int64
	}

	var docs []timeDoc
	prefix := []byte("doc|" + collection + "|")
	c := bDocs.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		// Index the map with the conversion inline: the compiler elides the
		// string allocation for a map lookup, and docID is not needed elsewhere.
		if allowedIDs != nil && !allowedIDs[string(k[len(prefix):])] {
			continue
		}
		d, err := loadDoc(v)
		if err != nil || d == nil {
			continue
		}
		var ts int64
		switch field {
		case "updatedAt":
			ts = d.UpdatedAt
		default: // addedAt
			ts = d.AddedAt
		}
		if ts > 0 {
			docs = append(docs, timeDoc{ts: ts})
		}
	}

	if len(docs) == 0 {
		return nil
	}

	// Find min/max timestamps
	minTS, maxTS := docs[0].ts, docs[0].ts
	for _, d := range docs[1:] {
		if d.ts < minTS {
			minTS = d.ts
		}
		if d.ts > maxTS {
			maxTS = d.ts
		}
	}

	// Generate time buckets
	bucketRanges := generateTimeBuckets(minTS, maxTS, interval)

	// Count docs per bucket
	counts := make([]int, len(bucketRanges))
	for _, d := range docs {
		idx := findBucket(bucketRanges, d.ts)
		if idx >= 0 {
			counts[idx]++
		}
	}

	result := make([]HistogramBucket, 0, len(bucketRanges))
	for i, br := range bucketRanges {
		if counts[i] > 0 {
			result = append(result, HistogramBucket{
				Key:   br.label,
				From:  br.from,
				To:    br.to,
				Count: counts[i],
			})
		}
	}
	return result
}

type timeBucketRange struct {
	label string
	from  int64
	to    int64
}

func generateTimeBuckets(minTS, maxTS int64, interval string) []timeBucketRange {
	var buckets []timeBucketRange
	t := time.Unix(minTS, 0).UTC()

	// Align to interval start
	switch interval {
	case "day":
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case "week":
		// Align to Monday
		for t.Weekday() != time.Monday {
			t = t.AddDate(0, 0, -1)
		}
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case "year":
		t = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	default: // month
		t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}

	maxTime := time.Unix(maxTS, 0).UTC()
	maxBuckets := 1000 // safety limit

	for i := 0; t.Unix() <= maxTS && i < maxBuckets; i++ {
		var next time.Time
		var label string
		switch interval {
		case "day":
			label = t.Format("2006-01-02")
			next = t.AddDate(0, 0, 1)
		case "week":
			label = t.Format("2006-W") + weekNumber(t)
			next = t.AddDate(0, 0, 7)
		case "year":
			label = t.Format("2006")
			next = t.AddDate(1, 0, 0)
		default: // month
			label = t.Format("2006-01")
			next = t.AddDate(0, 1, 0)
		}
		buckets = append(buckets, timeBucketRange{
			label: label,
			from:  t.Unix(),
			to:    next.Unix(),
		})
		t = next
		_ = maxTime // used in loop condition via maxTS
	}
	return buckets
}

func weekNumber(t time.Time) string {
	_, w := t.ISOWeek()
	if w < 10 {
		return "0" + itoa(w)
	}
	return itoa(w)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 4)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func findBucket(buckets []timeBucketRange, ts int64) int {
	// Binary search for the bucket containing ts
	lo, hi := 0, len(buckets)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if ts < buckets[mid].from {
			hi = mid - 1
		} else if ts >= buckets[mid].to {
			lo = mid + 1
		} else {
			return mid
		}
	}
	return -1
}

// aggregate is the internal method called by DirectClient and the HTTP handler.
func (s *Server) aggregate(req *AggregateRequest) (*AggregateResponse, error) {
	if req.Collection == "" {
		return nil, errMissingCollection
	}

	start := time.Now()
	maxFacets := req.MaxFacetSize
	if maxFacets <= 0 {
		maxFacets = 50
	}

	resp := &AggregateResponse{
		Collection: req.Collection,
		Facets:     make(map[string][]FacetBucket),
		Histograms: make(map[string][]HistogramBucket),
	}

	var allowedIDs map[string]bool

	_ = s.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		bDocs := tx.Bucket([]byte("docs"))
		if bIdx == nil || bDocs == nil {
			return nil
		}

		if len(req.FilterMeta) > 0 {
			allowedIDs = s.resolveMetaFilter(tx, req.Collection, req.FilterMeta)
		}

		docPrefix := []byte("doc|" + req.Collection + "|")
		c := bDocs.Cursor()
		for k, _ := c.Seek(docPrefix); k != nil && bytes.HasPrefix(k, docPrefix); k, _ = c.Next() {
			if allowedIDs == nil || allowedIDs[string(k[len(docPrefix):])] {
				resp.TotalDocs++
			}
		}

		for _, facetReq := range req.Facets {
			buckets := s.computeFacet(bIdx, req.Collection, facetReq.Field, allowedIDs, maxFacets, facetReq.OrderBy)
			resp.Facets[facetReq.Field] = buckets
		}

		for _, histReq := range req.Histograms {
			buckets := s.computeHistogram(bDocs, req.Collection, histReq.Field, histReq.Interval, allowedIDs)
			resp.Histograms[histReq.Field] = buckets
		}

		return nil
	})

	resp.DurationMs = time.Since(start).Milliseconds()
	return resp, nil
}

// errMissingCollection is a shared sentinel for missing collection parameter.
var errMissingCollection = errors.New("missing collection")
