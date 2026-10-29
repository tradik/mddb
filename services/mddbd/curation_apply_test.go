package main

import (
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

func TestApplyCurationLeavesResultsAloneWithoutARule(t *testing.T) {
	in := []FTSResultWithDoc{{Document: storage.Doc{Key: "a"}}}
	hin := []HybridSearchResultItem{{Document: storage.Doc{Key: "a"}}}

	none := &Server{}
	if got := none.applyCurationFTS("c", "q", in); len(got) != 1 {
		t.Fatalf("no curation manager: %v", got)
	}
	if got := none.applyCurationHybrid("c", "q", hin); len(got) != 1 {
		t.Fatalf("no curation manager: %v", got)
	}

	s, cleanup := newCurationTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()
	// No rule for the query, then a rule that neither pins nor hides.
	for _, q := range []string{"unmatched", "empty"} {
		if q == "empty" {
			if err := s.CurationManager.Set(&CurationRule{Collection: "c", Query: "empty", Enabled: true}); err != nil {
				t.Fatal(err)
			}
		}
		if got := s.applyCurationFTS("c", q, in); len(got) != 1 || got[0].Pinned {
			t.Fatalf("%s: FTS results changed: %v", q, got)
		}
		if got := s.applyCurationHybrid("c", q, hin); len(got) != 1 || got[0].Pinned {
			t.Fatalf("%s: hybrid results changed: %v", q, got)
		}
	}
}

// curatedServer holds documents a rule pins and hides, including the ways a
// pin can fail to resolve.
func curatedServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newCurationTestServer(t)
	t.Cleanup(func() {
		s.closeCollectionFiles()
		cleanup()
	})
	for _, key := range []string{"Pinned.MD", "organic", "hidden", "tail", "broken", "expired"} {
		if _, _, err := s.addDocument("c", key, "en", nil, "content of "+key, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	// "broken" no longer decodes, "expired" has expired, and "dangling" has
	// an index entry whose document is gone.
	expired, err := marshalDoc(&storage.Doc{ID: genID("c", "expired", "en"), Key: "expired", Lang: "en", ExpiresAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DBUpdateC("c", func(tx *bolt.Tx) error {
		docs, index := tx.Bucket([]byte("docs")), tx.Bucket([]byte("bykey"))
		if err := docs.Put(storage.DocKey("c", genID("c", "broken", "en")), []byte("not a document")); err != nil {
			return err
		}
		if err := docs.Put(storage.DocKey("c", genID("c", "expired", "en")), expired); err != nil {
			return err
		}
		return index.Put(storage.ByKeyKey("c", "dangling", "en"), []byte(genID("c", "dangling", "en")))
	}); err != nil {
		t.Fatal(err)
	}
	rule := &CurationRule{Collection: "c", Query: "q", Enabled: true, Hides: []string{"hidden"}, Pins: []PinnedDoc{
		// Found under another spelling, with no language: the first entry
		// under the key's prefix (DOC-017).
		{Key: "PINNED.md", Position: 1},
		{Key: "tail", Lang: "EN", Position: 0},
		{Key: "absent", Position: 2},
		{Key: "absent", Lang: "en", Position: 2},
		{Key: "dangling", Lang: "en", Position: 3},
		{Key: "broken", Lang: "en", Position: 3},
		{Key: "expired", Lang: "en", Position: 3},
	}}
	if err := s.CurationManager.Set(rule); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyCurationFTSPinsHidesAndSkipsWhatCannotResolve(t *testing.T) {
	s := curatedServer(t)
	in := []FTSResultWithDoc{
		{Document: storage.Doc{Key: "hidden"}},
		{Document: storage.Doc{Key: "tail"}}, // pinned elsewhere: dropped here
		{Document: storage.Doc{Key: "organic"}},
	}
	got := s.applyCurationFTS("c", "q", in)
	keys := make([]string, len(got))
	for i, r := range got {
		keys[i] = r.Document.Key
	}
	want := []string{"Pinned.MD", "organic", "tail"}
	if len(keys) != len(want) {
		t.Fatalf("results %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("results %v, want %v", keys, want)
		}
	}
	if !got[0].Pinned || got[1].Pinned || !got[2].Pinned {
		t.Fatalf("pinned flags wrong: %+v", got)
	}
}

func TestApplyCurationHybridPinsHidesAndRenumbers(t *testing.T) {
	s := curatedServer(t)
	in := []HybridSearchResultItem{
		{Document: storage.Doc{Key: "organic"}},
		{Document: storage.Doc{Key: "hidden"}},
		{Document: storage.Doc{Key: "Pinned.MD"}}, // pinned by another spelling: kept organic too
	}
	got := s.applyCurationHybrid("c", "q", in)
	want := []string{"Pinned.MD", "organic", "Pinned.MD", "tail"}
	if len(got) != len(want) {
		t.Fatalf("got %d results %+v, want %v", len(got), got, want)
	}
	for i, r := range got {
		if r.Document.Key != want[i] || r.Rank != i+1 {
			t.Fatalf("result %d = %q rank %d, want %q rank %d", i, r.Document.Key, r.Rank, want[i], i+1)
		}
	}
}

func TestPinOrdering(t *testing.T) {
	cases := []struct {
		a, b pinResolved
		less bool
	}{
		{pinResolved{Key: "z", Position: 1}, pinResolved{Key: "a", Position: 0}, true},  // placed before appended
		{pinResolved{Key: "a", Position: 0}, pinResolved{Key: "z", Position: 1}, false}, // appended after placed
		{pinResolved{Key: "a", Position: 0}, pinResolved{Key: "b", Position: -1}, true}, // appended: by key
		{pinResolved{Key: "z", Position: 1}, pinResolved{Key: "a", Position: 2}, true},  // by position
		{pinResolved{Key: "a", Position: 2}, pinResolved{Key: "b", Position: 2}, true},  // same position: by key
	}
	for _, c := range cases {
		if got := pinLess(c.a, c.b); got != c.less {
			t.Errorf("pinLess(%+v, %+v) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
	if hides, pins := collectPinsAndHides([]*CurationRule{{Hides: []string{"x"}}}); !hides["x"] || pins != nil {
		t.Fatalf("hides only: %v %v", hides, pins)
	}
}

func TestSplicePinnedHybridPlacesAppendsAndClamps(t *testing.T) {
	base := []HybridSearchResultItem{{Document: storage.Doc{Key: "a"}}, {Document: storage.Doc{Key: "b"}}}
	if got := splicePinnedHybrid(base, nil); len(got) != 2 {
		t.Fatalf("no pins: %v", got)
	}
	got := splicePinnedHybrid(base, []pinnedHybrid{
		{Result: HybridSearchResultItem{Document: storage.Doc{Key: "p2"}}, Position: 2},
		{Result: HybridSearchResultItem{Document: storage.Doc{Key: "p9"}}, Position: 9},
		{Result: HybridSearchResultItem{Document: storage.Doc{Key: "end"}}, Position: 0},
	})
	want := []string{"a", "p2", "b", "p9", "end"}
	for i, r := range got {
		if i >= len(want) || r.Document.Key != want[i] {
			t.Fatalf("spliced %+v, want %v", got, want)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("spliced %d results, want %d", len(got), len(want))
	}
}
