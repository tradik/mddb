package main

import (
	"context"
	"testing"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"

	proto "mddb/proto"
)

func TestCovGExpandByGraphInvalidDirectionAndNeighbourCap(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedGraphTheme(t, srv)
	seed := []graphSeed{{Key: "js/checkout.js", Score: 0.9}}

	// An unknown direction falls back to both, so it finds what "both" finds.
	both := srv.expandByGraph("theme", seed, GraphExpandOptions{Direction: "both"})
	odd := srv.expandByGraph("theme", seed, GraphExpandOptions{Direction: "sideways"})
	if len(both) < 2 || len(odd) != len(both) {
		t.Fatalf("invalid direction: %d vs both %d", len(odd), len(both))
	}

	// One neighbour per source, however many edges it has.
	for _, key := range []string{"js/checkout.js", "js/format.js", "css/site.css", "templates/cart.html"} {
		for _, depth := range []int{1, 2, 3} {
			for _, dir := range []string{"in", "out", "both"} {
				capped := srv.expandByGraph("theme", []graphSeed{{Key: key, Score: 1}},
					GraphExpandOptions{MaxNeighbours: 1, Depth: depth, Direction: dir})
				if len(capped) > 1 {
					t.Errorf("%s depth %d %s: maxNeighbours 1 returned %d", key, depth, dir, len(capped))
				}
			}
		}
	}
}

func TestCovGAppendGraphNeighbours(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedGraphTheme(t, srv)

	// No results, nothing to expand.
	exp, items := srv.appendGraphNeighbours("theme", nil, GraphExpandOptions{}, false)
	if exp != nil || items != nil {
		t.Errorf("empty input: %v %v", exp, items)
	}

	// A seed with no edges adds nothing.
	prose := []VectorSearchResultItem{{Document: storage.Doc{Key: "notes/readme.md"}, Score: 0.5, Rank: 1}}
	exp, items = srv.appendGraphNeighbours("theme", prose, GraphExpandOptions{}, false)
	if exp != nil || len(items) != 1 {
		t.Errorf("no edges: %v %d", exp, len(items))
	}

	seed := []VectorSearchResultItem{{Document: storage.Doc{Key: "js/checkout.js"}, Score: 0.8, Rank: 1}}
	exp, items = srv.appendGraphNeighbours("theme", seed, GraphExpandOptions{}, false)
	if len(exp) < 2 || len(items) != 1+len(exp) {
		t.Fatalf("expansion: %d explanations, %d items", len(exp), len(items))
	}
	for i, it := range items[1:] {
		if it.Rank != i+2 || it.Document.ContentMD != "" || it.Score >= 0.8 || it.Document.ID == "" {
			t.Errorf("neighbour %d: %+v", i, it)
		}
	}

	exp, items = srv.appendGraphNeighbours("theme", seed, GraphExpandOptions{}, true)
	if items[len(items)-1].Document.ContentMD == "" || len(exp) == 0 {
		t.Error("includeContent should keep the neighbours' bodies")
	}

	// A neighbour whose document is gone is explained but not returned.
	gone, err := srv.loadCodeDocByKey("theme", "js/format.js")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.DBUpdateC("theme", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Delete(storage.DocKey("theme", gone.DocID))
	}); err != nil {
		t.Fatal(err)
	}
	_, items = srv.appendGraphNeighbours("theme", seed, GraphExpandOptions{}, false)
	for _, it := range items {
		if it.Document.Key == "js/format.js" {
			t.Error("a deleted neighbour must not be returned")
		}
	}
}

// covGSeedChain stores a.js -> b.js -> c.js, each importing the next.
func covGSeedChain(t *testing.T, srv *Server) {
	t.Helper()
	js := map[string]*proto.MetaValues{"language": {Values: []string{"javascript"}}}
	docs := []*proto.BatchDocument{
		makeBatchDoc("a.js", "en", "import { b } from \"./b.js\";\nexport function a() { return b(); }\n", js, false),
		makeBatchDoc("b.js", "en", "import { c } from \"./c.js\";\nexport function b() { return c(); }\n", js, false),
		makeBatchDoc("c.js", "en", "export function c() { return 1; }\n", js, false),
	}
	resp, err := NewBatchProcessor(srv, 2).ProcessBatch(context.Background(), "chain", docs)
	if err != nil || resp.Failed > 0 {
		t.Fatalf("seeding: %v %v", err, resp)
	}
}

func TestCovGGraphNeighboursStopAtTheCapAcrossHops(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	covGSeedChain(t, srv)

	all := srv.expandByGraph("chain", []graphSeed{{Key: "a.js", Score: 1}},
		GraphExpandOptions{Depth: 2, Direction: "out"})
	if len(all) != 2 {
		t.Fatalf("two hops out of a.js should reach b.js and c.js, got %+v", all)
	}
	capped := srv.expandByGraph("chain", []graphSeed{{Key: "a.js", Score: 1}},
		GraphExpandOptions{Depth: 2, Direction: "out", MaxNeighbours: 1})
	if len(capped) != 1 || capped[0].Key != "b.js" {
		t.Errorf("the cap keeps the nearest neighbour only: %+v", capped)
	}
}
