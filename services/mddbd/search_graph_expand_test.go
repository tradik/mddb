package main

import (
	"context"
	"testing"

	proto "mddb/proto"
)

// SRCH-006. The claim is that a query can reach a document it matches on no
// term, because something it did match depends on it. These tests build the
// smallest theme where that is true.

func seedGraphTheme(t *testing.T, srv *Server) {
	t.Helper()
	docs := []*proto.BatchDocument{
		makeBatchDoc("css/site.css", "en",
			".cart-total { font-weight: 700; }\n.hero { padding: 2rem; }\n",
			map[string]*proto.MetaValues{"language": {Values: []string{"css"}}}, false),
		makeBatchDoc("js/checkout.js", "en",
			"import { formatPrice } from \"./format.js\";\nexport function total(cart) {\n"+
				"  document.querySelector(\".cart-total\").textContent = formatPrice(cart.sum);\n}\n",
			map[string]*proto.MetaValues{"language": {Values: []string{"javascript"}}}, false),
		makeBatchDoc("js/format.js", "en",
			"export function formatPrice(cents) { return (cents / 100).toFixed(2); }\n",
			map[string]*proto.MetaValues{"language": {Values: []string{"javascript"}}}, false),
		makeBatchDoc("templates/cart.html", "en",
			"<link rel=\"stylesheet\" href=\"../css/site.css\">\n<span class=\"cart-total\"></span>\n"+
				"<script src=\"../js/checkout.js\"></script>\n",
			map[string]*proto.MetaValues{"language": {Values: []string{"html"}}}, false),
	}
	resp, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "theme", docs)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Failed > 0 {
		t.Fatalf("seeding failed: %v", resp.Errors)
	}
}

func TestGraphExpansionReachesADocumentTheQueryDidNotMatch(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedGraphTheme(t, srv)

	// One direct match: the script. Everything it touches should follow.
	expansions := srv.expandByGraph("theme",
		[]graphSeed{{Key: "js/checkout.js", Score: 0.9}}, GraphExpandOptions{})

	if len(expansions) == 0 {
		t.Fatal("a document with edges expanded to nothing")
	}

	reached := map[string]GraphExpansion{}
	for _, e := range expansions {
		reached[e.Key] = e
	}

	// format.js is imported by checkout.js and contains none of the query's
	// words; cart.html loads checkout.js.
	for _, want := range []string{"js/format.js", "templates/cart.html"} {
		e, ok := reached[want]
		if !ok {
			t.Errorf("%s was not reached: got %v", want, keysOfExpansions(expansions))
			continue
		}
		if e.Symbol == "" {
			t.Errorf("%s was added with no symbol to justify it", want)
		}
		if e.FromKey != "js/checkout.js" {
			t.Errorf("%s says it came from %q", want, e.FromKey)
		}
		if e.Score >= 0.9 {
			t.Errorf("%s inherited %v, which is not less than its source's 0.9", want, e.Score)
		}
	}
}

func TestGraphExpansionSkipsDocumentsThatAlreadyMatched(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedGraphTheme(t, srv)

	// Both the script and the stylesheet matched directly. Neither should be
	// re-added as the other's neighbour — that would double-count a document
	// that already earned its place.
	expansions := srv.expandByGraph("theme", []graphSeed{
		{Key: "js/checkout.js", Score: 0.9},
		{Key: "css/site.css", Score: 0.8},
	}, GraphExpandOptions{})

	for _, e := range expansions {
		if e.Key == "js/checkout.js" || e.Key == "css/site.css" {
			t.Errorf("%s was already a direct match and was added again as an expansion", e.Key)
		}
	}
}

func TestGraphExpansionDecaysWithDistance(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedGraphTheme(t, srv)

	expansions := srv.expandByGraph("theme",
		[]graphSeed{{Key: "js/checkout.js", Score: 1.0}},
		GraphExpandOptions{Depth: 2, Decay: 0.5})

	for _, e := range expansions {
		want := 1.0
		for i := 0; i < e.Depth; i++ {
			want *= 0.5
		}
		if e.Score != want {
			t.Errorf("%s at depth %d scored %v, want %v", e.Key, e.Depth, e.Score, want)
		}
	}
}

func TestGraphExpansionOnACollectionWithNoEdges(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()

	// Prose. No language, no symbols, no edges — most collections.
	docs := []*proto.BatchDocument{
		makeBatchDoc("a.md", "en", "a paragraph about certificates", nil, false),
		makeBatchDoc("b.md", "en", "a paragraph about deployments", nil, false),
	}
	if _, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "prose", docs); err != nil {
		t.Fatal(err)
	}

	expansions := srv.expandByGraph("prose",
		[]graphSeed{{Key: "a.md", Score: 0.9}}, GraphExpandOptions{})

	if len(expansions) != 0 {
		t.Errorf("a collection with no edges expanded to %v", keysOfExpansions(expansions))
	}
}

func TestGraphExpansionHandlesMissingSeeds(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedGraphTheme(t, srv)

	// A key that is not in the collection must be skipped, not fail the search
	// it was expanding.
	expansions := srv.expandByGraph("theme",
		[]graphSeed{{Key: "does/not/exist.js", Score: 0.9}}, GraphExpandOptions{})
	if len(expansions) != 0 {
		t.Errorf("a missing seed expanded to %v", keysOfExpansions(expansions))
	}

	if got := srv.expandByGraph("theme", nil, GraphExpandOptions{}); got != nil {
		t.Errorf("no seeds expanded to %v", got)
	}
}

func TestGraphExpansionIsDeterministic(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedGraphTheme(t, srv)

	seeds := []graphSeed{{Key: "css/site.css", Score: 0.9}}
	first := srv.expandByGraph("theme", seeds, GraphExpandOptions{})
	second := srv.expandByGraph("theme", seeds, GraphExpandOptions{})

	if len(first) != len(second) {
		t.Fatalf("two runs expanded to %d and %d documents", len(first), len(second))
	}
	for i := range first {
		if first[i].Key != second[i].Key {
			t.Errorf("position %d differs between runs: %q vs %q", i, first[i].Key, second[i].Key)
		}
	}
}

func TestGraphExpandOptionsDefaultsAndClamps(t *testing.T) {
	got := GraphExpandOptions{}.Defaults()
	if got.Depth != GraphExpandDefaultDepth {
		t.Errorf("Depth = %d", got.Depth)
	}
	if got.Decay != GraphExpandDefaultDecay {
		t.Errorf("Decay = %v", got.Decay)
	}
	if got.MaxNeighbours != GraphExpandDefaultMaxNeighbours {
		t.Errorf("MaxNeighbours = %d", got.MaxNeighbours)
	}
	if got.Direction != string(GraphBoth) {
		t.Errorf("Direction = %q", got.Direction)
	}

	// Depth is clamped for the same reason CODE-005 clamps it: a popular
	// selector's neighbourhood at depth 10 is the whole collection.
	if deep := (GraphExpandOptions{Depth: 10}).Defaults(); deep.Depth != GraphExpandMaxDepth {
		t.Errorf("Depth 10 became %d, want %d", deep.Depth, GraphExpandMaxDepth)
	}
	// A decay above 1 would make a neighbour outrank the document that
	// reached it.
	if hot := (GraphExpandOptions{Decay: 5}).Defaults(); hot.Decay != GraphExpandDefaultDecay {
		t.Errorf("Decay 5 became %v", hot.Decay)
	}
	if neg := (GraphExpandOptions{Decay: -1}).Defaults(); neg.Decay != GraphExpandDefaultDecay {
		t.Errorf("Decay -1 became %v", neg.Decay)
	}
}

func TestGraphRetrievalModeIsAccepted(t *testing.T) {
	for _, mode := range []string{"", "parent", "chunk", "window", "graph"} {
		if !validRetrievalMode(mode) {
			t.Errorf("retrievalMode %q was rejected", mode)
		}
	}
	if validRetrievalMode("entity") {
		t.Error("an unimplemented mode was accepted")
	}
}

func keysOfExpansions(e []GraphExpansion) []string {
	out := make([]string, len(e))
	for i := range e {
		out[i] = e[i].Key
	}
	return out
}
