package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
)

// The fakes the --semantic tests run against (CODE-011): the language server
// of internal/lspindex, built once per test binary (CI installs no language
// server and no binary is committed), and an mddbd that keeps documents in a
// map so that a second run sees what the first one wrote.

var (
	fakeLSPOnce sync.Once
	fakeLSPDir  string
	fakeLSPBin  string
	fakeLSPErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if fakeLSPDir != "" {
		_ = os.RemoveAll(fakeLSPDir)
	}
	os.Exit(code)
}

// fakeLSP builds the fake language server once and returns its path.
func fakeLSP(t *testing.T) string {
	t.Helper()
	fakeLSPOnce.Do(func() {
		if fakeLSPDir, fakeLSPErr = os.MkdirTemp("", "autoindex-fakelsp-"); fakeLSPErr != nil {
			return
		}
		fakeLSPBin = filepath.Join(fakeLSPDir, "fakelsp")
		if runtime.GOOS == "windows" {
			fakeLSPBin += ".exe" // Windows runs a program only with its extension
		}
		cmd := exec.Command("go", "build", "-o", fakeLSPBin, "./internal/lspindex/testdata/fakelsp")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeLSPErr = fmt.Errorf("building the fake language server: %v\n%s", err, out)
		}
	})
	if fakeLSPErr != nil {
		t.Fatal(fakeLSPErr)
	}
	return fakeLSPBin
}

// useFakeGopls makes the fake, playing script, the Go server of this test.
func useFakeGopls(t *testing.T, script map[string]any) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.json")
	data, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MDDB_LSP_GO", fakeLSP(t)+" "+path)
}

// storedDoc is a document the fake mddbd holds.
type storedDoc struct {
	Content string              `json:"contentMd"`
	Meta    map[string][]string `json:"meta"`
}

// fakeMddbd is a stateful server: ingest skips unchanged content, as
// skipDuplicates does, and facets read the stored meta. Everything the
// handler touches is behind mu: the test reads it while the server runs.
type fakeMddbd struct {
	*httptest.Server
	mu       sync.Mutex
	docs     map[string]*storedDoc
	langs    string
	fail     map[string]bool // "METHOD path" answered with 500
	requests map[string]int
	ingested []string // keys of documents sent to ingest
	patches  []map[string]any
}

func newFakeMddbd(t *testing.T) *fakeMddbd {
	t.Helper()
	f := &fakeMddbd{docs: map[string]*storedDoc{}, fail: map[string]bool{}, requests: map[string]int{},
		langs: `{"extensions":{".go":"go",".py":"python"},"languages":["go","python"],"semantic":{"go":true,"php":true,"python":true}}`}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// set changes a fixture field while the server runs.
func (f *fakeMddbd) set(change func(f *fakeMddbd)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// read returns what fn computes from the fixture.
func (f *fakeMddbd) read(fn func(f *fakeMddbd) any) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}

func (f *fakeMddbd) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	route := r.Method + " " + r.URL.Path
	f.requests[route]++
	if f.fail[route] {
		http.Error(w, `{"error":"scripted failure"}`, http.StatusInternalServerError)
		return
	}
	var body map[string]json.RawMessage
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	w.Header().Set("Content-Type", "application/json")
	switch route {
	case "GET /v1/code-languages":
		_, _ = io.WriteString(w, f.langs)
	case "POST /v1/ingest":
		f.ingest(w, body)
	case "POST /v1/aggregate":
		f.aggregate(w, body)
	case "POST /v1/get":
		var key string
		_ = json.Unmarshal(body["key"], &key)
		if d, ok := f.docs[key]; ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"key": key, "lang": "en", "contentMd": d.Content, "meta": d.Meta})
			return
		}
		http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
	case "PATCH /v1/update":
		var p struct {
			Key  string              `json:"key"`
			Meta map[string][]string `json:"meta"`
		}
		_ = json.Unmarshal(raw, &p)
		var whole map[string]any
		_ = json.Unmarshal(raw, &whole)
		f.patches = append(f.patches, whole)
		if d, ok := f.docs[p.Key]; ok {
			d.Meta = p.Meta
		}
		_, _ = io.WriteString(w, `{}`)
	default:
		_, _ = io.WriteString(w, `{}`)
	}
}

func (f *fakeMddbd) ingest(w http.ResponseWriter, body map[string]json.RawMessage) {
	var docs []struct {
		Key       string              `json:"key"`
		ContentMD string              `json:"contentMd"`
		Meta      map[string][]string `json:"meta"`
	}
	_ = json.Unmarshal(body["documents"], &docs)
	added, updated, skipped := 0, 0, 0
	for _, d := range docs {
		f.ingested = append(f.ingested, d.Key)
		old, ok := f.docs[d.Key]
		switch {
		case ok && old.Content == d.ContentMD:
			skipped++
			continue
		case ok:
			updated++
		default:
			added++
		}
		f.docs[d.Key] = &storedDoc{Content: d.ContentMD, Meta: d.Meta}
	}
	_ = json.NewEncoder(w).Encode(map[string]int{"added": added, "updated": updated, "skipped": skipped})
}

func (f *fakeMddbd) aggregate(w http.ResponseWriter, body map[string]json.RawMessage) {
	var facets []struct {
		Field string `json:"field"`
	}
	_ = json.Unmarshal(body["facets"], &facets)
	out := map[string][]map[string]any{}
	for _, fc := range facets {
		counts := map[string]int{}
		for _, d := range f.docs {
			for _, v := range d.Meta[fc.Field] {
				counts[v]++
			}
		}
		values := make([]string, 0, len(counts))
		for v := range counts {
			values = append(values, v)
		}
		sort.Strings(values)
		buckets := []map[string]any{}
		for _, v := range values {
			buckets = append(buckets, map[string]any{"value": v, "count": counts[v]})
		}
		out[fc.Field] = buckets
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"facets": out})
}
