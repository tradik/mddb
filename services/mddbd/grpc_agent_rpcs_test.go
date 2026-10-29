package main

import (
	"context"
	"testing"

	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func grpcParityServer(t *testing.T) (*Server, *GRPCServer, func()) {
	s, cleanup := newHandlerTestServer(t)
	s.VectorIndex.SetReady()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	s.Gain = NewGainRecorder(func() *bolt.DB { return s.DB })
	for _, d := range []struct{ key, content, dir string }{
		{"a/one.go", "package a\n\n// Backup writes a snapshot.\nfunc Backup() {}\n", "/a"},
		{"a/two.go", "package a\n\nfunc Restore() { Backup() }\n", "/a"},
		{"b/three.md", "# Backup guide\n\nHow to back up the database.\n", "/b"},
	} {
		doc := addTestDoc(t, s, "repo", d.key, "en", d.content, map[string][]string{"dir": {d.dir}})
		waitForEmbedding(t, s, "repo", doc.ID)
	}
	return s, &GRPCServer{server: s}, func() { s.EmbeddingWorker.Stop(); cleanup() }
}

func TestGRPC_SearchFeaturesOfTheRelease(t *testing.T) {
	_, g, cleanup := grpcParityServer(t)
	defer cleanup()
	ctx := context.Background()
	underA := map[string]*proto.MetaCondition{"dir": {Prefix: "/a"}}

	// Search: the operator filter.
	sr, err := g.Search(ctx, &proto.SearchRequest{Collection: "repo", Filter: underA})
	if err != nil || len(sr.Documents) != 2 {
		t.Fatalf("search filter: %v %+v", err, sr)
	}

	// FTS: filter, groupBy, passages.
	fr, err := g.FTS(ctx, &proto.FTSRequest{Collection: "repo", Query: "backup", Algorithm: "bm25", Filter: underA,
		GroupBy: &proto.GroupBy{Key: "dir", PerGroup: 1}})
	if err != nil || len(fr.Results) != 1 || fr.Results[0].Group != "/a" || fr.Results[0].GroupRank != 1 {
		t.Fatalf("fts: %v %+v", err, fr)
	}
	fp, err := g.FTS(ctx, &proto.FTSRequest{Collection: "repo", Query: "backup", Format: "passages", PassageLines: 1})
	if err != nil || len(fp.Passages) == 0 || fp.Passages[0].StartLine == 0 || fp.Passages[0].Content == "" {
		t.Fatalf("fts passages: %v %+v", err, fp)
	}
	if _, err := g.FTS(ctx, &proto.FTSRequest{Collection: "repo", Query: "x", Format: "nope"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad format: %v", err)
	}
	if _, err := g.FTS(ctx, &proto.FTSRequest{Collection: "repo", Query: "x", Algorithm: "nope"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad algorithm: %v", err)
	}

	// Vector: chunk mode, filter, waits, passages.
	vr, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "repo", Query: "backup", Filter: underA,
		RetrievalMode: "chunk", WaitForIndexMs: 1000, IncludeContent: true})
	if err != nil || len(vr.Results) == 0 || vr.Results[0].ChunkText == "" || vr.Indexing == nil {
		t.Fatalf("vector: %v %+v", err, vr)
	}
	for _, r := range vr.Results {
		if r.Document.Meta["dir"].Values[0] != "/a" {
			t.Fatalf("filter ignored: %+v", r.Document)
		}
	}
	vp, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "repo", Query: "backup", Format: "passages"})
	if err != nil || len(vp.Passages) == 0 {
		t.Fatalf("vector passages: %v %+v", err, vp)
	}
	if _, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "repo", Query: "x", ReadLevel: "nope"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad read level: %v", err)
	}

	// Hybrid: groupBy, passages, facets, content stripping.
	hr, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "repo", Query: "backup",
		GroupBy: &proto.GroupBy{Key: "dir"}, FacetBy: []string{"dir"}})
	if err != nil || len(hr.Results) != 2 || hr.Results[0].Group == "" || hr.Facets["dir"] == nil || hr.Results[0].Document.ContentMd != "" {
		t.Fatalf("hybrid: %v %+v", err, hr)
	}
	hp, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "repo", Query: "backup", Format: "passages", Filter: underA})
	if err != nil || len(hp.Passages) == 0 {
		t.Fatalf("hybrid passages: %v %+v", err, hp)
	}
	if _, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "repo", Query: "x", Format: "nope"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("hybrid bad format: %v", err)
	}

	// Cross: text search type, groupBy by collection, passages, query vector.
	cr, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{Query: "backup", TargetCollections: []string{"repo"},
		SearchType: "fts", IncludeContent: true, GroupBy: &proto.GroupBy{Key: "collection", PerGroup: 2}})
	if err != nil || len(cr.Results) != 2 || cr.Results[0].Group != "repo" {
		t.Fatalf("cross fts: %v %+v", err, cr)
	}
	cp, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{Query: "backup", TargetCollections: []string{"repo"}, Format: "passages"})
	if err != nil || len(cp.Passages) == 0 || cp.TargetCollections[0] != "repo" {
		t.Fatalf("cross passages: %v %+v", err, cp)
	}
	cv, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{QueryVector: []float32{0.1, 0.2, 0.3}, TargetCollections: []string{"repo"}})
	if err != nil || len(cv.Results) == 0 {
		t.Fatalf("cross vector: %v %+v", err, cv)
	}
	if _, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{Query: "x", TargetCollections: []string{"repo"}, SearchType: "nope"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("cross bad type: %v", err)
	}
	if _, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{SourceDocId: "x", TargetCollections: []string{"repo"}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("source without collection: %v", err)
	}
	if _, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{SourceCollection: "repo", SourceDocId: "missing",
		TargetCollections: []string{"repo"}}); status.Code(err) != codes.NotFound {
		t.Fatalf("source without vector: %v", err)
	}
}

func TestGRPC_SymbolEvalGain(t *testing.T) {
	s, g, cleanup := grpcParityServer(t)
	defer cleanup()
	ctx := context.Background()

	sym, err := g.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: "repo", Name: "Backup", IncludeContent: true})
	if err != nil || len(sym.Definitions) != 1 || sym.Definitions[0].Path != "a/one.go" || len(sym.Callers) == 0 {
		t.Fatalf("symbol: %v %+v", err, sym)
	}
	if _, err := g.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: "repo"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("symbol without name: %v", err)
	}

	q, err := g.AddEvalQuery(ctx, &proto.AddEvalQueryRequest{Collection: "repo", Query: &proto.EvalQueryProto{
		Query: "backup snapshot", ExpectedKeys: []string{"a/one.go"}, Grades: map[string]int32{"a/one.go": 3}}})
	if err != nil || q.Id == "" || q.Grades["a/one.go"] != 3 {
		t.Fatalf("add eval: %v %+v", err, q)
	}
	if _, err := g.AddEvalQuery(ctx, &proto.AddEvalQueryRequest{Collection: "repo", Query: &proto.EvalQueryProto{}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("add invalid: %v", err)
	}
	list, err := g.ListEvalQueries(ctx, &proto.ListEvalQueriesRequest{Collection: "repo"})
	if err != nil || len(list.Queries) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
	alpha := 0.5
	res, err := g.RunEval(ctx, &proto.RunEvalRequest{Collection: "repo", Details: true, Configs: []*proto.EvalConfigProto{
		{SearchType: "fts", Algorithm: "bm25"}, {SearchType: "hybrid", Alpha: &alpha}}})
	if err != nil || len(res.Results) != 2 || res.Results[0].RecallAtK != 1 || len(res.Results[0].PerQuery) != 1 {
		t.Fatalf("run eval: %v %+v", err, res)
	}
	inline, err := g.RunEval(ctx, &proto.RunEvalRequest{Collection: "repo", Queries: []*proto.EvalQueryProto{
		{Query: "restore", ExpectedKeys: []string{"a/two.go"}}}})
	if err != nil || inline.Queries != 1 {
		t.Fatalf("inline eval: %v %+v", err, inline)
	}
	if _, err := g.RunEval(ctx, &proto.RunEvalRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("eval without collection: %v", err)
	}
	del, err := g.DeleteEvalQuery(ctx, &proto.DeleteEvalQueryRequest{Collection: "repo", Id: q.Id})
	if err != nil || !del.Deleted {
		t.Fatalf("delete: %v %+v", err, del)
	}

	if _, err := g.FTS(ctx, &proto.FTSRequest{Collection: "repo", Query: "backup"}); err != nil {
		t.Fatal(err)
	}
	gain, err := g.GetGain(ctx, &proto.GetGainRequest{Window: "7d", Daily: true})
	if err != nil || gain.WindowDays != 7 || len(gain.Daily) != 7 || gain.TotalSearches == 0 || gain.ToDate == "" {
		t.Fatalf("gain: %v %+v", err, gain)
	}
	if _, err := g.GetGain(ctx, &proto.GetGainRequest{Window: "0d"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("gain window: %v", err)
	}
	s.Gain = nil
	if _, err := g.GetGain(ctx, &proto.GetGainRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("gain off: %v", err)
	}

	// Read-only mode refuses the writes; auth refuses an anonymous caller.
	s.Mode = ModeRead
	if _, err := g.AddEvalQuery(ctx, &proto.AddEvalQueryRequest{Collection: "repo"}); err == nil {
		t.Fatal("read-only add")
	}
	if _, err := g.DeleteEvalQuery(ctx, &proto.DeleteEvalQueryRequest{Collection: "repo"}); err == nil {
		t.Fatal("read-only delete")
	}
	s.Mode = ModeRW
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am
	for name, call := range map[string]func() error{
		"symbol": func() error { _, err := g.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: "repo"}); return err },
		"eval":   func() error { _, err := g.RunEval(ctx, &proto.RunEvalRequest{Collection: "repo"}); return err },
		"add": func() error {
			_, err := g.AddEvalQuery(ctx, &proto.AddEvalQueryRequest{Collection: "repo"})
			return err
		},
		"list": func() error {
			_, err := g.ListEvalQueries(ctx, &proto.ListEvalQueriesRequest{Collection: "repo"})
			return err
		},
		"delete": func() error {
			_, err := g.DeleteEvalQuery(ctx, &proto.DeleteEvalQueryRequest{Collection: "repo"})
			return err
		},
		"gain": func() error { _, err := g.GetGain(ctx, &proto.GetGainRequest{}); return err },
		"cross": func() error {
			_, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{Query: "x", TargetCollections: []string{"repo"}, SourceCollection: "repo"})
			return err
		},
	} {
		if status.Code(call()) != codes.PermissionDenied {
			t.Errorf("%s: anonymous caller not refused", name)
		}
	}
}

func TestGRPC_SearchStatus(t *testing.T) {
	for err, want := range map[error]codes.Code{
		&vectorLoadingError{Collection: "c"}: codes.Unavailable,
		errNoEmbeddingProvider:               codes.FailedPrecondition,
		errSourceNoEmbedding:                 codes.NotFound,
		errEmbedQuery:                        codes.Internal,
		errSymbolRequest:                     codes.InvalidArgument,
	} {
		if got := status.Code(searchStatus(err)); got != want {
			t.Errorf("%v: %v, want %v", err, got, want)
		}
	}
	if passagesOf(nil) != nil || indexWaitToProto(nil) != nil || metaFilterFromProto(nil) != nil || metaValuesFromProto(nil) != nil {
		t.Fatal("nil conversions")
	}
}

// Input errors are INVALID_ARGUMENT, not INTERNAL.
func TestGRPC_InputErrorsAreInvalidArgument(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	g := &GRPCServer{server: s}
	ctx := context.Background()
	if _, err := g.AddSynonym(ctx, &proto.AddSynonymRequest{Collection: "c", Term: "  ", Synonyms: []string{"x"}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("blank term: %v", err)
	}
	if _, err := g.AddStopwords(ctx, &proto.AddStopwordsRequest{Collection: "c", Words: []string{" ", ""}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("blank words: %v", err)
	}
	labels := make([]string, 101)
	for i := range labels {
		labels[i] = "l"
	}
	if _, err := g.Classify(ctx, &proto.ClassifyRequest{Text: "x", Labels: labels}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("too many labels: %v", err)
	}
	if anyNonBlank([]string{" "}) || !anyNonBlank([]string{" ", "a"}) || invalidInput("x").Error() != "x" {
		t.Fatal("helpers")
	}
}
