package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Line answers for agents: code_search, code_symbol format lines, and the
// options that keep an answer small.

// adrDoc is a document whose hit is a heading over one long paragraph.
func adrDoc() string {
	return "# ADR 7\n\nIntro.\n\n## Decision: no lease while suspended\n\n" +
		strings.Repeat("A suspended session holds no quota lease. ", 40) + "\n\nMore text here.\n"
}

// seedLineRepo is the semantic repository plus one Markdown document.
func seedLineRepo(t *testing.T, s *Server) *MCPToolServer {
	t.Helper()
	seedSemRepo(t, s)
	addTestDoc(t, s, semColl, "docs/adr.md", "en", adrDoc(), map[string][]string{"path": {"docs/adr.md"}})
	return &MCPToolServer{client: NewDirectClient(s)}
}

func lineTool(t *testing.T, ts *MCPToolServer, name string, args map[string]interface{}) string {
	t.Helper()
	out, err := ts.mcpCallTool(context.Background(), name, args)
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return out
}

func TestCodeSearch_Exact(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		ts := seedLineRepo(t, s)
		out := lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "Close()", "mode": "exact"})
		want := "app/main.go:4: [run] c.Close()\n  :5: [run] f.Close()\n  :6: [run] c.Close()\napp/main_test.go:5: [TestRun] c.Close()\n"
		if !strings.HasPrefix(out, want) || !strings.HasSuffix(out, "\n8 of 8 lines in 6 files") {
			t.Fatalf("exact:\n%s", out)
		}
		// The limit cuts lines, the count still covers all; path narrows.
		out = lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "close", "mode": "exact", "limit": 2.0, "path": "app/"})
		if out != "app/main.go:4: [run] c.Close()\n  :5: [run] f.Close()\n2 of 4 lines in 2 files, more with limit or a narrower path" {
			t.Fatalf("limit and path:\n%s", out)
		}
		// Part of an identifier and a word the index stems are found too:
		// the documents are read, not the index.
		if out = lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "uspended sess", "mode": "exact", "path": "docs"}); !strings.HasSuffix(out, "\n1 of 1 lines in 1 files") {
			t.Fatalf("substring:\n%s", out)
		}
		out = lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "nothing-like-this", "mode": "exact"})
		if out != "0 of 0 lines in 0 files" {
			t.Fatalf("no match:\n%s", out)
		}
	})
}

func TestCodeSearch_WordsAndMeaning(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		ts := seedLineRepo(t, s)
		out := lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "flush"})
		if out != "file/file.go:5-8  File.Close\n  5: func (f *File) Close() error {\n  6: flush()\n  7: return nil\n  8: }\n1 of 1 hits" {
			t.Fatalf("words:\n%s", out)
		}
		// A hit on a Markdown heading returns the text under it, each line
		// bounded, instead of the heading and the lines above it.
		out = lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "lease suspended decision", "mode": "words"})
		if !strings.HasPrefix(out, "docs/adr.md:5-9\n  5: ## Decision: no lease while suspended\n  7: A suspended session holds no quota lease.") ||
			!strings.Contains(out, cutMark+"\n  9: More text here.\n1 of 1 hits") || len(out) > 400 {
			t.Fatalf("document:\n%s", out)
		}
		// One hit more than shown exists: the count says so.
		out = lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "Close", "limit": 2.0})
		if !strings.HasSuffix(out, "\n2 hits, more with limit") {
			t.Fatalf("more:\n%s", out)
		}
		out = lineTool(t, ts, "code_search", map[string]interface{}{"collection": semColl, "query": "close the connection", "mode": "meaning"})
		if !strings.Contains(out, "hits") {
			t.Fatalf("meaning:\n%s", out)
		}
	})
}

func TestCodeSearch_RequestErrors(t *testing.T) {
	fake := covBNewFake()
	if _, err := covBRun(t, fake, "code_search", map[string]interface{}{"collection": "c"}); !errors.Is(err, errCodeSearchRequest) {
		t.Fatalf("missing query: %v", err)
	}
	if _, err := covBRun(t, fake, "code_search", map[string]interface{}{"collection": "c", "query": "q", "mode": "regex"}); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("unknown mode: %v", err)
	}
	// A search without passages (a client that returns none) is an empty list.
	out, err := covBRun(t, fake, "code_search", map[string]interface{}{"collection": "c", "query": "q"})
	if err != nil || out != "0 of 0 hits" {
		t.Fatalf("no passages: %q %v", out, err)
	}
	// A collection past the scan bound: the count says where the scan
	// stopped instead of calling the list complete.
	page := &MCPSearchResponse{Total: codeSearchScanDocs + 1}
	for range codeSearchPage {
		page.Documents = append(page.Documents, MCPDocument{Key: "a.txt", ContentMD: "needle"})
	}
	fake.searchResp = page
	out, err = covBRun(t, fake, "code_search", map[string]interface{}{"collection": "c", "query": "needle", "mode": "exact", "limit": 1.0})
	if err != nil || !strings.HasSuffix(out, "1 of 5000 lines in 5000 files, more with limit or a narrower path; read 5000 of 5001 files, narrow with path") {
		t.Fatalf("scan bound: %q %v", out, err)
	}
	fake.err = errors.New("index offline")
	for _, mode := range []string{CodeSearchExact, CodeSearchWords, CodeSearchMeaning} {
		if _, err := covBRun(t, fake, "code_search", map[string]interface{}{"collection": "c", "query": "q", "mode": mode}); err == nil || err.Error() != "index offline" {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}

// meaningDownClient fails hybrid search only, as a server without an
// embedding provider does.
type meaningDownClient struct{ *covBFakeClient }

func (meaningDownClient) HybridSearch(context.Context, *MCPHybridSearchRequest) (*MCPHybridSearchResponse, error) {
	return nil, errors.New("no embedding provider")
}

func TestCodeSearch_MeaningFallsBackToWords(t *testing.T) {
	ts := &MCPToolServer{client: meaningDownClient{covBNewFake()}}
	out, err := ts.mcpCallTool(context.Background(), "code_search", map[string]interface{}{"collection": "c", "query": "q", "mode": "meaning"})
	if err != nil || out != "meaning search unavailable (no embedding provider); matched by words\n0 of 0 hits" {
		t.Fatalf("fallback: %q %v", out, err)
	}
}

func TestCodeSymbol_Lines(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		ts := seedLineRepo(t, s)
		out := lineTool(t, ts, "code_symbol", map[string]interface{}{"collection": semColl, "name": "Closer", "format": "lines",
			"include": []interface{}{"definitions", "implementations"}})
		want := "Closer: semantic (gopls/0.24.0)\ndefinitions: 1 of 1\nio/closer.go:3-5  interface  Closer\n" +
			"implementations: 3 of 3\nbuf/buf.go:3-3  struct  Buffer\nfile/file.go:3-3  struct  File\nnet/conn.go:3-3  struct  Conn"
		if out != want {
			t.Fatalf("implementations:\n%s", out)
		}
		// Several definitions: candidates, a target on every use, and the
		// total behind each cut relation.
		out = lineTool(t, ts, "code_symbol", map[string]interface{}{"collection": semColl, "name": "Close", "format": "lines", "limit": 1.0})
		for _, part := range []string{"ambiguous, 1 candidates; pass path to choose one\n  buf/buf.go  method  Buffer.Close\n",
			"definitions: 1 of 3, more with limit\n", "references: 1 of 4, more with limit\napp/main.go:5  in run: f.Close()  -> file/file.go#File.Close\n",
			"implementations: 0 of 0"} {
			if !strings.Contains(out, part) {
				t.Fatalf("missing %q in:\n%s", part, out)
			}
		}
		// Tests left out, text asked for, hits in one file under one path.
		out = lineTool(t, ts, "code_symbol", map[string]interface{}{"collection": semColl, "name": "Conn.Close", "format": "lines",
			"exclude_tests": true, "include_content": true, "include": []interface{}{"definitions", "references", "tests"}})
		want = "Conn.Close: semantic (gopls/0.24.0)\ndefinitions: 1 of 1\nnet/conn.go:5-7  method  Conn.Close\n" +
			"    func (c *Conn) Close() error {\n    \treturn nil\n    }\n" +
			"references: 2 of 2\napp/main.go:4  in run: c.Close()\n  :6  in run: c.Close()\ntests: 0 of 0"
		if out != want {
			t.Fatalf("exclude tests:\n%s", out)
		}
		if _, err := ts.mcpCallTool(context.Background(), "code_symbol", map[string]interface{}{"collection": semColl, "name": "Close", "format": "xml"}); err == nil {
			t.Fatal("unknown format accepted")
		}
	})
}

func TestLookupSymbol_DirectoryPathTotalsAndTests(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		seedSemRepo(t, s)
		// A directory keeps the definitions under it; one file's worth is
		// not ambiguous.
		res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Path: "net/", Include: []string{RelReferences}})
		if res.Ambiguous || len(res.References) != 3 {
			t.Fatalf("directory path: %+v", res)
		}
		// Two definitions in the file the path names: the caller chose it.
		addSemDoc(t, s, "two/two.go", semFile{content: "package two\n\nfunc (A) Open() {}\nfunc (B) Open() {}\n",
			defs: []string{"A.Open method 3-3 A", "B.Open method 4-4 B"}})
		if res = lookup(t, s, SymbolRequest{Collection: semColl, Name: "Open", Path: "two/two.go"}); res.Ambiguous || len(res.Definitions) != 2 {
			t.Fatalf("one file, two definitions: %+v", res)
		}
		res = lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Limit: 1, ExcludeTests: true})
		if !res.Truncated || res.Totals[RelDefinitions] != 3 || res.Totals[RelReferences] != 3 || len(res.Tests) != 0 {
			t.Fatalf("totals: %+v", res.Totals)
		}
		// The GET form reads excludeTests too.
		rec := httptest.NewRecorder()
		s.handleSymbol(rec, httptest.NewRequest(http.MethodGet, "/v1/symbol?collection="+semColl+"&name=Conn.Close&include=references&excludeTests=true", nil))
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "main_test.go") || !strings.Contains(rec.Body.String(), `"path":"app/main.go"`) {
			t.Fatalf("GET excludeTests: %d %s", rec.Code, rec.Body.String())
		}
		// Every passage in a test file goes, and definitions stay a list.
		res = lookup(t, s, SymbolRequest{Collection: semColl, Name: "TestRun", ExcludeTests: true})
		if res.Definitions == nil || len(res.Definitions) != 0 {
			t.Fatalf("definitions in tests: %+v", res.Definitions)
		}
	})
}

func TestSymbolLines_HeuristicAndScanLimit(t *testing.T) {
	req := &SymbolRequest{Include: []string{RelDefinitions, RelReferences}}
	res := &SymbolResult{Symbol: "run", Truncated: true,
		Definitions: []Passage{{Path: "a.go", Lines: PassageLines{Start: 3, End: 9}, Symbol: "run", Source: RelDefinitions}},
		References:  []Passage{{Path: "b.go", Lines: PassageLines{Start: 4, End: 4}, Content: "run()", Source: RelReferences}}}
	want := "run\ndefinitions: 1 of 1\na.go:3-9  run\nreferences: 1 of 1\nb.go:4: run()\nscan limit reached: lists may be incomplete; narrow with path"
	if got := symbolLines(req, res); got != want {
		t.Fatalf("lines:\n%s", got)
	}
	if got := lineText("\ta" + strings.Repeat("é", 100)); len(got) != lineTextMax-1+len(cutMark) || !strings.HasSuffix(got, cutMark) {
		t.Fatalf("lineText cut on a rune: %d %q", len(got), got)
	}
}
