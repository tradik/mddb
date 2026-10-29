package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/indexqueue"
)

// Branches of the 2.18.8 features the feature tests do not reach on their
// way: refusals under auth, and inputs at the edges.

func TestAuthRefusals2188(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	withAuth(t, s)
	for name, h := range map[string]http.HandlerFunc{
		"indexing-status": s.handleIndexingStatus,
		"symbol":          s.handleSymbol,
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/x?collection=c&name=n", nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without credentials: %d", name, rec.Code)
		}
	}
}

func TestIndexingStatus_MetadataQueue(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.IndexQueue = indexqueue.NewIndexQueue(nil, 1)
	defer s.IndexQueue.Shutdown()
	s.IndexQueue.Pending().Add("c", "d")
	st := s.IndexingStatus("c")
	if st.PendingMetadata != 1 || st.Indexed || st.OldestPendingAt == nil || time.Since(*st.OldestPendingAt) > time.Minute {
		t.Fatalf("metadata queue: %+v", st)
	}
}

func TestSymbol_SingleLineCalleeAndGrouping(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "r", "a.go", "en", "package a\n\nfunc A() { B() }\n\nfunc B() {}\n", nil)
	res := lookup(t, s, SymbolRequest{Collection: "r", Name: "A", Include: []string{RelCallees}})
	if got := passageKeys(res.Callees); got != "a.go:5-5@B" {
		t.Fatalf("single-line callee: %s", got)
	}
	// Grouping by a meta key reads the documents behind chunk keys.
	labels := s.groupLabels("r", &GroupBy{Key: "language"}, []string{"r|a.go|en#0", "r|missing|en"})
	if labels["r|a.go|en#0"] != "" || len(labels) != 1 {
		t.Fatalf("labels: %v", labels)
	}
}

func TestCrossText_PassagesForFTSAndNoTargets(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedReferences(t, s)
	items := s.crossTextItems(context.Background(), crossTextParams{SearchType: CrossSearchFTS, Query: "delete",
		Targets: []string{"ref-a", "nope"}, TopK: 1, GroupBy: &GroupBy{Key: "document"}})
	if len(items) != 1 || items[0].Group == "" || items[0].Document.ContentMD != "" {
		t.Fatalf("grouped, cut, bodies dropped: %+v", items)
	}
	if got := s.oneCollectionText(context.Background(), "nope", crossTextParams{SearchType: CrossSearchHybrid, Query: "x", TopK: 3}); len(got) != 0 {
		t.Fatalf("unknown collection: %+v", got)
	}
	if !strings.HasPrefix(rolePrefix, "role") {
		t.Fatal("prefix")
	}
}

func TestCoverage2188_Edges(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedReferences(t, s)
	ctx := context.Background()

	// Grouping by a key the documents lack keeps each apart; topK still cuts.
	items := s.crossTextItems(ctx, crossTextParams{SearchType: CrossSearchFTS, Query: "delete",
		Targets: []string{"ref-a", "ref-b"}, TopK: 1, GroupBy: &GroupBy{Key: "author"}})
	if len(items) != 1 {
		t.Fatalf("cut to topK: %+v", items)
	}

	// Reference tools surface their search errors and an empty reference set.
	tools := &MCPToolServer{client: NewDirectClient(s)}
	if _, err := tools.toolReferenceSearch(ctx, map[string]interface{}{"query": "x", "search_type": "regex"}); err == nil {
		t.Fatal("bad search type")
	}
	if _, err := tools.toolReferenceExamples(ctx, map[string]interface{}{"name": "x", "collections": []interface{}{"role:nobody"}}); err == nil {
		t.Fatal("no matching reference collections")
	}

	// A name used without being called is not a call site.
	addTestDoc(t, s, "ref-a", "tree/hook.go", "en", "package tree\n\nvar onDelete = Delete\n", nil)
	res := lookup(t, s, SymbolRequest{Collection: "ref-a", Name: "Delete", Include: []string{RelReferences}})
	if strings.Contains(passageKeys(res.References), "hook.go") {
		t.Fatalf("a reference without a call: %s", passageKeys(res.References))
	}

	// Grouped hybrid items are cut to topK and renumbered.
	three := []HybridSearchResultItem{{}, {}, {}}
	for i := range three {
		three[i].Document.ID = "c|k" + itoa(i) + "|en"
	}
	got := groupHybridItems("c", three, &GroupBy{Key: "k"}, 2)
	if len(got) != 2 || got[1].Rank != 2 {
		t.Fatalf("groupHybridItems: %+v", got)
	}
}

func TestPendingMatches_LimitAndSeen(t *testing.T) {
	s, g, cleanup := indexingServer(t)
	defer cleanup()
	a := addViaHTTP(t, s, "p1", "walrus one")
	addViaHTTP(t, s, "p2", "walrus two")
	if ids := s.pendingMatches("notes", "walrus", nil, nil, 1, nil); len(ids) != 1 {
		t.Fatalf("limit: %v", ids)
	}
	all := map[string]bool{a.ID: true, "notes|p2|en": true}
	if ids := s.pendingMatches("notes", "walrus", nil, nil, 5, all); ids != nil {
		t.Fatalf("everything already seen: %v", ids)
	}
	g.open()
}

func TestIndexingStatus_ServerScopeUnderAuth(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	withAuth(t, s)
	rec := httptest.NewRecorder()
	s.handleIndexingStatus(rec, httptest.NewRequest(http.MethodGet, "/v1/indexing-status", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("server-wide status without credentials: %d", rec.Code)
	}
}
