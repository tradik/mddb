package main

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

// GO-052: how long a move of a collection the size of the benchmark
// profile's corpus takes (TEST-005 small: 20,000 documents with 128-number
// vectors). Opt-in: MDDB_PLACEMENT_MEASURE=<documents>.
//
//	MDDB_PLACEMENT_MEASURE=20000 go test -run TestPlacementMeasureMove -v .
func TestPlacementMeasureMove(t *testing.T) {
	docs, _ := strconv.Atoi(os.Getenv("MDDB_PLACEMENT_MEASURE"))
	if docs <= 0 {
		t.Skip("set MDDB_PLACEMENT_MEASURE=<documents> to measure a move")
	}
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 2}, "meta")
	const c = "measured"
	from := mustOwner(t, tp, c)
	src := tp.leader(from).s
	rng := rand.New(rand.NewPCG(1, 2)) // #nosec G404 -- a seeded, reproducible test corpus, not a secret
	seeded := time.Now()
	for i := range docs {
		saved, _, err := src.addDocument(c, fmt.Sprintf("doc-%04d", i), "en", nil,
			fmt.Sprintf("document %d about topic %d with some words", i, i%97), 0, true)
		if err != nil {
			t.Fatal(err)
		}
		v := make([]float32, 128)
		for j := range v {
			v[j] = rng.Float32()
		}
		if err := src.VectorStore.Put(c, saved.ID, v, "measure", ""); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("seeded %d documents in %s", docs, time.Since(seeded).Round(time.Millisecond))
	to := otherGroup(from)
	load := startMoveLoad(tp, c, docs)
	time.Sleep(200 * time.Millisecond)
	var moved struct {
		Move moveStatus `json:"move"`
	}
	began := time.Now()
	if _, err := tp.nodes[0].call(http.MethodPost, "/v1/placement/move?wait=true", map[string]string{"collection": c, "group": to}, &moved); err != nil {
		t.Fatal(err)
	}
	took := time.Since(began)
	time.Sleep(200 * time.Millisecond)
	load.finish()
	if len(load.errs) > 0 {
		t.Fatalf("%d requests failed, first: %s", len(load.errs), load.errs[0])
	}
	want := docs + len(load.acked)
	if _, n := tp.leader(to).s.collectionChecksum(c); n != want {
		t.Fatalf("the new group holds %d documents, want %d", n, want)
	}
	t.Logf("moved %d documents (%d keys) %s→%s in %s (coordinator: %d ms); %d writes and %d reads during it, none failed",
		docs, moved.Move.KeysCopied, from, to, took.Round(time.Millisecond), moved.Move.DurationMs, load.writes, load.reads)
}
