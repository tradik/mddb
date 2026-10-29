package fts

import (
	"strings"
	"testing"
)

// SRCH-018: b decides how much document length counts. A short document
// with one occurrence and a long one with three trade places when b goes
// from 0.75 to 0; the defaults keep today's order.
func TestBM25Params_LengthNormalisationChangesOrder(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()
	filler := strings.Repeat("other words fill this document with unrelated text ", 20)
	if err := idx.Index("c", "short", "kernel tuning"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index("c", "long", "kernel kernel kernel "+filler); err != nil {
		t.Fatal(err)
	}
	if err := idx.Index("c", "none", "nothing relevant here"); err != nil {
		t.Fatal(err)
	}
	first := func(p BM25Params, fielded bool) string {
		t.Helper()
		var res []FTSResult
		var err error
		if fielded {
			res, err = idx.SearchBM25F("c", map[string]int{"kernel": 1}, 10, nil, p)
		} else {
			res, err = idx.SearchBM25("c", "kernel", 10, p)
		}
		if err != nil || len(res) != 2 {
			t.Fatalf("search %+v: %v %+v", p, err, res)
		}
		return res[0].DocID
	}
	noLength := BM25Params{K1: 1.2, B: 0}
	if got := first(DefaultBM25, false); got != "short" {
		t.Fatalf("default: %s first, want short (golden order)", got)
	}
	if got := first(noLength, false); got != "long" {
		t.Fatalf("b=0: %s first, want long", got)
	}
	// Fuzzy BM25 takes the same parameters.
	fz, err := idx.SearchBM25Fuzzy("c", "kernal", 10, 1, noLength)
	if err != nil || len(fz) == 0 || fz[0].DocID != "long" {
		t.Fatalf("fuzzy b=0: %v %+v", err, fz)
	}
	// BM25F uses them per field.
	if a, b := first(DefaultBM25, true), first(noLength, true); a == b {
		t.Fatalf("bm25f: b did not change the order (%s)", a)
	}
	if _, err := idx.SearchBM25FFuzzy("c", map[string]int{"kernal": 1}, 10, 1, nil, noLength); err != nil {
		t.Fatal(err)
	}
}

func TestBM25Params_Validate(t *testing.T) {
	for _, ok := range []BM25Params{DefaultBM25, {0, 0}, {3, 1}} {
		if err := ok.Validate(); err != nil {
			t.Fatalf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []BM25Params{{-0.1, 0.5}, {3.1, 0.5}, {1, -0.1}, {1, 1.1}} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	// k1=0 ignores repetition: two occurrences score as one.
	p := BM25Params{K1: 0, B: 0.75}
	if p.termScore(1, 1, 10, 10) != p.termScore(1, 5, 10, 10) {
		t.Fatal("k1=0 saturates at once")
	}
}
