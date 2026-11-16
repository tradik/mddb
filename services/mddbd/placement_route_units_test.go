package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	proto "mddb/proto"

	"google.golang.org/grpc"
)

// GO-052: the router and the forwarding, branch by branch, on runtimes
// cloned from a running node so a test may give them groups of its own.

// fakeLeaderRepl answers a probe as the leader of its group.
type fakeLeaderRepl struct {
	proto.UnimplementedMDDBReplicationServer
	id string
}

func (f fakeLeaderRepl) ReplicationStatus(context.Context, *proto.ReplicationStatusRequest) (*proto.ReplicationStatusResponse, error) {
	return &proto.ReplicationStatusResponse{NodeId: f.id, Role: "leader", LeaderId: f.id}, nil
}

// startFakeLeader serves a replication service whose node says it leads.
func startFakeLeader(t *testing.T, id string) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	proto.RegisterMDDBReplicationServer(srv, fakeLeaderRepl{id: id})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

// closedURL is an http URL nothing listens on.
func closedURL(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return "http://" + addr
}

// unitRuntime is a runtime that is not running, cloned from node's, with
// extra groups and the collections placed in them.
func unitRuntime(node *placementNodeT, extra map[string][]placementNode, placed map[string]string) *placementRuntime {
	base := node.s.placement
	cfg := base.cfg
	cfg.groups = map[string][]placementNode{}
	for g, ns := range base.cfg.groups {
		cfg.groups[g] = ns
	}
	for g, ns := range extra {
		cfg.groups[g] = ns
	}
	cfg.names = nil
	for g := range cfg.groups {
		cfg.names = append(cfg.names, g)
	}
	sort.Strings(cfg.names)
	p := &placementRuntime{s: node.s, cfg: cfg, ring: base.ring, changed: make(chan struct{}), client: base.client}
	p.local.init()
	p.moves.init()
	t := base.current().clone()
	for c, g := range placed {
		t.Entries[c] = placementEntry{Group: g}
	}
	p.table.Store(t)
	return p
}

// fakeGroup is a group "fake" of one node that says it leads, answering
// REST at httpURL.
func fakeGroup(t *testing.T, httpURL string) map[string][]placementNode {
	return map[string][]placementNode{"fake": {{ID: "fake-1", GRPC: startFakeLeader(t, "fake-1"), HTTP: httpURL}}}
}

// here is a next handler that answers 299.
var here = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(299) })

// routeOnce routes a request through p and returns the answer.
func routeOnce(p *placementRuntime, method, pattern, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, pattern, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	p.route(w, r, pattern, here)
	return w
}

// errBody fails every read.
type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("broken body") }
func (errBody) Close() error             { return nil }

// nameIn is a collection name the runtime places in group.
func nameIn(t *testing.T, p *placementRuntime, group string) string {
	t.Helper()
	for i := range 10000 {
		c := fmt.Sprintf("unit-%d", i)
		if g, _ := p.owner(c); g == group {
			return c
		}
	}
	t.Fatalf("no name hashes to %s", group)
	return ""
}

func TestPlacementRouteBranches(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	meta := tp.leader("meta")
	p := unitRuntime(meta, nil, nil)
	mine, theirs := nameIn(t, p, "meta"), nameIn(t, p, "g2")
	cases := []struct {
		name, method, pattern, body string
		want                        int
	}{
		{"not a collection route", http.MethodGet, "/v1/health", "", 299},
		{"a route without a collection", http.MethodGet, "/v1/indexing-status", "", 299},
		{"collections of two groups", http.MethodPost, "/v1/add", `{"collection":"` + mine + `","sourceCollection":"` + theirs + `"}`, http.StatusConflict},
		{"a read here", http.MethodPost, "/v1/get", `{"collection":"` + mine + `"}`, 299},
		{"a cross-search of one group", http.MethodPost, "/v1/cross-search", `{"targetCollections":["` + mine + `"]}`, 299},
		{"a write here, as leader", http.MethodPost, "/v1/add", `{"collection":"` + mine + `"}`, 299},
		{"a part of a fan-out", http.MethodPost, "/v1/add", `{"collection":"` + theirs + `"}`, 299},
	}
	for _, c := range cases {
		hdr := map[string]string{}
		if strings.HasPrefix(c.name, "a part") {
			hdr[hdrPlacementPartial] = "1"
		}
		if got := routeOnce(p, c.method, c.pattern, c.body, hdr).Code; got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	// An unreadable body.
	r := httptest.NewRequest(http.MethodPost, "/v1/add", nil)
	r.Body = errBody{}
	w := httptest.NewRecorder()
	p.route(w, r, "/v1/add", here)
	if w.Code != http.StatusBadRequest {
		t.Errorf("unreadable body: %d", w.Code)
	}
	// No map yet.
	p.table.Store(nil)
	if got := routeOnce(p, http.MethodPost, "/v1/get", `{"collection":"x"}`, nil).Code; got != http.StatusServiceUnavailable {
		t.Errorf("no map: %d", got)
	}
}

func TestPlacementRouteHoldsAndRefusesFencedWrites(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	p := unitRuntime(tp.leader("meta"), nil, nil)
	c := nameIn(t, p, "meta")
	p.cfg.fenceTimeout = 30 * time.Millisecond
	if err := p.local.fence(c, time.Second); err != nil {
		t.Fatal(err)
	}
	body := `{"collection":"` + c + `"}`
	if got := routeOnce(p, http.MethodPost, "/v1/add", body, nil).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("a write held past the fence timeout: %d", got)
	}
	// The map keeps changing while the write waits: routed again, then refused.
	p.cfg.fenceTimeout = 5 * time.Second
	stop := make(chan struct{})
	go func() {
		for v := uint64(1); ; v++ {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				next := p.current().clone()
				next.Version += v
				p.adopt(next)
			}
		}
	}()
	defer close(stop)
	if got := routeOnce(p, http.MethodPost, "/v1/add", body, nil).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("a write whose placement keeps changing: %d", got)
	}
	// Lifted: the write goes through.
	p.local.lift(c)
	if got := routeOnce(p, http.MethodPost, "/v1/add", body, nil).Code; got != 299 {
		t.Fatalf("after the fence: %d", got)
	}
}

func TestPlacementRouteSendsRoleCrossSearchToEveryGroup(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1, "g2": 1}, "meta")
	n := tp.leader("g2")
	var got struct {
		Results []any `json:"results"`
	}
	if _, err := n.call(http.MethodPost, "/v1/cross-search", map[string]any{"targetCollections": []string{"role:reference"},
		"query": "x", "searchType": "fts"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 0 {
		t.Fatalf("results without any reference collection: %v", got.Results)
	}
}

func TestPlacementForwardBranches(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	node := tp.leader("meta")
	misdirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMisdirectedRequest)
	}))
	defer misdirected.Close()
	hangup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer hangup.Close()
	cases := []struct {
		name   string
		url    string
		method string
		hdr    map[string]string
		want   int
	}{
		{"too many hops", misdirected.URL, http.MethodPost, map[string]string{hdrPlacementHops: "2"}, http.StatusServiceUnavailable},
		{"misdirected twice", misdirected.URL, http.MethodPost, nil, http.StatusMisdirectedRequest},
		{"nobody listens", closedURL(t), http.MethodPost, nil, http.StatusServiceUnavailable},
		{"the connection drops", hangup.URL, http.MethodPost, nil, http.StatusServiceUnavailable},
		{"a method no request can carry", misdirected.URL, "BAD METHOD", nil, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		p := unitRuntime(node, fakeGroup(t, c.url), map[string]string{"far": "fake"})
		r := httptest.NewRequest(http.MethodPost, "/v1/get", strings.NewReader(`{"collection":"far"}`))
		r.Method = c.method
		for k, v := range c.hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		p.forward(w, r, "fake")
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d: %s", c.name, w.Code, c.want, w.Body)
		}
	}
	// No leader known, an unreadable body, and the redirect without a leader.
	p := unitRuntime(node, map[string][]placementNode{"lost": {{ID: "lost-1", GRPC: "127.0.0.1:1", HTTP: closedURL(t)}}}, nil)
	w := httptest.NewRecorder()
	p.forward(w, httptest.NewRequest(http.MethodGet, "/v1/get", nil), "lost")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("no leader: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/get", nil)
	r.Body = errBody{}
	w = httptest.NewRecorder()
	p.forward(w, r, "lost")
	if w.Code != http.StatusBadRequest {
		t.Errorf("unreadable body: %d", w.Code)
	}
	p.cfg.redirect = true
	w = httptest.NewRecorder()
	p.forward(w, httptest.NewRequest(http.MethodGet, "/v1/get", nil), "lost")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("redirect without a leader: %d", w.Code)
	}
}

func TestPlacementForwardingClientFollowsNoRedirect(t *testing.T) {
	tp := startTestPlacement(t, map[string]int{"meta": 1}, "meta")
	to := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer to.Close()
	resp, err := tp.leader("meta").s.placement.client.Get(to.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("the forwarding client followed a redirect: %d", resp.StatusCode)
	}
}
