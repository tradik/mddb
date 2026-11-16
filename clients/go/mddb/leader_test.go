package mddb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// GO-051: following the cluster leader. A fake follower answers writes with
// 421 (or 503 while "electing") and a fake leader accepts them.

// node is a fake cluster node recording what it received.
type node struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string // "METHOD /path body"
	answer func(w http.ResponseWriter, r *http.Request, hit int)
}

func newNode(t *testing.T, answer func(w http.ResponseWriter, r *http.Request, hit int)) *node {
	t.Helper()
	n := &node{answer: answer}
	n.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		n.mu.Lock()
		n.hits = append(n.hits, r.Method+" "+r.URL.Path+" "+string(b))
		hit := len(n.hits)
		n.mu.Unlock()
		n.answer(w, r, hit)
	}))
	t.Cleanup(n.srv.Close)
	return n
}

func (n *node) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.hits)
}

func (n *node) last() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hits[len(n.hits)-1]
}

// ok answers 200 with a document.
func ok(w http.ResponseWriter, _ *http.Request, _ int) {
	_, _ = w.Write([]byte(`{"id":"blog|k|en","key":"k"}`))
}

// misdirect answers 421 naming leader in the header and/or the body.
func misdirect(header, bodyAddr string) func(w http.ResponseWriter, r *http.Request, hit int) {
	return func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.URL.Path == "/v1/get" {
			ok(w, r, 0)
			return
		}
		if header != "" {
			w.Header().Set(headerLeader, header)
		}
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = w.Write([]byte(`{"error":"not the leader","leaderAddr":"` + bodyAddr + `","leaderId":"n2","term":5}`))
	}
}

// noLeader answers 503 "no leader" for the first n hits, then ok.
func noLeader(n int, retryAfter string) func(w http.ResponseWriter, r *http.Request, hit int) {
	return func(w http.ResponseWriter, r *http.Request, hit int) {
		if hit > n {
			ok(w, r, hit)
			return
		}
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"no leader: an election is in progress","term":7}`))
	}
}

// recordSleeps replaces the client's election wait with a recorder.
func recordSleeps(c *Client) *[]time.Duration {
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return &waits
}

func asAPIError(t *testing.T, err error) *APIError {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T %v", err, err)
	}
	return apiErr
}

func TestFollowLeaderOffSurfacesLeader(t *testing.T) {
	leader := newNode(t, ok)
	follower := newNode(t, misdirect(leader.srv.URL, "http://body-only:1"))
	c := New(follower.srv.URL)

	_, err := c.Add(context.Background(), AddRequest{Collection: "blog", Key: "k"})
	e := asAPIError(t, err)
	if e.StatusCode != http.StatusMisdirectedRequest || e.LeaderAddr != leader.srv.URL || e.LeaderID != "n2" || e.Term != 5 {
		t.Errorf("APIError = %+v", e)
	}
	if leader.count() != 0 || follower.count() != 1 || c.Leader() != "" {
		t.Errorf("follow off must not redirect: leader=%d follower=%d learned=%q", leader.count(), follower.count(), c.Leader())
	}
}

func TestLeaderAddrFallsBackToBody(t *testing.T) {
	follower := newNode(t, misdirect("", "http://10.0.0.2:11023"))
	_, err := New(follower.srv.URL).Do(context.Background(), "POST", "/v1/add", map[string]string{})
	if e := asAPIError(t, err); e.LeaderAddr != "http://10.0.0.2:11023" {
		t.Errorf("LeaderAddr = %q, want the body's", e.LeaderAddr)
	}
}

func TestNoLeaderErrorFields(t *testing.T) {
	n := newNode(t, noLeader(100, "1"))
	_, err := New(n.srv.URL).Do(context.Background(), "POST", "/v1/add", nil)
	if e := asAPIError(t, err); !e.NoLeader || e.Term != 7 || e.LeaderAddr != "" {
		t.Errorf("APIError = %+v", e)
	}
	// A 503 that is not about the election (or not JSON) is not NoLeader.
	if e := newAPIError(http.StatusServiceUnavailable, http.Header{}, []byte(`{"status":"warming_up"}`)); e.NoLeader {
		t.Error("warming_up 503 reported as NoLeader")
	}
	if e := newAPIError(http.StatusBadRequest, http.Header{}, []byte(`x`)); e.LeaderAddr != "" || e.NoLeader {
		t.Errorf("400 carries leader fields: %+v", e)
	}
}

func TestFollowLeaderRedirectsAndRemembers(t *testing.T) {
	leader := newNode(t, ok)
	follower := newNode(t, misdirect(leader.srv.URL+"/", ""))
	c := New(follower.srv.URL, WithFollowLeader(true))
	ctx := context.Background()

	doc, err := c.Add(ctx, AddRequest{Collection: "blog", Key: "k", ContentMD: "body"})
	if err != nil || doc.ID != "blog|k|en" {
		t.Fatalf("Add: %+v %v", doc, err)
	}
	if c.Leader() != leader.srv.URL {
		t.Errorf("Leader() = %q, want %q", c.Leader(), leader.srv.URL)
	}
	if follower.last() != leader.last() || !strings.Contains(leader.last(), `"contentMd":"body"`) {
		t.Errorf("body not re-sent intact: follower %q leader %q", follower.last(), leader.last())
	}

	// The next write of that kind goes to the leader directly.
	if _, err := c.Add(ctx, AddRequest{Collection: "blog", Key: "k2"}); err != nil {
		t.Fatal(err)
	}
	if follower.count() != 1 || leader.count() != 2 {
		t.Errorf("second write: follower=%d leader=%d, want 1 and 2", follower.count(), leader.count())
	}
	// Reads keep going to the configured node.
	if _, err := c.Get(ctx, GetRequest{Collection: "blog", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if follower.count() != 2 || leader.count() != 2 {
		t.Errorf("read: follower=%d leader=%d, want 2 and 2", follower.count(), leader.count())
	}
}

func TestFollowLeaderUsesBodyAddress(t *testing.T) {
	leader := newNode(t, ok)
	follower := newNode(t, misdirect("", leader.srv.URL))
	if _, err := New(follower.srv.URL, WithFollowLeader(true)).Add(context.Background(), AddRequest{}); err != nil {
		t.Fatal(err)
	}
	if leader.count() != 1 {
		t.Errorf("leader hits = %d, want 1", leader.count())
	}
}

func TestFollowLeaderRedirectsOnlyOnce(t *testing.T) {
	third := newNode(t, ok)
	second := newNode(t, misdirect(third.srv.URL, ""))
	first := newNode(t, misdirect(second.srv.URL, ""))
	_, err := New(first.srv.URL, WithFollowLeader(true)).Add(context.Background(), AddRequest{})
	if e := asAPIError(t, err); e.LeaderAddr != third.srv.URL {
		t.Errorf("APIError = %+v", e)
	}
	if third.count() != 0 || second.count() != 1 {
		t.Errorf("redirect loop: second=%d third=%d", second.count(), third.count())
	}
}

func TestFollowLeaderRejectsUnusableAddress(t *testing.T) {
	for _, addr := range []string{"ftp://10.0.0.2", "10.0.0.2:11023", "http://", "http://%zz"} {
		follower := newNode(t, misdirect(addr, ""))
		c := New(follower.srv.URL, WithFollowLeader(true))
		_, err := c.Add(context.Background(), AddRequest{})
		if e := asAPIError(t, err); e.LeaderAddr != addr || c.Leader() != "" || follower.count() != 1 {
			t.Errorf("%q: err=%+v learned=%q hits=%d", addr, e, c.Leader(), follower.count())
		}
	}
}

func TestFollowLeaderWaitsForElection(t *testing.T) {
	cases := []struct {
		retryAfter string
		want       time.Duration
	}{
		{"1", time.Second},
		{"100", maxNoLeaderWait},
		{"", defaultNoLeaderWait},
		{"soon", defaultNoLeaderWait},
		{"-4", defaultNoLeaderWait},
	}
	for _, tc := range cases {
		n := newNode(t, noLeader(2, tc.retryAfter))
		c := New(n.srv.URL, WithFollowLeader(true))
		waits := recordSleeps(c)
		if _, err := c.Add(context.Background(), AddRequest{}); err != nil {
			t.Fatalf("%q: %v", tc.retryAfter, err)
		}
		if len(*waits) != 2 || (*waits)[0] != tc.want || n.count() != 3 {
			t.Errorf("%q: waits=%v hits=%d", tc.retryAfter, *waits, n.count())
		}
	}
}

func TestFollowLeaderElectionWaitsAreBounded(t *testing.T) {
	n := newNode(t, noLeader(100, "1"))
	c := New(n.srv.URL, WithFollowLeader(true))
	waits := recordSleeps(c)
	_, err := c.Add(context.Background(), AddRequest{})
	if e := asAPIError(t, err); !e.NoLeader {
		t.Errorf("APIError = %+v", e)
	}
	if len(*waits) != maxNoLeaderWaits || n.count() != maxNoLeaderWaits+1 {
		t.Errorf("waits=%d hits=%d", len(*waits), n.count())
	}
}

func TestFollowLeaderIgnoresOther503(t *testing.T) {
	n := newNode(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"warming_up"}`))
	})
	c := New(n.srv.URL, WithFollowLeader(true))
	waits := recordSleeps(c)
	if _, err := c.Add(context.Background(), AddRequest{}); err == nil || len(*waits) != 0 || n.count() != 1 {
		t.Errorf("err=%v waits=%d hits=%d", err, len(*waits), n.count())
	}
}

func TestFollowLeaderElectionWaitHonoursContext(t *testing.T) {
	n := newNode(t, noLeader(100, "1"))
	c := New(n.srv.URL, WithFollowLeader(true))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Add(ctx, AddRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Errorf("sleepCtx = %v", err)
	}
}

func TestFollowLeaderForgetsUnreachableLeader(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	follower := newNode(t, misdirect(goneURL, ""))
	c := New(follower.srv.URL, WithFollowLeader(true))
	ctx := context.Background()

	if _, err := c.Add(ctx, AddRequest{}); err == nil {
		t.Fatal("expected a transport error from the unreachable leader")
	}
	if c.Leader() != "" {
		t.Errorf("unreachable leader kept: %q", c.Leader())
	}
	// The next write asks the configured node again.
	_, _ = c.Add(ctx, AddRequest{})
	if follower.count() != 2 {
		t.Errorf("follower hits = %d, want 2", follower.count())
	}
}

func TestRememberLeaderBoundsRoutes(t *testing.T) {
	c := New("http://home", WithFollowLeader(true))
	for i := range maxWriteRoutes + 1 {
		c.rememberLeader(routeKey("POST", "/v1/x/"+string(rune('a'+i%26))+strings.Repeat("y", i)), "http://leader")
	}
	if n := len(c.leader.writes); n != 1 {
		t.Errorf("writes = %d, want reset to 1", n)
	}
	if routeKey("DELETE", "/v1/a?b=c") != "DELETE /v1/a" {
		t.Error("routeKey keeps the query")
	}
	c.forgetLeader("http://home")
	if c.Leader() != "http://leader" {
		t.Error("a failure of the home node forgot the leader")
	}
}

func TestDoStreamFollowsLeaderWithSeekableBody(t *testing.T) {
	leader := newNode(t, ok)
	follower := newNode(t, misdirect(leader.srv.URL, ""))
	c := New(follower.srv.URL, WithFollowLeader(true))

	body := bytes.NewReader([]byte("xxPACK"))
	_, _ = body.Seek(2, io.SeekStart) // the stream starts mid-reader
	rc, err := c.DoStream(context.Background(), "POST", "/v1/packs/import", "application/octet-stream", body)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if leader.last() != "POST /v1/packs/import PACK" {
		t.Errorf("leader got %q", leader.last())
	}
}

func TestDoStreamFollowsLeaderWithFile(t *testing.T) {
	leader := newNode(t, ok)
	follower := newNode(t, misdirect(leader.srv.URL, ""))
	c := New(follower.srv.URL, WithFollowLeader(true))

	f, err := os.Create(filepath.Join(t.TempDir(), "pack"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString("PACK")
	_, _ = f.Seek(0, io.SeekStart)
	rc, err := c.DoStream(context.Background(), "POST", "/v1/packs/import", "", f)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if leader.last() != "POST /v1/packs/import PACK" {
		t.Errorf("leader got %q", leader.last())
	}
}

func TestDoStreamUnreplayableBodyRemembersLeader(t *testing.T) {
	leader := newNode(t, ok)
	follower := newNode(t, misdirect(leader.srv.URL, ""))
	c := New(follower.srv.URL, WithFollowLeader(true))
	ctx := context.Background()

	_, err := c.DoStream(ctx, "POST", "/v1/packs/import", "", io.MultiReader(strings.NewReader("PACK")))
	if e := asAPIError(t, err); e.LeaderAddr != leader.srv.URL {
		t.Errorf("APIError = %+v", e)
	}
	if leader.count() != 0 || c.Leader() != leader.srv.URL {
		t.Fatalf("leader hits=%d learned=%q", leader.count(), c.Leader())
	}
	// The caller's next attempt goes straight to the leader.
	rc, err := c.DoStream(ctx, "POST", "/v1/packs/import", "", io.MultiReader(strings.NewReader("PACK")))
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if leader.count() != 1 {
		t.Errorf("leader hits = %d, want 1", leader.count())
	}
}

// badSeeker fails its Seek calls from call number failAt on.
type badSeeker struct {
	io.Reader
	calls, failAt int
}

func (b *badSeeker) Seek(int64, int) (int64, error) {
	b.calls++
	if b.calls >= b.failAt {
		return 0, errors.New("seek failed")
	}
	return 0, nil
}

func TestRequestBodyOpenErrors(t *testing.T) {
	c := New("http://127.0.0.1:1", WithFollowLeader(true))
	_, err := c.DoStream(context.Background(), "POST", "/x", "", &badSeeker{Reader: strings.NewReader("x"), failAt: 1})
	if err == nil || !strings.Contains(err.Error(), "seek failed") {
		t.Errorf("first open: %v", err)
	}

	rewind := &requestBody{stream: &badSeeker{Reader: strings.NewReader("x"), failAt: 2}, follow: true}
	if _, err := rewind.open(); err != nil {
		t.Fatal(err)
	}
	if _, err := rewind.open(); err == nil {
		t.Error("rewind: expected the seek error")
	}

	once := &requestBody{stream: io.MultiReader(), follow: true}
	if _, err := once.open(); err != nil {
		t.Fatal(err)
	}
	if _, err := once.open(); !errors.Is(err, errBodyNotReplayable) {
		t.Errorf("second open = %v, want errBodyNotReplayable", err)
	}
	if once.replayable() {
		t.Error("a plain reader reported replayable")
	}
}

func TestSendReadBodyError(t *testing.T) {
	// A server that promises more body than it sends: reading it fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort"))
		_ = conn.Close()
	}()
	if _, err := New("http://"+ln.Addr().String()).Do(context.Background(), "GET", "/x", nil); err == nil {
		t.Error("expected a body read error")
	}
}
