package geo

import (
	"path/filepath"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// #359: a router set on the store is the one it reads and writes through.
func TestGeoStoreSetRouterRoutesToTheRouter(t *testing.T) {
	own := openGeoTestDB(t)
	other := openGeoTestDB(t)
	gs := NewGeoStore(own)
	gs.SetRouter(boltx.Single{DB: other})
	if err := boltx.EnsureBuckets(other, geoBucketName); err != nil {
		t.Fatal(err)
	}
	if err := gs.Put("c", "d", 1, 2); err != nil {
		t.Fatalf("put through the router: %v", err)
	}
	if lat, lng, ok, err := gs.Get("c", "d"); err != nil || !ok || lat != 1 || lng != 2 {
		t.Fatalf("get through the router = %v %v %v %v", lat, lng, ok, err)
	}
}

// Without the bucket, Put fails and reads, deletes and rebuilds find nothing.
func TestGeoStoreWithoutTheBucket(t *testing.T) {
	gs := NewGeoStore(openGeoTestDB(t))
	if err := gs.Put("c", "d", 1, 2); err == nil {
		t.Fatal("put without the bucket succeeded")
	}
	if _, _, ok, err := gs.Get("c", "d"); ok || err != nil {
		t.Fatalf("get without the bucket = %v %v", ok, err)
	}
	if err := gs.Delete("c", "d"); err != nil {
		t.Fatalf("delete without the bucket: %v", err)
	}
	if err := gs.DeleteCollection("c"); err != nil {
		t.Fatalf("delete collection without the bucket: %v", err)
	}
	if n, err := gs.Rebuild(NewGeoIndex(), ""); n != 0 || err != nil {
		t.Fatalf("rebuild without the bucket = %d %v", n, err)
	}
	if n, err := gs.RebuildHash(NewGeoHashIndex(), ""); n != 0 || err != nil {
		t.Fatalf("rebuild hash without the bucket = %d %v", n, err)
	}
}

// Every write reaches the replication log: puts, deletes and each point of
// a dropped collection.
func TestGeoStoreWritesReachTheBinlog(t *testing.T) {
	db := openGeoTestDB(t)
	bl, err := binlog.NewBinlog("", binlog.BinlogConfig{Path: filepath.Join(t.TempDir(), "geo.binlog")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	gs := NewGeoStore(db)
	gs.SetBinlog(bl)
	if err := gs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return gs.Put("c", "a", 1, 1) },
		func() error { return gs.Put("c", "b", 2, 2) },
		func() error { return gs.Delete("c", "a") },
		func() error { return gs.DeleteCollection("c") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	if got := bl.CurrentLSN(); got != 4 {
		t.Fatalf("binlog LSN = %d, want 4 (two puts, a delete, a dropped point)", got)
	}
}

// Rebuilds skip keys and values they cannot read and stop at the end of the
// collection they rebuild; a closed file is reported.
func TestGeoStoreRebuildSkipsDamagedEntries(t *testing.T) {
	db := openGeoTestDB(t)
	gs := NewGeoStore(db)
	if err := gs.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := gs.Put("a", "good", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := gs.Put("b", "next", 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(geoBucketName)
		if err := b.Put([]byte("geo|nosep"), encodeGeoValue(0, 0)); err != nil {
			return err
		}
		return b.Put(buildGeoKey("a", "short"), []byte{1, 2, 3})
	}); err != nil {
		t.Fatal(err)
	}

	for name, rebuild := range map[string]func(string) (int, error){
		"rtree":   func(c string) (int, error) { return gs.Rebuild(NewGeoIndex(), c) },
		"geohash": func(c string) (int, error) { return gs.RebuildHash(NewGeoHashIndex(), c) },
	} {
		if n, err := rebuild("a"); n != 1 || err != nil {
			t.Fatalf("%s: rebuild of a = %d %v, want 1", name, n, err)
		}
		if n, err := rebuild(""); n != 2 || err != nil {
			t.Fatalf("%s: rebuild of all = %d %v, want 2", name, n, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Rebuild(NewGeoIndex(), ""); err == nil {
		t.Fatal("rebuild on a closed file succeeded")
	}
}
