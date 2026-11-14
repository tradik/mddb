package main

import (
	"context"
	"testing"
)

func TestHybridHitsCarryNoSemanticFacts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addFactsDoc(t, s, "svc/run.go")
	items := []HybridSearchResultItem{{CombinedScore: 1}}
	items[0].Document.ID = genID(factsCollection, "svc/run.go", "en")
	got := s.loadHybridDocs(factsCollection, items, true)
	if len(got) != 1 || hasSemanticFacts(got[0].Document.Meta) || got[0].Document.Meta["path"] == nil {
		t.Errorf("hybrid hit: %+v", got)
	}
}

func TestRunVectorSearchWithoutEmbeddings(t *testing.T) {
	// Automation triggers call the vector half alone; with no provider it
	// finds nothing and is not an error, like the hybrid search it belongs to.
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	res, err := s.runVectorSearch(context.Background(), HybridSearchRequest{Collection: factsCollection, Query: "x"})
	if err != nil || res != nil {
		t.Errorf("runVectorSearch = %v, %v", res, err)
	}
}
