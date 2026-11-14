package main

import (
	"testing"

	"mddb/internal/storage"
)

func TestGraphNeighboursSkipNodesPastTheRequestedDepth(t *testing.T) {
	// The graph walk defaults a zero depth to one hop; the expansion still
	// honours the depth it was asked for and adds nothing.
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	covGSeedChain(t, srv)
	got := srv.graphNeighbours("chain", graphSeed{Key: "a.js", Score: 1}, GraphOut,
		GraphExpandOptions{Depth: 0, MaxNeighbours: 5}, map[string]bool{"a.js": true}, nil)
	if len(got) != 0 {
		t.Errorf("depth 0 expanded to %+v", got)
	}
}

func TestGraphNeighboursCarryNoSemanticFacts(t *testing.T) {
	// A neighbour appended to vector results is a result like the hits
	// before it (CODE-010).
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	covGSeedChain(t, srv)
	content := "export function c() { return 1; }\n"
	meta := map[string][]string{
		"language":      {"javascript"},
		MetaKeySymIndex: {"v1 " + semanticContentHash(content) + " d tsserver/1"},
		MetaKeySymDefs:  {"c function 1-1"},
	}
	if _, _, err := srv.addDocumentIf("chain", "c.js", "en", meta, content, 0, false, ""); err != nil {
		t.Fatal(err)
	}
	seed := []VectorSearchResultItem{{Document: storage.Doc{Key: "b.js"}, Score: 0.8, Rank: 1}}
	_, items := srv.appendGraphNeighbours("chain", seed, GraphExpandOptions{Depth: 1, Direction: "out"}, false)
	found := false
	for _, it := range items[1:] {
		found = found || it.Document.Key == "c.js"
		if hasSemanticFacts(it.Document.Meta) {
			t.Errorf("%s carries facts: %v", it.Document.Key, it.Document.Meta)
		}
	}
	if !found {
		t.Fatalf("c.js not expanded: %+v", items)
	}
}
