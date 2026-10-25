package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// #335: the breakdown attributes every key to its bucket and collection.

func TestStorageBreakdown(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "big", "a", "en", "a long document about storage accounting and where the bytes go", map[string][]string{"tag": {"x"}})
	addTestDoc(t, s, "big", "b", "en", "another one", nil)
	addTestDoc(t, s, "small", "a", "en", "tiny", nil)
	// A bucket whose tags sort around each other, and a key with no tag.
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("zz_test"))
		_ = b.Put([]byte("tagA|big|1"), []byte("12345"))
		_ = b.Put([]byte("tag|big|1"), []byte("123"))
		_ = b.Put([]byte("tag|small|1"), []byte("1"))
		_ = b.Put([]byte("untagged"), []byte("1"))
		return nil
	})

	rec := httptest.NewRecorder()
	s.handleStorage(rec, httptest.NewRequest(http.MethodGet, "/v1/storage", nil))
	var full storageBreakdown
	_ = json.Unmarshal(rec.Body.Bytes(), &full)
	if rec.Code != http.StatusOK || full.FileBytes == 0 || full.PageSize == 0 || full.Buckets["docs"].Keys != 3 {
		t.Fatalf("full: %d %s", rec.Code, rec.Body.String())
	}
	big := full.Collections["big"]
	if big == nil || big.ByCategory["content"] == 0 || big.ByCategory["metadata"] == 0 || big.ByCategory["fullText"] == 0 {
		t.Fatalf("big: %+v", big)
	}
	if big.TotalBytes <= full.Collections["small"].TotalBytes {
		t.Error("big is not bigger than small")
	}

	one, err := s.storageUsage("big")
	if err != nil || one.Buckets != nil || len(one.Collections) != 1 {
		t.Fatalf("one collection: %+v %v", one, err)
	}
	// The per-collection scan must find exactly what the full pass found.
	for bucket, n := range big.ByBucket {
		if one.Collections["big"].ByBucket[bucket] != n {
			t.Errorf("%s: full pass %d, collection scan %d", bucket, n, one.Collections["big"].ByBucket[bucket])
		}
	}
	if got := one.Collections["big"].ByBucket["zz_test"]; got != int64(len("tagA|big|1")+5+len("tag|big|1")+3) {
		t.Errorf("tag jumping lost keys: %d", got)
	}

	for in, want := range map[string]string{"docs": "content", "bykey": "metadata", "ftsp": "fullText", "vectors": "vectors", "automation": "other"} {
		if storageCategory(in) != want {
			t.Errorf("%s -> %s", in, storageCategory(in))
		}
	}
	if _, ok := collectionOf([]byte("nocollection")); ok {
		t.Error("untagged key")
	}
	if _, ok := collectionOf([]byte("tag||x")); ok {
		t.Error("empty collection")
	}
}
