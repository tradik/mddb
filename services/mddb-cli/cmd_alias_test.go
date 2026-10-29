package main

import (
	"strings"
	"testing"
	"time"
)

func TestAlias_ListSetDelete(t *testing.T) {
	srv := newFakeServer(t, map[string]interface{}{
		"GET /v1/aliases":           `{"aliases":[{"alias":"docs","collection":"docs-v1"}]}`,
		"PUT /v1/aliases/docs":      `{"alias":"docs","collection":"docs-v2"}`,
		"DELETE /v1/aliases/docs":   `{"deleted":true}`,
		"DELETE /v1/aliases/absent": failure{404, `{"error":"alias not found"}`},
	})
	if out, err := runCLI(t, srv.URL, "alias", "list"); err != nil || !strings.Contains(out, "docs -> docs-v1") {
		t.Fatalf("list: %v %s", err, out)
	}
	if out, err := runCLI(t, srv.URL, "alias", "set", "docs", "docs-v2"); err != nil || !strings.Contains(out, "docs -> docs-v2") {
		t.Fatalf("set: %v %s", err, out)
	}
	if srv.lastCall(t).Body["collection"] != "docs-v2" {
		t.Fatal("set body")
	}
	if out, err := runCLI(t, srv.URL, "alias", "delete", "docs"); err != nil || !strings.Contains(out, "Deleted alias docs") {
		t.Fatalf("delete: %v %s", err, out)
	}
	if _, err := runCLI(t, srv.URL, "alias", "delete", "absent"); err == nil {
		t.Fatal("delete missing")
	}
	empty := newFakeServer(t, map[string]interface{}{"GET /v1/aliases": `{"aliases":[]}`})
	if out, _ := runCLI(t, empty.URL, "alias", "list"); !strings.Contains(out, "No aliases.") {
		t.Fatalf("empty: %s", out)
	}
	broken := newFakeServer(t, map[string]interface{}{"/v1/aliases": failure{500, `{"error":"x"}`},
		"/v1/aliases/docs": failure{400, `{"error":"x"}`}})
	for _, args := range [][]string{{"alias", "list"}, {"alias", "set", "docs", "x"}} {
		if _, err := runCLI(t, broken.URL, args...); err == nil {
			t.Fatalf("%v: no error", args)
		}
	}
}

const swapExport = `{"key":"a","lang":"en","meta":{"v":["1"]},"contentMd":"one"}
{"key":"b","lang":"en","contentMd":"two","expiresAt":9999999999}
`

func swapRoutes(intoConfigured bool) map[string]interface{} {
	cfg := `{"collection":"x","config":{"type":"documents","description":"d"},"configured":true}`
	if !intoConfigured {
		cfg = `{"collection":"x","config":{"type":"default"},"configured":false}`
	}
	return map[string]interface{}{
		"GET /v1/aliases":           `{"aliases":[{"alias":"docs","collection":"docs-v1"}]}`,
		"PUT /v1/aliases/docs":      `{"alias":"docs","collection":"docs-v2"}`,
		"GET /v1/collection-config": cfg,
		"PUT /v1/collection-config": `{"status":"ok"}`,
		"/v1/export":                swapExport,
		"/v1/ingest":                `{"added":2,"failed":0}`,
		"/v1/indexing-status":       `{"collection":"docs-v2","indexed":true,"progress":1}`,
	}
}

func TestReindexSwap(t *testing.T) {
	swapPoll = time.Millisecond
	srv := newFakeServer(t, swapRoutes(true))
	out, err := runCLI(t, srv.URL, "reindex-swap", "docs", "--into", "docs-v2")
	if err != nil || !strings.Contains(out, "Copied 2 documents") || !strings.Contains(out, "docs -> docs-v2 (was docs-v1") {
		t.Fatalf("swap: %v %s", err, out)
	}
	var ingested, puts int
	for _, c := range srv.calls() {
		switch {
		case c.Path == "/v1/ingest":
			ingested++
			docs := c.Body["documents"].([]interface{})
			if len(docs) != 2 || docs[1].(map[string]interface{})["ttl"] == nil {
				t.Fatalf("ingest: %+v", c.Body)
			}
		case c.Method == "PUT" && c.Path == "/v1/collection-config":
			puts++
		}
	}
	if ingested != 1 || puts != 0 {
		t.Fatalf("ingests %d, config copies %d (into had its own config)", ingested, puts)
	}

	// Without a config of its own, into gets the old collection's.
	routes := swapRoutes(false)
	routes["GET /v1/collection-config?collection=docs-v1"] = `{"config":{"type":"documents"},"configured":true}`
	srv = newFakeServer(t, routes)
	if _, err := runCLI(t, srv.URL, "reindex-swap", "docs", "--into", "docs-v2"); err != nil {
		t.Fatal(err)
	}
	copied := false
	for _, c := range srv.calls() {
		if c.Method == "PUT" && c.Path == "/v1/collection-config" && c.Body["collection"] == "docs-v2" {
			copied = true
		}
	}
	if !copied {
		t.Fatal("config not copied")
	}
}

func TestReindexSwap_Failures(t *testing.T) {
	swapPoll = time.Millisecond
	cases := map[string]struct {
		args  []string
		patch map[string]interface{}
		want  string
	}{
		"not an alias":    {[]string{"other", "--into", "x"}, nil, "not an alias"},
		"same collection": {[]string{"docs", "--into", "docs-v1"}, nil, "already points"},
		"ingest failures": {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"/v1/ingest": `{"added":1,"failed":1}`}, "failed to copy"},
		"bad export":      {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"/v1/export": "not json\n"}, "reading the export"},
		"embeddings fail": {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"/v1/indexing-status": `{"indexed":false,"failedEmbeddings":2}`}, "embeddings failed"},
		"times out":       {[]string{"docs", "--into", "docs-v2", "--timeout", "1ms"}, map[string]interface{}{"/v1/indexing-status": `{"indexed":false,"progress":0.5}`}, "not indexed yet"},
		"aliases error":   {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"GET /v1/aliases": failure{500, "{}"}}, "500"},
		"status error":    {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"/v1/indexing-status": failure{500, "{}"}}, "500"},
		"export error":    {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"/v1/export": failure{500, "{}"}}, "500"},
		"ingest error":    {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"/v1/ingest": failure{500, "{}"}}, "500"},
		"set alias error": {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"PUT /v1/aliases/docs": failure{400, "{}"}}, "400"},
		"config error":    {[]string{"docs", "--into", "docs-v2"}, map[string]interface{}{"GET /v1/collection-config": failure{500, "{}"}}, "500"},
	}
	for name, c := range cases {
		routes := swapRoutes(true)
		for k, v := range c.patch {
			routes[k] = v
		}
		srv := newFakeServer(t, routes)
		if _, err := runCLI(t, srv.URL, append([]string{"reindex-swap"}, c.args...)...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A config copy whose read fails, does not parse, or whose write fails.
	for name, patch := range map[string]map[string]interface{}{
		"write": {"GET /v1/collection-config?collection=docs-v1": `{"config":{},"configured":true}`,
			"PUT /v1/collection-config": failure{400, `{"error":"bad"}`}},
		"read":  {"GET /v1/collection-config?collection=docs-v1": failure{500, "{}"}},
		"parse": {"GET /v1/collection-config?collection=docs-v1": `[1]`},
	} {
		routes := swapRoutes(false)
		for k, v := range patch {
			routes[k] = v
		}
		srv := newFakeServer(t, routes)
		if _, err := runCLI(t, srv.URL, "reindex-swap", "docs", "--into", "docs-v2"); err == nil {
			t.Errorf("config %s error passed", name)
		}
	}
}

func TestReindexSwap_BatchesAndLongLines(t *testing.T) {
	swapPoll = time.Millisecond
	defer func(b, l int) { swapBatch, swapMaxLine = b, l }(swapBatch, swapMaxLine)
	swapBatch = 1
	srv := newFakeServer(t, swapRoutes(true))
	if out, err := runCLI(t, srv.URL, "reindex-swap", "docs", "--into", "docs-v2"); err != nil || !strings.Contains(out, "Copied 2") {
		t.Fatalf("batches of one: %v %s", err, out)
	}
	ingests := 0
	for _, c := range srv.calls() {
		if c.Path == "/v1/ingest" {
			ingests++
		}
	}
	if ingests != 2 {
		t.Fatalf("%d ingest calls, want 2", ingests)
	}
	routes := swapRoutes(true)
	routes["/v1/ingest"] = failure{500, "{}"}
	if _, err := runCLI(t, newFakeServer(t, routes).URL, "reindex-swap", "docs", "--into", "docs-v2"); err == nil {
		t.Fatal("a failing batch inside the export stops the copy")
	}
	swapBatch, swapMaxLine = 100, 16
	if _, err := runCLI(t, newFakeServer(t, swapRoutes(true)).URL, "reindex-swap", "docs", "--into", "docs-v2"); err == nil {
		t.Fatal("a line longer than the limit is an error")
	}
}
