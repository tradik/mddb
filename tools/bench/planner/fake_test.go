package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"mddb-bench/internal/benchkit"
)

// fakeMDDB answers the calls the bench makes. It searches exactly over the
// corpus vectors of what was added (rebuilt from the collection's size, as
// the corpus is deterministic) and plans "auto" the way the server does at
// small sizes: flat, with a prefilter when filtered.
type fakeMDDB struct {
	cfg  config
	mu   sync.Mutex
	docs map[string][]int // collection -> added document numbers
	rare map[string]map[int]bool

	pendingPolls int      // indexing-status answers with work pending before it drains
	fail         string   // "add", "addFailed", "addJSON", "status", "statusFailed", "pending", "search:<algo>", "searchJSON"
	searched     []search // every search, in order
	deleted      []string // collections deleted
	corpora      map[int]*corpus
}

func newFake(cfg config) *fakeMDDB {
	return &fakeMDDB{cfg: cfg, docs: map[string][]int{}, rare: map[string]map[int]bool{}, corpora: map[int]*corpus{}}
}

func (f *fakeMDDB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	switch r.URL.Path {
	case "/v1/delete-collection":
		f.deleted = append(f.deleted, req["collection"].(string))
		delete(f.docs, req["collection"].(string))
		_, _ = w.Write([]byte("{}"))
	case "/v1/add-batch":
		f.addBatch(w, req)
	case "/v1/indexing-status":
		f.status(w)
	case "/v1/vector-search":
		f.search(w, req)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeMDDB) addBatch(w http.ResponseWriter, req map[string]any) {
	switch f.fail {
	case "add":
		http.Error(w, "read-only", http.StatusForbidden)
		return
	case "addFailed":
		_, _ = w.Write([]byte(`{"failed": 1}`))
		return
	case "addJSON":
		_, _ = w.Write([]byte("{"))
		return
	}
	coll := req["collection"].(string)
	if f.rare[coll] == nil {
		f.rare[coll] = map[int]bool{}
	}
	for _, d := range req["documents"].([]any) {
		doc := d.(map[string]any)
		i := keyDoc(doc["key"].(string))
		f.docs[coll] = append(f.docs[coll], i)
		if doc["meta"].(map[string]any)["slice"].([]any)[0] == "rare" {
			f.rare[coll][i] = true
		}
	}
	_, _ = w.Write([]byte(`{"added": 1}`))
}

func (f *fakeMDDB) status(w http.ResponseWriter) {
	switch {
	case f.fail == "status":
		_, _ = w.Write([]byte("{"))
	case f.fail == "statusFailed":
		_, _ = w.Write([]byte(`{"pendingEmbeddings": 0, "failedEmbeddings": 2}`))
	case f.fail == "pending" || f.pendingPolls > 0:
		f.pendingPolls--
		_, _ = w.Write([]byte(`{"pendingEmbeddings": 5}`))
	default:
		_, _ = w.Write([]byte(`{"pendingEmbeddings": 0}`))
	}
}

func (f *fakeMDDB) search(w http.ResponseWriter, req map[string]any) {
	algo := req["algorithm"].(string)
	_, filtered := req["filterMeta"]
	f.searched = append(f.searched, search{algo, filtered})
	if f.fail == "search:"+algo {
		http.Error(w, "index unavailable", http.StatusServiceUnavailable)
		return
	}
	if f.fail == "searchJSON" {
		_, _ = w.Write([]byte("{"))
		return
	}
	coll := req["collection"].(string)
	q := make([]float32, f.cfg.Dims)
	for i, x := range req["queryVector"].([]any) {
		q[i] = float32(x.(float64))
	}
	c := f.corpus(len(f.docs[coll]))
	added := map[int]bool{}
	for _, i := range f.docs[coll] {
		added[i] = true
	}
	allow := func(i int) bool { return added[i] && (!filtered || f.rare[coll][i]) }
	plan := map[string]any{"algorithm": algo}
	if algo == "auto" {
		plan = map[string]any{"algorithm": "flat", "prefilter": filtered}
	}
	var results []map[string]any
	for _, i := range benchkit.Nearest(c.docs, q, 10, allow) {
		results = append(results, map[string]any{"document": map[string]any{"key": docKey(i)}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results, "plan": plan})
}

// corpus is the bench's corpus for a collection of n documents.
func (f *fakeMDDB) corpus(n int) *corpus {
	if f.corpora[n] == nil {
		f.corpora[n] = newCorpus(f.cfg, n)
	}
	return f.corpora[n]
}

func smallConfig(url string) config {
	return config{URL: url, Collection: "c", Seed: 1, Dims: 8, Topics: 4, Queries: 6, Warmup: 2,
		Sizes: []int{40, 82}, Batch: 7, Parallel: 3, RareEvery: 10, Timeout: 5 * time.Second,
		Indexes: []string{"flat", "hnsw", "ivf"}, PollPeriod: time.Millisecond}
}

// rowFor finds a report row by its first three cells.
func rowFor(report string, cells ...string) string {
	prefix := "| " + strings.Join(cells, " | ") + " |"
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return fmt.Sprintf("no row %q in:\n%s", prefix, report)
}
