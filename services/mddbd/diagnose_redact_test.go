package main

import (
	"reflect"
	"testing"
)

// OPS-025: the bundle's redaction rules, one at a time.

func TestBundleNamesHashOrKeep(t *testing.T) {
	hashed := newBundleNames([]string{"docs", ""}, false)
	if hashed.name("docs") != collectionHash("docs") || hashed.name("unseen") != collectionHash("unseen") {
		t.Error("hashing")
	}
	if len(collectionHash("docs")) != len("c_")+8 {
		t.Errorf("hash %q", collectionHash("docs"))
	}
	kept := newBundleNames([]string{"docs"}, true)
	if kept.name("docs") != "docs" || kept.name("unseen") != "unseen" {
		t.Error("keepNames")
	}
}

func TestScrubJSONKeepsOnlyNamedStrings(t *testing.T) {
	b := newBundleNames([]string{"docs"}, false)
	h := collectionHash("docs")
	in := map[string]any{
		"docs":        map[string]any{"total": 3.0, "mode": "wr"},
		"collections": []any{map[string]any{"name": "docs", "description": "secret"}},
		"path":        "/home/user/db",
		"ok":          true,
	}
	want := map[string]any{
		h:             map[string]any{"total": 3.0, "mode": "wr"},
		"collections": []any{map[string]any{"name": h, "description": redacted}},
		"path":        redacted,
		"ok":          true,
	}
	if got := b.scrubJSON("", in); !reflect.DeepEqual(got, want) {
		t.Errorf("scrubJSON = %#v", got)
	}
}

func TestScrubMetricsLabels(t *testing.T) {
	b := newBundleNames([]string{"docs"}, false)
	in := `mddb_documents_total{collection="docs"} 1
mddb_http_requests_total{method="GET",path="/v1/auth/users/alice",status="200"} 1
mddb_http_requests_total{method="BREW",path="/v1/fts",status="200"} 1
mddb_operations_total{operation="autocomplete",label="Patient Name"} 1
mddb_operations_total{operation="fts_search",label="simple/bm25"} 1
x_bucket{le="+Inf"} 2
x{label=""} 1
`
	want := `mddb_documents_total{collection="` + collectionHash("docs") + `"} 1
mddb_http_requests_total{method="GET",path="/v1/auth/*",status="200"} 1
mddb_http_requests_total{method="<redacted>",path="/v1/fts",status="200"} 1
mddb_operations_total{operation="autocomplete",label="<redacted>"} 1
mddb_operations_total{operation="fts_search",label="simple/bm25"} 1
x_bucket{le="+Inf"} 2
x{label=""} 1
`
	if got := b.scrubMetrics(in); got != want {
		t.Errorf("scrubMetrics:\n%s\nwant\n%s", got, want)
	}
}

func TestEndpointOf(t *testing.T) {
	for path, want := range map[string]string{
		"/v1/fts": "/v1/fts", "/v1/aliases/": "/v1/aliases/*", "/v1/vector-reindex/jobs/abc": "/v1/vector-reindex/*",
		"/health": "/health", "/metrics": "/metrics", "/other": "/other", "/home/user": "/other",
	} {
		if got := endpointOf(path); got != want {
			t.Errorf("endpointOf(%q) = %q, want %q", path, got, want)
		}
	}
}
