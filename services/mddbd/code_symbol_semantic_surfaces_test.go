package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gql "mddb/graphql"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
	proto "mddb/proto"
)

// CODE-012 on every surface, in graph expansion and in passages.

func TestCodeSymbolSemantic_Surfaces(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSemRepo(t, s)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	s.handleSymbol(rec, httptest.NewRequest(http.MethodGet,
		"/v1/symbol?collection=sem&name=Closer&path=io/closer.go&include=definitions,implementations", nil))
	var rest SymbolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &rest); err != nil || rec.Code != 200 || len(rest.Implementations) != 3 ||
		rest.Precision != PrecisionSemantic || !strings.Contains(rec.Body.String(), `"target":"io/closer.go#Closer"`) {
		t.Fatalf("REST: %d %s", rec.Code, rec.Body.String())
	}

	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.toolCodeSymbol(ctx, map[string]interface{}{"collection": semColl, "name": "Close"})
	if err != nil || !strings.Contains(out, `"ambiguous": true`) || !strings.Contains(out, `"name": "Buffer.Close"`) {
		t.Fatalf("MCP: %v %s", err, out)
	}
	out, err = tools.toolCodeSymbol(ctx, map[string]interface{}{"collection": semColl, "name": "Close", "path": "buf/buf.go"})
	if err != nil || strings.Contains(out, `"ambiguous"`) || !strings.Contains(out, `"container": "Buffer"`) {
		t.Fatalf("MCP with path: %v %s", err, out)
	}

	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	path := "net/conn.go"
	g, err := adapter.CodeSymbol(ctx, gql.CodeSymbolInput{Collection: semColl, Name: "Close", Path: &path})
	if err != nil || g.Precision == nil || *g.Precision != PrecisionSemantic || g.Ambiguous || len(g.References) != 3 ||
		g.References[0].Target == nil || *g.Definitions[0].Container != "Conn" || *g.SemanticSource != "gopls/0.24.0" {
		t.Fatalf("GraphQL: %v %+v", err, g)
	}
	g, err = adapter.CodeSymbol(ctx, gql.CodeSymbolInput{Collection: semColl, Name: "Close"})
	if err != nil || !g.Ambiguous || len(g.Candidates) != 3 || g.Candidates[2].Name != "Conn.Close" {
		t.Fatalf("GraphQL candidates: %v %+v", err, g)
	}

	rpc := &GRPCServer{server: s}
	sym, err := rpc.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: semColl, Name: "Close"})
	if err != nil || !sym.Ambiguous || len(sym.Candidates) != 3 || sym.Candidates[0].Path != "buf/buf.go" || sym.Precision != PrecisionSemantic {
		t.Fatalf("gRPC: %v %+v", err, sym)
	}
	sym, err = rpc.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: semColl, Name: "Closer", Path: "io/closer.go",
		Include: []string{RelImplementations}})
	if err != nil || len(sym.Implementations) != 3 || sym.Implementations[2].Target != "io/closer.go#Closer" ||
		sym.Implementations[2].Container != "" || sym.SemanticSource != "gopls/0.24.0" {
		t.Fatalf("gRPC implementations: %v %+v", err, sym)
	}
}

func TestGraphExpand_SemanticKinds(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		seedSemRepo(t, s)
		addTestDoc(t, s, semColl, "legacy/old.go", "en", "package legacy\n\nfunc Old() {}\n", nil)
		expand := func(seed, kind string, max int) map[string]string {
			out := map[string]string{}
			for _, e := range s.expandByGraph(semColl, []graphSeed{{Key: seed, Score: 1}}, GraphExpandOptions{Kinds: []string{kind}, MaxNeighbours: max}) {
				out[e.Key] = e.Symbol + "/" + e.Kind
			}
			return out
		}
		if got := expand("net/conn.go", RelCallers, 0); len(got) != 2 || got["app/main.go"] != "Conn/callers" || got["app/main_test.go"] == "" {
			t.Fatalf("callers: %v", got)
		}
		if got := expand("net/conn.go", RelTests, 0); len(got) != 1 || got["app/main_test.go"] == "" {
			t.Fatalf("tests: %v", got)
		}
		if got := expand("app/main.go", RelCallees, 0); len(got) != 2 || got["file/file.go"] == "" || got["net/conn.go"] == "" {
			t.Fatalf("callees: %v", got)
		}
		if got := expand("io/closer.go", RelImplementations, 0); len(got) != 3 || got["buf/buf.go"] != "Closer/implementations" {
			t.Fatalf("implementations: %v", got)
		}
		if got := expand("io/closer.go", RelImplementations, 1); len(got) != 1 {
			t.Fatalf("max neighbours: %v", got)
		}
		// Without facts: implementations add nothing; a missing seed neither.
		if got := expand("legacy/old.go", RelImplementations, 0); len(got) != 0 {
			t.Fatalf("no facts: %v", got)
		}
		if got := expand("gone.go", RelImplementations, 0); len(got) != 0 {
			t.Fatalf("missing seed: %v", got)
		}
	})
}

func TestPassagesNameTheQualifiedDefinition(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSemRepo(t, s)
	file := semRepo["file/file.go"]
	doc := storage.Doc{ID: genID(semColl, "file/file.go", "en"), Key: "file/file.go", Lang: "en",
		Meta: map[string][]string{"path": {"file/file.go"}, "kind": {"code"}, "language": {"go"}}, ContentMD: file.content}
	ps := s.buildPassages(semColl, []passageSource{{doc: doc, bestChunk: -1, terms: []string{"flush"}}}, PassageOptions{}, "fts")
	if len(ps) != 1 || ps[0].Symbol != "File.Close" {
		t.Fatalf("passage: %+v", ps)
	}
	if hasSemanticFacts(doc.Meta) {
		t.Fatal("the hit's own meta must stay without facts")
	}
	// Another document with the same content is not this one.
	other := doc
	other.ID = "other"
	if got := s.withSemanticFacts(semColl, &other); got != &other {
		t.Fatal("facts of another document")
	}
	if s.withSemanticFacts(semColl, nil) != nil {
		t.Fatal("nil")
	}
}

func TestSemanticHelpers(t *testing.T) {
	if combinePrecision("", PrecisionSemantic, true) != PrecisionSemantic ||
		combinePrecision(PrecisionSemantic, PrecisionSemantic, false) != PrecisionSemantic ||
		combinePrecision("", PrecisionHeuristic, false) != PrecisionHeuristic ||
		combinePrecision(PrecisionHeuristic, "", false) != PrecisionHeuristic ||
		combinePrecision(PrecisionSemantic, "", false) != PrecisionMixed {
		t.Fatal("combinePrecision")
	}
	if bareName("a.b.C") != "C" || bareName("C") != "C" || !matchesSymbol("x.Close", "Close") || matchesSymbol("Closer", "Close") {
		t.Fatal("names")
	}
	st := &sourceText{doc: &storage.Doc{}}
	if st.definitionAt(1) != nil || st.defNamed("x") != nil || st.symbolAt(1) != "" {
		t.Fatal("no facts")
	}
	r := &factReader{docs: map[string]*sourceText{}}
	if r.doc("x") != nil || r.atPath("x") != nil {
		t.Fatal("reader without buckets")
	}
}
