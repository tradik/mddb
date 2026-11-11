package main

import (
	"fmt"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// direction=both listed an edge twice: once as "out" from the document that
// depends, once as "in" when the walk reached the document depended on and
// looked back. An edge is a statement about two documents — From depends on
// To through Symbol — so the graph lists it once, with the direction in
// which the walk first found it (the hop nearer the root).

func edgeIdentity(e GraphEdge) string {
	return fmt.Sprintf("%s -> %s %s %s", e.From, e.To, e.Kind, e.Symbol)
}

func TestGraphBothListsEachEdgeOnce(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	for _, root := range []string{"theme/index.html", "theme/style.css", "theme/app.js"} {
		for depth := 1; depth <= GraphMaxDepth; depth++ {
			res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: root, Direction: GraphBoth, Depth: depth})
			seen := map[string]string{}
			for _, e := range res.Edges {
				id := edgeIdentity(e)
				if dir, dup := seen[id]; dup {
					t.Errorf("%s depth %d: %s listed twice (%s and %s)", root, depth, id, dir, e.Direction)
				}
				seen[id] = e.Direction
			}
		}
	}
}

// The per-document dedupe and the lookups on a store without the buckets the
// graph reads.
func TestGraphResolverEdges(t *testing.T) {
	e := GraphEdge{From: "a", To: "b", Kind: EdgeImports, Symbol: "b"}
	if got := dedupeEdges([]resolvedEdge{{edge: e}, {edge: e}, {edge: GraphEdge{From: "a", To: "c"}}}); len(got) != 2 {
		t.Errorf("dedupe kept %d edges, want 2", len(got))
	}

	srv, cleanup := themeFixture(t)
	defer cleanup()
	empty, err := bolt.Open(t.TempDir()+"/empty.db", 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = empty.Close() }()
	_ = empty.View(func(tx *bolt.Tx) error {
		if ref := srv.lookupByKeyTx(tx, "theme", "theme/style.css"); ref != nil {
			t.Errorf("found %+v in a store without buckets", ref)
		}
		return nil
	})

	ref, err := srv.loadCodeDocByKey("theme", "theme/style.css")
	if err != nil {
		t.Fatal(err)
	}
	// Both storage layouts: the index lives in the collection's own file when
	// collections are stored per file.
	db, err := srv.collectionDBForWrite("theme")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(srv.BucketNames.IdxMeta) }); err != nil {
		t.Fatal(err)
	}
	if edges, truncated := srv.neighbours(GraphRequest{Collection: "theme", Direction: GraphBoth, MaxDegree: 10}, ref); len(edges) != 0 || truncated {
		t.Errorf("neighbours without a metadata index: %v, %v", edges, truncated)
	}
}

// The direction kept is the one seen from the root's side: from the
// stylesheet, the page applying its selectors depends on it ("in"), and the
// reset it imports is something it depends on ("out") — whatever the walk
// finds when it looks back from those documents one hop further.
func TestGraphBothKeepsTheDirectionNearestTheRoot(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/style.css", Direction: GraphBoth, Depth: 3})
	if e := edgeFor(res, "theme/index.html", "theme/style.css"); e == nil || e.Direction != string(GraphIn) {
		t.Errorf("index.html -> style.css: %+v, want direction in", e)
	}
	if e := edgeFor(res, "theme/style.css", "theme/reset.css"); e == nil || e.Direction != string(GraphOut) {
		t.Errorf("style.css -> reset.css: %+v, want direction out", e)
	}
	// Two hops away, index.html loads app.js: found looking out from
	// index.html, so "out".
	if e := edgeFor(res, "theme/index.html", "theme/app.js"); e == nil || e.Direction != string(GraphOut) {
		t.Errorf("index.html -> app.js: %+v, want direction out", e)
	}
}
