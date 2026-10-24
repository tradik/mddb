package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"mddb/internal/fts"
)

// #326: identifiers in a code collection. The code tokeniser indexed
// `do_parse` as `do_parse`, `do` and `parse`; the query went through the
// prose tokeniser and became `pars`. A search for a function by name did not
// rank its definition first, and phrase mode found nothing.

func codeFTS(t *testing.T, s *Server, body map[string]any) FTSSearchResponse {
	t.Helper()
	rec := doRequest(t, s.handleFTS, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp FTSSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func loadCodeFunctions(t *testing.T, s *Server) {
	t.Helper()
	code := map[string][]string{"kind": {"code"}, "language": {"c"}}
	for key, body := range map[string]string{
		"src/core/parse.c#do_parse":    "int\ndo_parse(struct buf *b)\n{\n\treturn (scan(b));\n}",
		"src/core/cmd.c#cmd_parse":     "int\ncmd_parse(char *s)\n{\n\treturn (parse_args(s));\n}",
		"src/core/file.c#fparse":       "int\nfparse(FILE *f)\n{\n\treturn (parse_stream(f));\n}",
		"src/vm/page.c#mem_page_alloc": "void *\nmem_page_alloc(size_t n)\n{\n\treturn (pool_get(n));\n}",
	} {
		addTestDoc(t, s, "code-fn", key, "en", body, code)
	}
}

func TestFTSFindsAFunctionByItsName(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	loadCodeFunctions(t, s)

	for _, algo := range []string{"bm25", "tfidf"} {
		resp := codeFTS(t, s, map[string]any{"collection": "code-fn", "query": "do_parse", "algorithm": algo, "limit": 4})
		if len(resp.Results) == 0 || resp.Results[0].Document.Key != "src/core/parse.c#do_parse" {
			t.Errorf("%s: the definition is not first: %+v", algo, ftsKeys(resp))
			continue
		}
		if !slices.Contains(resp.Results[0].MatchedTerms, "do_parse") {
			t.Errorf("%s: the whole identifier was not matched: %v", algo, resp.Results[0].MatchedTerms)
		}
	}
}

func TestFTSPhraseFindsAnIdentifier(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	loadCodeFunctions(t, s)

	resp := codeFTS(t, s, map[string]any{"collection": "code-fn", "query": `"mem_page_alloc"`, "mode": "phrase", "limit": 4})
	if resp.Total == 0 || resp.Results[0].Document.Key != "src/vm/page.c#mem_page_alloc" {
		t.Errorf("phrase search for an identifier: %+v", ftsKeys(resp))
	}
}

// Documents written one at a time are tokenised as code too: only the bulk
// path passed the document's kind to the index before.
func TestASingleCodeDocumentIsIndexedAsCode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "one", "lib/str.c", "en", "size_t\nstr_len(const char *s)\n{\n\treturn (0);\n}", nil)
	resp := codeFTS(t, s, map[string]any{"collection": "one", "query": "str_len", "algorithm": "bm25"})
	if resp.Total != 1 || !slices.Contains(resp.Results[0].MatchedTerms, "str_len") {
		t.Errorf("a .c document added alone is not searchable by identifier: %+v", resp.Results)
	}
}

// disableStem turned stemming off on the shared index for the length of the
// request, so a document written meanwhile was indexed unstemmed.
func TestDisableStemDoesNotTouchTheSharedIndex(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.FTSIndex.SetStemmer(fts.NewPorterStemmer()) // as startup does
	addTestDoc(t, s, "prose", "a", "en", "running quickly", nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = doRequest(t, s.handleFTS, map[string]any{"collection": "prose", "query": "running", "disableStem": true})
		}()
	}
	wg.Wait()
	if s.FTSIndex.Stemmer() == nil {
		t.Fatal("the shared index lost its stemmer")
	}
	view := s.FTSIndex.QueryView(true, true)
	if view.Stemmer() != nil || view.SynonymManager() != nil || s.FTSIndex.Stemmer() == nil {
		t.Error("QueryView should switch off stemming and synonyms on the copy only")
	}
	if s.FTSIndex.QueryView(false, false) != s.FTSIndex {
		t.Error("a view with nothing switched off should be the index itself")
	}
	// Stemmed search still works for everyone else.
	if resp := codeFTS(t, s, map[string]any{"collection": "prose", "query": "runs"}); resp.Total != 1 {
		t.Errorf("a stemmed query no longer finds the stemmed document: %d", resp.Total)
	}
}

func ftsKeys(resp FTSSearchResponse) []string {
	var out []string
	for _, r := range resp.Results {
		out = append(out, r.Document.Key)
	}
	return out
}
