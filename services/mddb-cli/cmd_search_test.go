package main

import (
	"net/http"
	"strings"
	"testing"
)

// TEST-001. Search commands. The pattern that matters here is that each one
// sends the flags the user gave and prints a result set a person can read.

func TestSearchSendsFiltersAndPaging(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/search": []map[string]interface{}{
			{"id": "blog|a|en", "key": "a", "lang": "en", "updatedAt": 1700000000,
				"meta": map[string][]string{"tag": {"go"}}},
			{"id": "blog|b|en", "key": "b", "lang": "en", "updatedAt": 1700000001},
		},
	})

	out, err := runCLI(t, fs.URL, "search", "blog",
		"--filter", "tag=go", "--limit", "10", "--offset", "20", "--sort", "addedAt", "--asc")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	call := fs.lastCall(t)
	assertBodyField(t, call.Body, "collection", "blog")
	assertBodyField(t, call.Body, "filterMeta", map[string][]string{"tag": {"go"}})
	assertBodyField(t, call.Body, "limit", 10)
	assertBodyField(t, call.Body, "offset", 20)
	assertBodyField(t, call.Body, "sort", "addedAt")
	assertBodyField(t, call.Body, "asc", true)

	mustContain(t, out, "Found 2 documents")
	mustContain(t, out, "blog|a|en")
	mustContain(t, out, "tag=")
}

// An empty result set is a normal answer, not an error, and must say so
// clearly rather than printing nothing.
func TestSearchWithNoMatchesSaysZero(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/search": []map[string]interface{}{}})

	out, err := runCLI(t, fs.URL, "search", "blog")
	if err != nil {
		t.Fatalf("an empty result set was reported as an error: %v", err)
	}
	mustContain(t, out, "Found 0 documents")
}

// --json is what scripts consume; it must be the server's answer verbatim,
// with no decoration around it.
func TestSearchJSONOutputIsTheRawResponse(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/search": `[{"id":"blog|a|en","key":"a"}]`,
	})

	out, err := runCLI(t, fs.URL, "search", "blog", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != `[{"id":"blog|a|en","key":"a"}]` {
		t.Errorf("--json printed %q, want the response unchanged", out)
	}
	if strings.Contains(out, "Found") {
		t.Error("--json output carries human decoration a parser would choke on")
	}
}

func TestFTSRequiresAQuery(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/fts": map[string]interface{}{"results": []interface{}{}},
	})

	if _, err := runCLI(t, fs.URL, "fts", "blog"); err == nil {
		t.Fatal("fts ran without --query")
	}
	if calls := fs.calls(); len(calls) != 0 {
		t.Errorf("a query-less fts reached the server: %v", calls)
	}
}

func TestFTSSendsTheQuery(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		// The server nests the document inside each result
		// (FTSResultWithDoc); a flat shape here would let the CLI's reader
		// drift from what the server sends.
		"/v1/fts": map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"document":     map[string]interface{}{"key": "post", "lang": "en"},
					"score":        0.87,
					"matchedTerms": []string{"kubernetes"},
				},
			},
			"total": 1,
		},
	})

	out, err := runCLI(t, fs.URL, "fts", "blog", "--query", "kubernetes", "--limit", "5")
	if err != nil {
		t.Fatalf("fts failed: %v", err)
	}

	call := fs.lastCall(t)
	if call.Method != http.MethodPost || call.Path != "/v1/fts" {
		t.Errorf("called %s %s", call.Method, call.Path)
	}
	assertBodyField(t, call.Body, "query", "kubernetes")
	assertBodyField(t, call.Body, "limit", 5)

	mustContain(t, out, "post")
	mustContain(t, out, "1 matches")
}

func TestVectorSearchRequiresAQuery(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/vector-search": map[string]interface{}{}})

	if _, err := runCLI(t, fs.URL, "vector-search", "blog"); err == nil {
		t.Fatal("vector-search ran without --query")
	}
}

func TestVectorSearchSendsItsTuning(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/vector-search": map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"document": map[string]interface{}{"key": "post", "lang": "en"},
					"score":    0.91,
					"rank":     1,
				},
			},
			"total": 1,
		},
	})

	if _, err := runCLI(t, fs.URL, "vector-search", "blog",
		"--query", "how do I shard", "--top-k", "3", "--threshold", "0.5",
		"--filter", "kind=doc"); err != nil {
		t.Fatalf("vector-search failed: %v", err)
	}

	body := fs.lastCall(t).Body
	assertBodyField(t, body, "query", "how do I shard")
	assertBodyField(t, body, "topK", 3)
	assertBodyField(t, body, "threshold", 0.5)
	assertBodyField(t, body, "filterMeta", map[string][]string{"kind": {"doc"}})
}

// A search against a server that refuses must exit non-zero; a script that
// pipes the output would otherwise treat "no results" and "no permission" the
// same way.
func TestSearchFailuresExitNonZero(t *testing.T) {
	for name, route := range map[string]interface{}{
		"forbidden":   failure{http.StatusForbidden, `{"error":"forbidden"}`},
		"server down": failure{http.StatusInternalServerError, `{"error":"boom"}`},
		"bad json":    `[{"id": tru`,
	} {
		t.Run(name, func(t *testing.T) {
			fs := newFakeServer(t, map[string]interface{}{"/v1/search": route})
			if _, err := runCLI(t, fs.URL, "search", "blog"); err == nil {
				t.Errorf("%s was reported as success", name)
			}
		})
	}
}
