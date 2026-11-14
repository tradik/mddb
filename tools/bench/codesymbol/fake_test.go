package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The fixture: a.go defines Run (function), Conn.Close and Pipe.Close
// (ambiguous methods) and Closer (interface); b.go uses them and implements
// Closer twice; a_test.go holds a test helper that must not be sampled.
const fixtureTruth = `{
  "server": "gopls/0.24.0",
  "files": {
    "a.go": {
      "defs": [
        {"name": "Run", "kind": "function", "start": 3},
        {"name": "Conn.Close", "kind": "method", "start": 10},
        {"name": "Pipe.Close", "kind": "method", "start": 20},
        {"name": "Closer", "kind": "interface", "start": 30},
        {"name": "Conn", "kind": "struct", "start": 8}
      ],
      "refs": [{"target": "a.go#Run", "lines": [3, 40]}]
    },
    "b.go": {
      "defs": [{"name": "main", "kind": "function", "start": 1}],
      "refs": [
        {"target": "a.go#Run", "lines": [5, 6]},
        {"target": "a.go#Conn.Close", "lines": [7]},
        {"target": "a.go#Closer", "lines": [9]},
        {"target": "gone.go#X", "lines": [1]}
      ],
      "impls": [{"target": "a.go#Closer", "local": "Conn"}, {"target": "a.go#Closer", "local": "Pipe"},
        {"target": "gone.go#Y", "local": "Z"}]
    },
    "a_test.go": {"defs": [{"name": "helper", "kind": "function", "start": 1}]}
  }
}`

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

type ref struct {
	Path   string         `json:"path"`
	Lines  map[string]int `json:"lines"`
	Target string         `json:"target,omitempty"`
}

func refAt(path string, line int, target string) ref {
	return ref{Path: path, Lines: map[string]int{"start": line, "end": line}, Target: target}
}

// fakeMDDB answers like mddbd: "heur" by name only, "sem" from the facts.
// broken makes every endpoint fail; searchDiffers makes "sem" answer
// searches differently.
type fakeMDDB struct {
	broken, searchDiffers, hybridFails bool
	symbolStatus, exportStatus         int
	export                             string
}

func (f *fakeMDDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.broken {
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	switch r.URL.Path {
	case "/v1/symbol":
		f.symbol(w, r)
	case "/v1/export":
		if f.exportStatus != 0 {
			http.Error(w, "nope", f.exportStatus)
			return
		}
		_, _ = w.Write([]byte(f.export))
	case "/v1/fts", "/v1/hybrid-search":
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if f.hybridFails && r.URL.Path == "/v1/hybrid-search" {
			http.Error(w, "no embeddings", http.StatusUnprocessableEntity)
			return
		}
		coll := req["collection"].(string)
		meta := map[string]any{"path": []string{"a.go"}}
		if coll == "sem" {
			meta["symIndex"] = []string{"v1 x y gopls"}
		}
		hit := map[string]any{"collection": coll, "key": "a.go", "meta": meta, "symbol": "Run"}
		if coll == "sem" && req["query"] == "where" {
			hit["symbol"] = "T.Run" // facts qualify the label; the results stay
		}
		score := 1.0
		if f.searchDiffers && coll == "sem" && req["query"] == "differs" {
			score = 2.0
		}
		hit["score"] = score
		_ = json.NewEncoder(w).Encode(map[string]any{"took": 3, "results": []any{hit}})
	}
}

func (f *fakeMDDB) symbol(w http.ResponseWriter, r *http.Request) {
	if f.symbolStatus != 0 {
		http.Error(w, "nope", f.symbolStatus)
		return
	}
	q := r.URL.Query()
	var refs []ref
	var impls []any
	precision := "heuristic"
	switch {
	case q.Get("collection") == "heur" && q.Get("name") == "Run":
		// the definition line, a duplicate, a namesake's use, two true ones
		refs = []ref{refAt("a.go", 3, ""), refAt("b.go", 5, ""), refAt("b.go", 5, ""), refAt("c.go", 1, ""), refAt("b.go", 6, "")}
	case q.Get("collection") == "heur" && q.Get("name") == "Close":
		refs = []ref{refAt("b.go", 7, ""), refAt("b.go", 8, "")}
	case q.Get("name") == "Run":
		precision = "semantic"
		refs = []ref{refAt("b.go", 5, "a.go#Run"), refAt("b.go", 6, "a.go#Run"), refAt("a.go", 40, "a.go#Run")}
	case q.Get("name") == "Close":
		precision = "semantic"
		refs = []ref{refAt("b.go", 7, "a.go#Conn.Close")}
	case q.Get("collection") == "sem" && q.Get("name") == "Closer":
		precision = "semantic"
		refs = []ref{refAt("b.go", 9, "a.go#Closer")}
		impls = []any{map[string]any{"path": "b.go"}, map[string]any{"path": "b.go"}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"references": refs, "implementations": impls,
		"precision": precision, "truncated": q.Get("name") == "Closer"})
}

func newFake(t *testing.T, f *fakeMDDB) string {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv.URL
}

const fixtureExport = `{"key":"a.go","meta":{"symIndex":["v1 a b gopls"],"symDefs":["Run function 3-5"],"path":["a.go"]}}

{"key":"b.go","meta":{"symIndex":["v1 a b gopls truncated"],"symRefs":["` + "a.go#Run 5,6" + `"]}}
{"key":"c.md","meta":{"path":["c.md"]}}
`

func evalFiles(t *testing.T) string {
	docs := writeFile(t, "docs.json", `{"queries":[{"query":"same"},{"query":"differs"}]}`)
	tasks := writeFile(t, "tasks.json", `{"tasks":[{"question":"where","grep":"Run"}]}`)
	return docs + "," + tasks
}

func TestFixtureTruthLoads(t *testing.T) {
	server, all, err := loadTruth(writeFile(t, "t.json", fixtureTruth))
	if err != nil || server != "gopls/0.24.0" || len(all) != 7 {
		t.Fatalf("%v %s %d", err, server, len(all))
	}
}
