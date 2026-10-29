package main

import (
	"strings"
	"testing"
)

const gainAnswer = `{"windowDays":30,"totals":{"searches":{"vector":7410,"hybrid":5822,"fts":5160},
 "algorithms":{"hnsw":65,"flat":12,"pq":8},"fusion":{"rrf":44,"alpha":56},
 "tokensReturned":21700000,"tokensAvoided":160700000,"fragmentHits":68102,"documentHits":14922},
 "totalSearches":18392,"tokensWithoutRetrieval":182400000,"savedPercent":88.1}`

func TestGain_Prints(t *testing.T) {
	srv := newFakeServer(t, map[string]interface{}{"/v1/gain": gainAnswer})
	out, err := runCLI(t, srv.URL, "gain", "--window", "7d")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Last 30 days", "18392   (vector 7410 · hybrid 5822 · fts 5160)", "182.4M tokens",
		"160.7M  (88.1%)", "hnsw 76%", "Fusion alpha 56% · rrf 44%", "Passages returned   68102"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if out, _ = runCLI(t, srv.URL, "--json", "gain"); !strings.Contains(out, `"savedPercent":88.1`) {
		t.Fatalf("json: %s", out)
	}
}

func TestGain_EmptyAndErrors(t *testing.T) {
	empty := newFakeServer(t, map[string]interface{}{"/v1/gain": `{"windowDays":30,"totals":{}}`})
	out, err := runCLI(t, empty.URL, "gain")
	if err != nil || strings.Contains(out, "Algorithms") {
		t.Fatalf("empty: %v %s", err, out)
	}
	if _, err := runCLI(t, newFakeServer(t, map[string]interface{}{"/v1/gain": failure{400, `{"error":"window"}`}}).URL, "gain"); err == nil {
		t.Fatal("server error")
	}
	if _, err := runCLI(t, newFakeServer(t, map[string]interface{}{"/v1/gain": `[1]`}).URL, "gain"); err == nil {
		t.Fatal("bad response")
	}
	if compact(512) != "512" || compact(2_500_000_000) != "2.5G" || compact(1500) != "1.5k" {
		t.Fatal("compact")
	}
	if shares(map[string]int64{"b": 1, "a": 1}, false) != "a 1 · b 1" {
		t.Fatal("tie order")
	}
}
