package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/geo"
	"mddb/internal/storage"
	"mddb/internal/temporal"

	bolt "go.etcd.io/bbolt"
)

// POST /v1/add-batch request handling, and the post-commit hooks every batch
// path shares.

func TestHandleAddBatchRequestErrors(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	cases := []struct {
		name, method, body string
		want               int
		wantBody           string
	}{
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed, "method not allowed"},
		{"bad JSON", http.MethodPost, "{", http.StatusBadRequest, "error"},
		{"no collection", http.MethodPost, `{"documents":[{"key":"a","lang":"en"}]}`, http.StatusBadRequest, "missing collection"},
		{"no documents", http.MethodPost, `{"collection":"c"}`, http.StatusOK, `"added":0`},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		s.handleAddBatch(rec, httptest.NewRequest(tc.method, "/v1/add-batch", bytes.NewReader([]byte(tc.body))))
		if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestFirePostBatchHooksTTLGeoTemporalTriggers(t *testing.T) {
	t.Setenv("MDDB_TRIGGERS", "true")
	_, s := quotaTestServer(t) // TTL, FTS, webhooks, automation
	s.GeoStore = geo.NewGeoStore(s.DB)
	if err := s.GeoStore.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	s.GeoIndex, s.GeoHashIndex = geo.NewGeoIndex(), geo.NewGeoHashIndex()
	s.TemporalManager = temporal.NewTemporalManager(s.DB)

	expires := time.Now().Add(time.Hour).Unix()
	placed := &ProcessedDoc{DocID: "p1", Doc: storage.Doc{ID: "p1", Key: "placed", Lang: "en", ContentMD: "here",
		ExpiresAt: expires, Meta: map[string][]string{"geo_lat": {"52.5"}, "geo_lng": {"13.4"}}}}
	moved := &ProcessedDoc{DocID: "p2", IsUpdate: true, Doc: storage.Doc{ID: "p2", Key: "moved", Lang: "en", ContentMD: "gone"}}
	failed := &ProcessedDoc{DocID: "p3", Error: errors.New("not written")}
	if err := s.GeoStore.Put("c", "p2", 1, 1); err != nil {
		t.Fatal(err)
	}
	s.GeoIndex.Add("c", "p2", 1, 1)

	s.firePostBatchHooks("c", []*ProcessedDoc{placed, moved, failed}, postBatchOptions{})

	if _, _, ok, _ := s.GeoStore.Get("c", "p1"); !ok {
		t.Error("a new document with coordinates was not placed")
	}
	if _, _, ok, _ := s.GeoStore.Get("c", "p2"); ok {
		t.Error("an update without coordinates kept its old point")
	}
	ttlSet := false
	_ = s.DBViewC("c", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("ttlrev")).ForEach(func(k, _ []byte) error {
			ttlSet = ttlSet || strings.Contains(string(k), "p1")
			return nil
		})
	})
	if !ttlSet {
		t.Error("the expiring document was not registered with TTL")
	}
}
