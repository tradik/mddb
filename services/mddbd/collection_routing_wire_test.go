package main

import (
	"testing"

	"mddb/internal/fts"
	"mddb/internal/geo"
	"mddb/internal/temporal"
	"mddb/internal/ttl"
	"mddb/internal/vector"
)

// #359: after a restore, collectionStore wires the routers again as it
// reopens the collection files, while the vector index reload and request
// handlers already read them. Every component wired here is read while the
// routers are set; under -race an unsynchronised router fails this test.
func TestWireRoutersWhileComponentsRead(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	s.FTSIndex = fts.NewFTSIndex(s.DB)
	s.VectorStore = vector.NewVectorStore(s.DB)
	s.GeoStore = geo.NewGeoStore(s.DB)
	s.TTLManager = ttl.NewTTLManager(s.DB, nil)
	s.TemporalManager = temporal.NewTemporalManager(s.DB)
	for _, ensure := range []func() error{s.FTSIndex.EnsureBuckets, s.TTLManager.EnsureBuckets, s.TemporalManager.EnsureBuckets} {
		if err := ensure(); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			s.wireRouters()
		}
	}()
	for range 200 {
		_, _ = s.FTSIndex.Search("c", "routers", 1)
		_, _ = s.VectorStore.CountByCollection()
		_, _, _, _ = s.GeoStore.Get("c", "d")
		_ = s.TTLManager.Remove("c", "d")
		_, _ = s.TemporalManager.QueryRange("c", "d", 0, 1, "", 1)
	}
	<-done
}

// Components built before the collection files open are pointed at them
// when they do: a geo point lands in its collection's file, where the
// bucket is, not in the main file, where it is not.
func TestOpeningCollectionFilesRoutesComponentsBuiltEarlier(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	s, cleanup := newTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()

	s.GeoStore = geo.NewGeoStore(s.DB)
	s.TemporalManager = temporal.NewTemporalManager(s.DB)
	if s.collectionStore() == nil {
		t.Fatal("the collection files did not open")
	}
	if _, _, err := s.addDocument("places", "here", "en", nil, "x", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := s.GeoStore.Put("places", "places|here|en", 52.2, 21.0); err != nil {
		t.Fatalf("geo point not routed to the collection file: %v", err)
	}
	if lat, _, ok, err := s.GeoStore.Get("places", "places|here|en"); err != nil || !ok || lat != 52.2 {
		t.Fatalf("geo point read back as %v %v %v", lat, ok, err)
	}
}
