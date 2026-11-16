package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// answering is a server that answers every request with status and body.
func answering(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// nodeAt is a node whose HTTP API is the server at url.
func nodeAt(url string) *node {
	return &node{httpAddr: strings.TrimPrefix(url, "http://"), client: http.DefaultClient}
}

func TestSearchFailures(t *testing.T) {
	old := pollPeriod
	pollPeriod = time.Millisecond
	t.Cleanup(func() { pollPeriod = old })
	p := tinyProfile()
	c := newCorpus(p, 1)
	s := func(url string) searcher { return searcher{client: http.DefaultClient, url: url, c: c, p: p} }

	if _, err := s(answering(t, 200, "{")).query(scenario{Kind: "vector"}, 0, false); err == nil {
		t.Error("a broken answer parsed")
	}
	if _, err := s(answering(t, 500, "")).waitAnswering(time.Now(), 5*time.Millisecond); err == nil {
		t.Error("a failing index answered")
	}
	if _, err := s(answering(t, 200, `{"results":[]}`)).measure(scenario{Name: "x", Kind: "fts"}, nil); err == nil {
		t.Error("a scenario with no results measured")
	}
	if _, err := s(answering(t, 500, "")).run(io.Discard); err == nil {
		t.Error("a failing server ran")
	}
	a, err := s(answering(t, 200, `{"plan":{"algorithm":"flat","prefilter":true}}`)).query(scenario{Kind: "filtered", Pct: 1}, 0, true)
	if err != nil || a.ran != "flat+prefilter" {
		t.Errorf("plan: %+v %v", a, err)
	}
}

func TestReplicaFailures(t *testing.T) {
	old := pollPeriod
	pollPeriod = time.Millisecond
	t.Cleanup(func() { pollPeriod = old })
	c := newCorpus(tinyProfile(), 1)

	if _, _, err := checksum(http.DefaultClient, answering(t, 404, ""), "c"); err == nil {
		t.Error("a 404 checksum")
	}
	if _, _, err := checksum(http.DefaultClient, "http://127.0.0.1:1", "c"); err == nil {
		t.Error("a checksum with no server")
	}
	pair := replicaPair{
		leader:     nodeAt(answering(t, 200, `{"checksum":"a","documentCount":1}`)),
		follower:   nodeAt(answering(t, 200, `{"checksum":"b","documentCount":1}`)),
		collection: "c", timeout: 5 * time.Millisecond,
	}
	if err := pair.waitInSync(); err == nil || !strings.Contains(err.Error(), "follower has 1 documents (checksum b)") {
		t.Errorf("never in sync: %v", err)
	}
	pair.leader = nodeAt("http://127.0.0.1:1")
	if err := pair.waitInSync(); err == nil {
		t.Error("in sync with no leader")
	}
	down := writer{client: http.DefaultClient, url: "http://127.0.0.1:1", collection: "c"}
	if _, _, err := pair.lagUnderLoad(down, c, 1, 1000); err == nil {
		t.Error("lag measured with no leader")
	}
	pair.follower = nodeAt(answering(t, 404, ""))
	up := writer{client: http.DefaultClient, url: answering(t, 200, "{}"), collection: "c"}
	oldLate := lateProbe
	lateProbe = 10 * time.Millisecond
	t.Cleanup(func() { lateProbe = oldLate })
	if lag, late, err := pair.lagUnderLoad(up, c, 3, 1000); err != nil || late != 3 || len(lag) != 0 {
		t.Errorf("a probe that never arrived: lag %v, late %d, %v", lag, late, err)
	}
}
