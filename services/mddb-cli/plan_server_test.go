package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// planServer keeps documents the way the server does for the calls the plan
// commands make: add (with expectedRevision and ifAbsent), get, a meta
// search sorted by key, and an order facet. Every write gets a new revision,
// so a conditional write over an old one loses with the server's 409.
type planServer struct {
	*httptest.Server
	mu   sync.Mutex
	docs map[string]mddb.Document
	rev  int
	// fail answers a path with a 500.
	fail map[string]bool
	// maxLimit caps a search's limit, as the server's cap does.
	maxLimit int
	// conflictAlways makes every conditional write lose.
	conflictAlways bool
	// searchBarrier, when set, holds that many searches until all of them
	// have arrived, so concurrent callers read the same state.
	searchBarrier *sync.WaitGroup
	barrierLeft   int
	// failKeyPrefix makes adds of keys with this prefix answer 500.
	failKeyPrefix string
	// hideSearch makes every search answer with no documents.
	hideSearch bool
	// searches counts the search calls, to show what a command read.
	searches int
}

func newPlanServer(t *testing.T) *planServer {
	t.Helper()
	s := &planServer{docs: map[string]mddb.Document{}, fail: map[string]bool{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// hold makes the next n searches wait for one another.
func (s *planServer) hold(n int) {
	s.searchBarrier = &sync.WaitGroup{}
	s.searchBarrier.Add(n)
	s.barrierLeft = n
}

func (s *planServer) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/v1/search" {
		s.waitAtBarrier()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail[r.URL.Path] {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
		return
	}
	switch r.URL.Path {
	case "/v1/add":
		var req mddb.AddRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.add(w, req)
	case "/v1/get":
		var req mddb.GetRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		d, ok := s.docs[req.Collection+"|"+req.Key+"|"+req.Lang]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	case "/v1/search":
		var req mddb.SearchRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.searches++
		_ = json.NewEncoder(w).Encode(s.search(req))
	case "/v1/aggregate":
		var req mddb.AggregateRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(s.aggregate(req))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *planServer) waitAtBarrier() {
	s.mu.Lock()
	b := s.searchBarrier
	if b != nil && s.barrierLeft > 0 {
		s.barrierLeft--
		s.mu.Unlock()
		b.Done()
		b.Wait()
		return
	}
	s.mu.Unlock()
}

func (s *planServer) add(w http.ResponseWriter, req mddb.AddRequest) {
	if s.failKeyPrefix != "" && strings.HasPrefix(req.Key, s.failKeyPrefix) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
		return
	}
	id := req.Collection + "|" + req.Key + "|" + req.Lang
	have, exists := s.docs[id]
	lost := (req.IfAbsent && exists) ||
		(req.ExpectedRevision != "" && (s.conflictAlways || !exists || have.Revision != req.ExpectedRevision))
	if lost {
		w.WriteHeader(http.StatusConflict)
		_, _ = fmt.Fprintf(w, `{"error":"revision conflict","expectedRevision":%q,"currentRevision":%q}`, req.ExpectedRevision, have.Revision)
		return
	}
	s.rev++
	d := mddb.Document{ID: id, Key: req.Key, Lang: req.Lang, Meta: req.Meta, ContentMD: req.ContentMD, Revision: fmt.Sprintf("r%d", s.rev)}
	s.docs[id] = d
	_ = json.NewEncoder(w).Encode(d)
}

// matching is the collection's documents that pass the filter, by key.
func (s *planServer) matching(collection string, filter map[string][]string) []mddb.Document {
	var out []mddb.Document
	for _, d := range s.docs {
		if !strings.HasPrefix(d.ID, collection+"|") || !metaMatches(d.Meta, filter) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// metaMatches is the server's filterMeta: AND over keys, OR over values.
func metaMatches(meta, filter map[string][]string) bool {
	for k, want := range filter {
		found := false
		for _, w := range want {
			for _, v := range meta[k] {
				found = found || v == w
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *planServer) search(req mddb.SearchRequest) []mddb.Document {
	docs := s.matching(req.Collection, req.FilterMeta)
	if s.hideSearch {
		return []mddb.Document{}
	}
	limit := req.Limit
	if s.maxLimit > 0 && limit > s.maxLimit {
		limit = s.maxLimit
	}
	start := min(req.Offset, len(docs))
	return docs[start:min(start+limit, len(docs))]
}

func (s *planServer) aggregate(req mddb.AggregateRequest) mddb.AggregateResponse {
	resp := mddb.AggregateResponse{Facets: map[string][]mddb.FacetBucket{}}
	for _, f := range req.Facets {
		counts := map[string]int{}
		for _, d := range s.matching(req.Collection, req.FilterMeta) {
			for _, v := range d.Meta[f.Field] {
				counts[v]++
			}
		}
		for v, c := range counts {
			resp.Facets[f.Field] = append(resp.Facets[f.Field], mddb.FacetBucket{Value: v, Count: c})
		}
	}
	return resp
}

// doc returns a stored document by key in collection c, language en.
func (s *planServer) doc(key string) (mddb.Document, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs["c|"+key+"|en"]
	return d, ok
}

// setFail makes a path answer 500 (or stop doing so).
func (s *planServer) setFail(path string, on bool) {
	s.mu.Lock()
	s.fail[path] = on
	s.mu.Unlock()
}
