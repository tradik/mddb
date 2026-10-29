package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

func gainServer(t *testing.T) (*Server, func()) {
	s, cleanup := newHandlerTestServer(t)
	s.Gain = NewGainRecorder(func() *bolt.DB { return s.DB })
	return s, cleanup
}

func TestGain_CountsAndSurvivesRestart(t *testing.T) {
	s, cleanup := gainServer(t)
	defer cleanup()
	g := s.Gain
	g.Search(gainKind{Type: "vector", Algorithm: "hnsw"})
	g.Search(gainKind{Type: "hybrid", Algorithm: "flat", Fusion: "rrf"})
	g.Hits([]gainSize{{Returned: 10, Full: 100}, {Returned: 50, Full: 50}})
	g.Hits(nil)
	before, err := g.Report(30, true)
	if err != nil {
		t.Fatal(err)
	}
	tot := before.Totals
	if before.TotalSearches != 2 || tot.TokensReturned != 60 || tot.TokensAvoided != 90 || tot.FragmentHits != 1 ||
		tot.DocumentHits != 1 || tot.Fusion["rrf"] != 1 || before.TokensWithoutRetrieval != 150 || before.SavedPercent != 60 {
		t.Fatalf("report: %+v", before)
	}
	if len(before.Daily) != 30 || before.Daily[29].Date != before.To {
		t.Fatalf("daily: %d", len(before.Daily))
	}
	// /metrics reads the counts since start.
	if v, ok := (&serverMetricsStats{s: s}).RetrievalGain(); !ok || v.TokensAvoided != 90 || v.FragmentHits != 1 {
		t.Fatalf("metrics view: %+v %v", v, ok)
	}
	if _, ok := (&serverMetricsStats{s: &Server{}}).RetrievalGain(); ok {
		t.Fatal("no recorder, no metric")
	}

	// Stop writes what is pending; a new recorder on the same database — a
	// restart — reports the same numbers.
	if err := g.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := g.Stop(); err != nil {
		t.Fatal("stop twice")
	}
	after, err := NewGainRecorder(func() *bolt.DB { return s.DB }).Report(30, false)
	if err != nil || after.Totals.TokensAvoided != 90 || after.TotalSearches != 2 || after.Daily != nil {
		t.Fatalf("after restart: %v %+v", err, after)
	}
	// Flushing adds to the stored day rather than replacing it.
	g2 := NewGainRecorder(func() *bolt.DB { return s.DB })
	g2.Hits([]gainSize{{Returned: 1, Full: 1}})
	if err := g2.Flush(); err != nil {
		t.Fatal(err)
	}
	if r, _ := g2.Report(1, false); r.Totals.TokensReturned != 61 {
		t.Fatalf("accumulate: %+v", r.Totals)
	}
	if err := g2.Flush(); err != nil {
		t.Fatal("empty flush")
	}
}

func TestGain_FlushFailureKeepsCounts(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "closed.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	g := NewGainRecorder(func() *bolt.DB { return db })
	g.Search(gainKind{Type: "fts"})
	if err := g.Flush(); err == nil {
		t.Fatal("closed database")
	}
	g.Search(gainKind{Type: "fts"})
	if g.pending[g.now().UTC().Format(time.DateOnly)].Searches["fts"] != 2 {
		t.Fatal("counts kept for the next flush")
	}
	if _, _, err := g.Days(0); err == nil {
		t.Fatal("zero window")
	}
	var nilRec *GainRecorder
	nilRec.Search(gainKind{})
	nilRec.Hits([]gainSize{{}})
	if _, err := nilRec.Report(1, false); err == nil {
		t.Fatal("nil report")
	}
}

func TestGain_FlusherAndWindow(t *testing.T) {
	s, cleanup := gainServer(t)
	defer cleanup()
	defer func(d time.Duration) { gainFlushEvery = d }(gainFlushEvery)
	gainFlushEvery = 5 * time.Millisecond
	s.Gain.Start()
	s.Gain.Search(gainKind{Type: "fts"})
	// The ticker writes it without a Stop.
	stored := func() int64 {
		var n int64
		_ = s.DB.View(func(tx *bolt.Tx) error {
			if b := tx.Bucket(bucketStats); b != nil {
				n = readGainDay(b, time.Now().UTC().Format(time.DateOnly)).Searches["fts"]
			}
			return nil
		})
		return n
	}
	for deadline := time.Now().Add(2 * time.Second); stored() != 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the ticker never flushed")
		}
	}
	// A failing flush on the ticker is logged and kept.
	closed, _ := bolt.Open(filepath.Join(t.TempDir(), "c.db"), 0o600, nil)
	_ = closed.Close()
	if err := s.Gain.Stop(); err != nil {
		t.Fatal(err)
	}
	failing := NewGainRecorder(func() *bolt.DB { return closed })
	failing.Start()
	failing.Search(gainKind{Type: "fts"})
	time.Sleep(30 * time.Millisecond)
	if err := failing.Stop(); err == nil {
		t.Fatal("stop on a closed database reports it")
	}
	if r, _ := NewGainRecorder(func() *bolt.DB { return &bolt.DB{} }).Report(1, false); r != nil && r.SavedPercent != 0 {
		t.Fatal("empty report")
	}
	countHits(&Server{}, []int{1}, func(int) gainSize { return gainSize{} })
	for in, want := range map[string]int{"": 30, "7d": 7, "365": 365} {
		if got, err := parseGainWindow(in); err != nil || got != want {
			t.Fatalf("%q: %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"0d", "366d", "x"} {
		if _, err := parseGainWindow(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestGain_HTTPAndMCP(t *testing.T) {
	s, cleanup := gainServer(t)
	defer cleanup()
	s.Gain.Search(gainKind{Type: "fts", Algorithm: "bm25"})
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleGain(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	var rep GainReport
	if rec := get("/v1/gain?window=7d&daily=true"); json.Unmarshal(rec.Body.Bytes(), &rep) != nil || rep.WindowDays != 7 || len(rep.Daily) != 7 {
		t.Fatalf("gain: %s", rec.Body.String())
	}
	if rec := get("/v1/gain?window=x"); rec.Code != 400 {
		t.Fatal("bad window")
	}
	rec := httptest.NewRecorder()
	s.handleGain(rec, httptest.NewRequest(http.MethodPost, "/v1/gain", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatal("method")
	}
	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.mcpCallTool(context.Background(), "get_gain", map[string]interface{}{"window": "1d"})
	if err != nil || !strings.Contains(out, `"bm25": 1`) {
		t.Fatalf("mcp: %v %s", err, out)
	}
	if _, err := tools.mcpCallTool(context.Background(), "get_gain", map[string]interface{}{"window": "0d"}); err == nil {
		t.Fatal("mcp bad window")
	}
	s.Gain = nil
	if rec := get("/v1/gain"); rec.Code != 400 {
		t.Fatal("no recorder")
	}
	if _, err := tools.mcpCallTool(context.Background(), "get_gain", nil); err == nil {
		t.Fatal("mcp no recorder")
	}

	// With auth on, an anonymous caller is refused.
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am
	if rec := get("/v1/gain"); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous: %d", rec.Code)
	}
}

func TestGain_PassageHits(t *testing.T) {
	hits := passageHits([]Passage{
		{Collection: "c", Key: "a", Tokens: 10, docTokens: 100},
		{Collection: "c", Key: "a", Tokens: 20, docTokens: 100},
		{Collection: "c", Key: "b", Tokens: 5, docTokens: 5},
	})
	if len(hits) != 2 || hits[0] != (gainSize{Returned: 30, Full: 100}) || hits[1] != (gainSize{Returned: 5, Full: 5}) {
		t.Fatalf("hits: %+v", hits)
	}
	if chunkHit("", "abcd", 0) != (gainSize{Returned: 1, Full: 1}) || chunkHit("abcd", "", 9) != (gainSize{Returned: 1, Full: 9}) {
		t.Fatal("chunkHit")
	}
}
