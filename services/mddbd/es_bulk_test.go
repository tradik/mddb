package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// INT-019: _bulk writes runs through the batch path, keeps operation order,
// reports every item, and refuses an unsupported action before writing.

// esBulkItems returns each item's action body, in order.
func esBulkItems(out map[string]any) []map[string]any {
	var items []map[string]any
	for _, it := range out["items"].([]any) {
		for _, v := range it.(map[string]any) {
			items = append(items, v.(map[string]any))
		}
	}
	return items
}

func esBulkDo(t *testing.T, h http.Handler, path string, lines ...string) (int, map[string]any) {
	t.Helper()
	rec, out := esDo(t, h, http.MethodPost, path, strings.Join(lines, "\n")+"\n", "Content-Type", esNDJSON)
	return rec.Code, out
}

func TestESBulkKeepsOrderAndReportsEachItem(t *testing.T) {
	s, h := esTestServer(t)
	if err := s.SchemaManager.Set("books", `{"required":["genre"]}`); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "books", "taken", "en", "exists", map[string][]string{"genre": {"x"}})
	code, out := esBulkDo(t, h, "/es/books/_bulk",
		`{"index":{"_id":"a"}}`, `{"content":"first","genre":"g"}`,
		`{"index":{"_id":"A"}}`, `{"content":"second","genre":"g"}`, // same document: a new run
		`{"create":{"_id":"taken"}}`, `{"content":"no","genre":"g"}`,
		`{"index":{"_id":"bad"}}`, `{"content":"no genre"}`,
		`{"index":{"_id":"nested"}}`, `{"content":"x","m":{"a":1}}`,
		`{"index":{}}`, `{"content":"auto id","genre":"g"}`,
		`{"index":{"_index":"other","_id":"o"}}`, `{"content":"other collection","lang":"de"}`,
		`{"delete":{"_id":"a"}}`,
		`{"delete":{"_id":"missing"}}`,
	)
	if code != http.StatusOK || out["errors"] != true {
		t.Fatalf("bulk: %d %v", code, out)
	}
	items := esBulkItems(out)
	want := []float64{201, 200, 409, 400, 400, 201, 201, 200, 404}
	for i, it := range items {
		if it["status"] != want[i] {
			t.Errorf("item %d: %v, want status %v", i, it, want[i])
		}
	}
	if reason := items[3]["error"].(map[string]any)["reason"].(string); !strings.Contains(reason, "genre") {
		t.Fatalf("a schema refusal names the field: %q", reason)
	}
	if _, err := s.loadDocByRef("other", "o", "de"); err != nil {
		t.Fatalf("other collection: %v", err)
	}
	if _, err := s.loadDocByRef("books", "a", "en"); err == nil {
		t.Fatal("the delete after the writes must win")
	}
}

func TestESBulkWholeRequestRefusals(t *testing.T) {
	_, h := esTestServer(t)
	for name, c := range map[string]struct {
		path  string
		lines []string
		want  string
	}{
		"update":       {"/es/_bulk", []string{`{"update":{"_index":"b","_id":"1"}}`, `{"doc":{}}`}, "[bulk.update]"},
		"bad action":   {"/es/_bulk", []string{`[]`}, "[bulk]"},
		"meta":         {"/es/_bulk", []string{`{"index":[]}`}, "[bulk.index]"},
		"routing":      {"/es/_bulk", []string{`{"index":{"_index":"b","routing":"x"}}`, `{}`}, "[bulk.index.routing]"},
		"bad id":       {"/es/_bulk", []string{`{"index":{"_index":"b","_id":1}}`, `{}`}, "[bulk.index._id]"},
		"no index":     {"/es/_bulk", []string{`{"index":{"_id":"1"}}`, `{}`}, "[bulk.index._index]"},
		"system index": {"/es/_bulk", []string{`{"index":{"_index":"_x"}}`, `{}`}, "[bulk.index._index]"},
		"delete id":    {"/es/books/_bulk", []string{`{"delete":{}}`}, "[bulk.delete._id]"},
		"no source":    {"/es/books/_bulk", []string{`{"index":{}}`}, "[bulk.index]"},
		"param":        {"/es/books/_bulk?pipeline=p", []string{`{"index":{}}`, `{}`}, "[pipeline]"},
	} {
		code, out := esBulkDo(t, h, c.path, c.lines...)
		reason, _ := out["error"].(map[string]any)["reason"].(string)
		if code != http.StatusBadRequest || !strings.Contains(reason, c.want) {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}

	lines := make([]string, 0, esBulkMaxOps+1)
	for i := 0; i <= esBulkMaxOps; i++ {
		lines = append(lines, `{"delete":{"_id":"x"}}`)
	}
	if code, out := esBulkDo(t, h, "/es/books/_bulk", lines...); code != http.StatusBadRequest || !strings.Contains(out["error"].(map[string]any)["reason"].(string), "operations") {
		t.Fatalf("too many operations: %d", code)
	}
}

func TestESBulkWholeBatchRefusal(t *testing.T) {
	s, h := esTestServer(t)
	addTestDoc(t, s, "pack", "a", "en", "x", nil)
	if err := s.CollectionManager.put("pack", &CollectionConfig{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	code, out := esBulkDo(t, h, "/es/pack/_bulk", `{"index":{"_id":"b"}}`, `{"content":"x"}`, `{"index":{"_id":"c"}}`, `{"content":"y"}`)
	if code != http.StatusOK || out["errors"] != true {
		t.Fatalf("read-only bulk: %d %v", code, out)
	}
	for _, it := range esBulkItems(out) {
		if it["status"] != float64(403) || it["error"].(map[string]any)["type"] != "cluster_block_exception" {
			t.Fatalf("item %v", it)
		}
	}

	// A cancelled request: every document of the run fails, none is written.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &esBulkRun{index: "books", lang: "en", ops: []int{0}}
	ops := []esBulkOp{{Action: "index", Index: "books", ID: "z", Doc: &esDocument{Content: "x"}}}
	items := make([]map[string]any, 1)
	s.esBulkWrite(ctx, run, ops, items)
	if items[0]["index"].(map[string]any)["status"] != http.StatusBadRequest {
		t.Fatalf("cancelled run: %v", items[0])
	}
}

func TestESBulkPermissions(t *testing.T) {
	s, _ := esTestServer(t)
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s.AuthManager = am
	h := esHandler(t, s, true)
	if _, err := am.CreateUser("writer", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := am.SetPermission(&Permission{Username: "writer", Collection: "mine", Read: true, Write: true}); err != nil {
		t.Fatal(err)
	}
	key, err := am.CreateAPIKey("writer", "es", 0)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"index":{"_index":"mine","_id":"1"}}` + "\n" + `{"content":"x"}` + "\n" +
		`{"index":{"_index":"theirs","_id":"1"}}` + "\n" + `{"content":"x"}` + "\n"

	rec, out := esDo(t, h, http.MethodPost, "/es/_bulk", body, "Content-Type", esNDJSON, "Authorization", "ApiKey "+key)
	if rec.Code != http.StatusOK {
		t.Fatalf("ApiKey on /es: %d %s", rec.Code, rec.Body.String())
	}
	items := esBulkItems(out)
	if items[0]["status"] != float64(201) || items[1]["status"] != float64(403) {
		t.Fatalf("per-index permissions: %v", items)
	}
	if rec, _ = esDo(t, h, http.MethodPost, "/es/theirs/_bulk", body, "Content-Type", esNDJSON, "Authorization", "ApiKey "+key); rec.Code != http.StatusForbidden {
		t.Fatalf("no write permission on the URL's index: %d", rec.Code)
	}
	if rec, _ = esDo(t, h, http.MethodGet, "/es/mine/_doc/1", "", "Authorization", "Bearer "+key); rec.Code != http.StatusOK {
		t.Fatalf("Bearer key on /es: %d", rec.Code)
	}
	if rec, _ = esDo(t, h, http.MethodGet, "/es/mine/_doc/1", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credentials: %d", rec.Code)
	}
	// /v1 keeps exactly the credentials it accepted before.
	req := httptest.NewRequest(http.MethodGet, "/v1/stats", nil)
	req.Header.Set("Authorization", "ApiKey "+key)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ApiKey scheme on /v1: %d", rec.Code)
	}
}
