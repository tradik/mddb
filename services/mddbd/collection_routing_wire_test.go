package main

import (
	"testing"

	"mddb/internal/geo"
	"mddb/internal/temporal"
)

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
