package main

import (
	"net/http"
	"testing"
	"time"

	"mddb/internal/spell"
)

// Branches of the searchFTS steps (fts_search_run.go) the handler tests do
// not reach.

// TestSpellCorrectFTSOffForTheCollection: a ready spell manager corrects
// nothing in a collection without spellCorrect, or with no configuration.
func TestSpellCorrectFTSOffForTheCollection(t *testing.T) {
	s := covGServer(t)
	sm := spell.NewSpellManager(s.DB)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := sm.AddWord("blog", "en", "programming", 1000); err != nil {
		t.Fatal(err)
	}
	sm.LoadAll()
	deadline := time.Now().Add(5 * time.Second)
	for !sm.Ready() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s.SpellManager = sm
	covGSetConfig(t, s, "blog", &CollectionConfig{SpellCorrect: false})

	for _, collection := range []string{"blog", "unconfigured"} {
		req := FTSSearchRequest{Collection: collection, Query: "programing", Lang: "en"}
		if got := s.spellCorrectFTS(&req); got != nil {
			t.Errorf("%s: correction %+v, want none", collection, got)
		}
		if req.Query != "programing" {
			t.Errorf("%s: query changed to %q", collection, req.Query)
		}
	}
}

// TestFTSProximityClauseOfOneWordFallsBack: a proximity clause of a single
// word cannot be searched, so the whole query is searched at the clause's
// distance instead, and the search answers rather than failing.
func TestFTSProximityClauseOfOneWordFallsBack(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": `"quick"~3`, "mode": "proximity"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Mode != ftsModeProximity {
		t.Errorf("mode = %q, want %q", resp.Mode, ftsModeProximity)
	}
}
