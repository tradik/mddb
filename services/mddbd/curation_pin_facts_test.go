package main

import (
	"testing"
)

func TestPinnedDocumentsCarryNoSemanticFacts(t *testing.T) {
	// A pinned document is spliced into the results, so it is shown like
	// them: without semantic facts (CODE-010).
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addFactsDoc(t, s, "svc/run.go")

	for _, pin := range []pinResolved{{Key: "svc/run.go"}, {Key: "svc/run.go", Lang: "en"}} {
		fts := s.loadPinnedFTS(factsCollection, []pinResolved{pin})
		if len(fts) != 1 || hasSemanticFacts(fts[0].Result.Document.Meta) {
			t.Errorf("pin %+v (fts): %+v", pin, fts)
		}
		hybrid := s.loadPinnedHybrid(factsCollection, []pinResolved{pin})
		if len(hybrid) != 1 || hasSemanticFacts(hybrid[0].Result.Document.Meta) {
			t.Errorf("pin %+v (hybrid): %+v", pin, hybrid)
		}
	}

	// No pins, and a pin naming no document, add nothing.
	if s.loadPinnedFTS(factsCollection, nil) != nil || s.loadPinnedHybrid(factsCollection, nil) != nil {
		t.Error("no pins should load nothing")
	}
	if got := s.loadPinnedFTS(factsCollection, []pinResolved{{Key: "svc/missing.go"}}); len(got) != 0 {
		t.Errorf("a pin without a document loaded %+v", got)
	}
}
