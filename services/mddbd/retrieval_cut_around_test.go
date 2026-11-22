package main

import (
	"context"
	"strings"
	"testing"

	gql "mddb/graphql"
	"mddb/proto"
)

// A long line cut around what matched, and the 2.19.13 fields on gRPC and
// GraphQL: excludeTests, totals, passageTokens, truncated.

func TestCutAround(t *testing.T) {
	long := strings.Repeat("filler words here ", 30) + "the lease is released at suspend" + strings.Repeat(" and more text", 30)
	got := cutAround(long, []string{"Lease"}, 160)
	if !strings.HasPrefix(got, cutLead) || !strings.HasSuffix(got, cutMark) || !strings.Contains(got, "the lease is released") ||
		len(got) != len(cutLead)+160+len(cutMark) {
		t.Fatalf("around the match: %d %q", len(got), got)
	}
	// A match early in the line, none at all, and a fuzzy term's indexed
	// form: from the start.
	for _, terms := range [][]string{{"filler"}, {"absent"}, nil, {"fillr~filler"}} {
		if got := cutAround(long, terms, 160); got != long[:160]+cutMark {
			t.Fatalf("%v: %q", terms, got)
		}
	}
	if got := cutAround("short", []string{"short"}, 160); got != "short" {
		t.Fatalf("short text: %q", got)
	}
	// A match near the end: the cut runs to the end and has no end mark.
	tail := strings.Repeat("x ", 200) + "needle"
	if got := cutAround(tail, []string{"needle"}, 100); !strings.HasPrefix(got, cutLead) || !strings.HasSuffix(got, "needle") {
		t.Fatalf("tail: %q", got)
	}
	// Both cut ends land on rune boundaries.
	runes := "a" + strings.Repeat("é", 200) + " needle " + strings.Repeat("é", 200)
	got = cutAround(runes, []string{"needle"}, 104)
	if strings.ContainsRune(got, '\uFFFD') || !strings.Contains(got, "needle") {
		t.Fatalf("rune boundaries: %q", got)
	}
	// Lowering changes this text's length, so a match cannot be placed in
	// it: cut from the start.
	odd := strings.Repeat("İ", 100) + " needle"
	if got := cutAround(odd, []string{"needle"}, 50); strings.HasPrefix(got, cutLead) || !strings.HasSuffix(got, cutMark) {
		t.Fatalf("length-changing lower: %q", got)
	}
}

// farMatchDoc is one paragraph whose match is far past the start.
func farMatchDoc() string {
	return "# Notes\n\n" + strings.Repeat("Sessions are created and destroyed by the manager. ", 60) +
		"A zebra holds no quota lease." + strings.Repeat(" Trailing words follow.", 60) + "\n"
}

func TestLongLineShowsTheMatch(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		ts := seedLineRepo(t, s)
		addTestDoc(t, s, semColl, "docs/far.md", "en", farMatchDoc(), map[string][]string{"path": {"docs/far.md"}})
		for _, args := range []map[string]interface{}{
			{"collection": semColl, "query": "zebra"},
			{"collection": semColl, "query": "Zebra holds", "mode": "exact"},
		} {
			out := lineTool(t, ts, "code_search", args)
			if !strings.Contains(out, cutLead) || !strings.Contains(out, "zebra holds no quota lease") || len(out) > 400 {
				t.Fatalf("%v:\n%s", args, out)
			}
		}
		// The JSON passage of the same hit: one line over the bound, cut
		// around the match.
		got := decodePassages(t, []byte(lineTool(t, ts, "full_text_search", map[string]interface{}{
			"collection": semColl, "query": "zebra", "format": "passages", "passage_tokens": 60.0})))
		if p := got.Passages[0]; !p.Truncated || !strings.HasPrefix(p.Content, cutLead) || !strings.Contains(p.Content, "zebra holds") || p.Tokens > 64 {
			t.Fatalf("passage: %+v", p)
		}
	})
}

func TestSymbolAndPassageFields_GRPCAndGraphQL(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		seedLineRepo(t, s)
		ctx := context.Background()
		rpc := &GRPCServer{server: s}
		sym, err := rpc.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: semColl, Name: "Close", Limit: 1, ExcludeTests: true})
		if err != nil || sym.Totals[RelDefinitions] != 3 || sym.Totals[RelReferences] != 3 || len(sym.Tests) != 0 {
			t.Fatalf("gRPC symbol: %v %+v", err, sym.GetTotals())
		}
		if sym, err = rpc.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: semColl, Name: "Closer"}); err != nil || sym.Totals != nil {
			t.Fatalf("gRPC symbol, nothing cut: %v %+v", err, sym.GetTotals())
		}
		fp, err := rpc.FTS(ctx, &proto.FTSRequest{Collection: semColl, Query: "lease suspended decision", Format: "passages", PassageTokens: 40})
		if err != nil || len(fp.Passages) != 1 || !fp.Passages[0].Truncated || fp.Passages[0].Tokens > 44 {
			t.Fatalf("gRPC passages: %v %+v", err, fp.GetPassages())
		}

		adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
		limit, yes := 1, true
		g, err := adapter.CodeSymbol(ctx, gql.CodeSymbolInput{Collection: semColl, Name: "Close", Limit: &limit, ExcludeTests: &yes})
		if err != nil || len(g.Totals) < 2 || g.Totals[0].Relation != RelDefinitions || g.Totals[0].Total != 3 || len(g.Tests) != 0 {
			t.Fatalf("GraphQL symbol: %v %+v", err, g)
		}
		format, tokens := FormatPassages, 40
		res, err := adapter.FullTextSearch(ctx, gql.FTSInput{Collection: semColl, Query: "lease suspended decision", Format: &format, PassageTokens: &tokens})
		if err != nil || len(res.Passages) != 1 || res.Passages[0].Truncated == nil || !*res.Passages[0].Truncated {
			t.Fatalf("GraphQL passages: %v %+v", err, res)
		}
		// A passage that fits carries no truncated at all.
		res, err = adapter.FullTextSearch(ctx, gql.FTSInput{Collection: semColl, Query: "flush", Format: &format})
		if err != nil || len(res.Passages) != 1 || res.Passages[0].Truncated != nil {
			t.Fatalf("GraphQL passage that fits: %v %+v", err, res)
		}
	})
}
