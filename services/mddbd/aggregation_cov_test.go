package main

import (
	"net/http"
	"testing"
	"time"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covGDate is a UTC unix time for a calendar day.
func covGDate(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Unix()
}

// covGAggDocs stores three dated documents with their meta-index entries, a
// corrupt document and a malformed index key.
func covGAggDocs(t *testing.T, s *Server) {
	t.Helper()
	for _, d := range []storage.Doc{
		{ID: "1", Key: "mid", Lang: "en", AddedAt: covGDate(2024, 1, 10), UpdatedAt: covGDate(2024, 2, 1), Meta: map[string][]string{"tag": {"go"}}},
		{ID: "2", Key: "old", Lang: "en", AddedAt: covGDate(2023, 6, 1), UpdatedAt: covGDate(2023, 6, 1), Meta: map[string][]string{"tag": {"go"}}},
		{ID: "3", Key: "new", Lang: "en", AddedAt: covGDate(2025, 3, 5), UpdatedAt: covGDate(2025, 3, 6), Meta: map[string][]string{"tag": {"rust"}}},
	} {
		covGPutDoc(t, s, "agg", &d)
		for k, vals := range d.Meta {
			for _, v := range vals {
				covGPutIdx(t, s, "agg", append(storage.MetaKeyPrefix("agg", k, v), []byte(d.ID)...))
			}
		}
	}
	covGPutRaw(t, s, "agg", "4", []byte{0xff, 0xfe})
	covGPutIdx(t, s, "agg", []byte("meta|agg|tag|novalue"))
}

func covGPutIdx(t *testing.T, s *Server, collection string, key []byte) {
	t.Helper()
	if err := s.DBUpdateC(collection, func(tx *bolt.Tx) error { return tx.Bucket([]byte("idxmeta")).Put(key, []byte("1")) }); err != nil {
		t.Fatal(err)
	}
}

func covGAggregate(t *testing.T, s *Server, body any) AggregateResponse {
	t.Helper()
	rec := doRequest(t, s.handleAggregate, body)
	covGWantCode(t, rec, http.StatusOK)
	var resp AggregateResponse
	covGDecode(t, rec, &resp)
	return resp
}

func TestCovGAggregateForbidden(t *testing.T) {
	s := covGServer(t)
	covGEnableAuth(t, s)
	covGWantCode(t, doRequest(t, s.handleAggregate, map[string]any{"collection": "agg"}), http.StatusForbidden)
}

func TestCovGAggregateHistogramsAndFacets(t *testing.T) {
	s := covGServer(t)
	covGAggDocs(t, s)

	resp := covGAggregate(t, s, map[string]any{
		"collection": "agg",
		"facets":     []map[string]any{{"field": "tag"}},
		"histograms": []map[string]any{{"field": "addedAt", "interval": "week"}, {"field": "updatedAt"}},
	})
	if resp.TotalDocs != 4 {
		t.Errorf("totalDocs = %d, want 4 (the corrupt one is still a document)", resp.TotalDocs)
	}
	if got := resp.Facets["tag"]; len(got) != 2 || got[0].Value != "go" || got[0].Count != 2 {
		t.Errorf("facets = %+v (the malformed key must be skipped)", got)
	}
	weeks := resp.Histograms["addedAt"]
	if len(weeks) != 3 {
		t.Fatalf("one bucket per populated week, got %+v", weeks)
	}
	for _, b := range weeks {
		if time.Unix(b.From, 0).UTC().Weekday() != time.Monday {
			t.Errorf("week bucket %s does not start on a Monday", b.Key)
		}
	}
	months := resp.Histograms["updatedAt"]
	if len(months) != 3 || months[0].Key != "2023-06" || months[2].Key != "2025-03" {
		t.Errorf("updatedAt months = %+v", months)
	}

	// A filter narrows both the count and the histogram.
	resp = covGAggregate(t, s, map[string]any{
		"collection": "agg", "filterMeta": map[string][]string{"tag": {"go"}},
		"histograms": []map[string]any{{"field": "addedAt", "interval": "year"}},
	})
	if resp.TotalDocs != 2 || len(resp.Histograms["addedAt"]) != 2 {
		t.Errorf("filtered: total %d histogram %+v", resp.TotalDocs, resp.Histograms["addedAt"])
	}

	// An empty collection has no histogram at all.
	resp = covGAggregate(t, s, map[string]any{"collection": "none",
		"histograms": []map[string]any{{"field": "addedAt"}}})
	if resp.Histograms["addedAt"] != nil {
		t.Errorf("empty collection histogram = %+v", resp.Histograms["addedAt"])
	}
}

func TestCovGAggregateWithoutIndexBucket(t *testing.T) {
	s := covGServer(t)
	covGAggDocs(t, s)
	if err := s.DBUpdateC("agg", func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("idxmeta")) }); err != nil {
		t.Fatal(err)
	}
	resp := covGAggregate(t, s, map[string]any{"collection": "agg", "facets": []map[string]any{{"field": "tag"}}})
	if resp.TotalDocs != 0 || len(resp.Facets) != 0 {
		t.Errorf("no index bucket: %+v", resp)
	}
}

func TestCovGFindBucketOutsideRange(t *testing.T) {
	buckets := generateTimeBuckets(covGDate(2024, 1, 1), covGDate(2024, 3, 1), "month")
	if findBucket(buckets, covGDate(2020, 1, 1)) != -1 || findBucket(buckets, covGDate(2030, 1, 1)) != -1 {
		t.Error("a time outside every bucket must report -1")
	}
	if findBucket(buckets, covGDate(2024, 2, 15)) != 1 {
		t.Error("mid-February belongs to the second month bucket")
	}
}
