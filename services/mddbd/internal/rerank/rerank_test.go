package rerank

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

// fakeServer answers /rerank by scoring each document by how many times it
// contains the query, or with whatever body is set.
type fakeServer struct {
	*httptest.Server
	calls atomic.Int64
	body  atomic.Value // string: a fixed answer instead of scoring
	code  atomic.Int64
	delay atomic.Int64 // nanoseconds
	auth  atomic.Value // string: last Authorization header
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	f.body.Store("")
	f.auth.Store("")
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.auth.Store(r.Header.Get("Authorization"))
		if d := f.delay.Load(); d > 0 {
			time.Sleep(time.Duration(d))
		}
		if c := f.code.Load(); c != 0 {
			w.WriteHeader(int(c))
			_, _ = io.WriteString(w, "upstream says no")
			return
		}
		if b := f.body.Load().(string); b != "" {
			_, _ = io.WriteString(w, b)
			return
		}
		var req httpRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		type entry struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		}
		var out []entry
		for i, d := range req.Documents {
			out = append(out, entry{Index: i, Score: float64(strings.Count(d, req.Query))})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": out})
	}))
	t.Cleanup(f.Close)
	return f
}

func TestHTTPProviderReorders(t *testing.T) {
	f := newFakeServer(t)
	p := NewHTTPProvider(Config{APIURL: f.URL + "/", APIKey: "k", Model: "m"})
	if p.Model() != "m" {
		t.Fatalf("model %q", p.Model())
	}
	u := &Usage{}
	got, err := p.Rerank(WithUsage(context.Background(), u), "go", []string{"none", "go go", "go"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 2 {
		t.Fatalf("order %+v", got)
	}
	if f.auth.Load().(string) != "Bearer k" {
		t.Fatalf("auth header %q", f.auth.Load())
	}
	if u.Calls() != 1 || u.Chars() != int64(len("go")+len("none")+len("go go")+len("go")) {
		t.Fatalf("usage %d %d", u.Calls(), u.Chars())
	}
	if got, err := p.Rerank(context.Background(), "q", nil, 0); got != nil || err != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
}

func TestHTTPProviderShapes(t *testing.T) {
	f := newFakeServer(t)
	p := NewHTTPProvider(Config{APIURL: f.URL})
	for name, body := range map[string]string{
		"data":  `{"data":[{"index":0,"score":0.1},{"index":1,"score":0.9}]}`,
		"bare":  `[{"index":0,"score":0.1},{"index":1,"relevance_score":0.9}]`,
		"ties":  `{"results":[{"index":1,"score":0.5},{"index":0,"score":0.5}]}`,
		"empty": `{"results":[]}`,
	} {
		f.body.Store(body)
		got, err := p.Rerank(context.Background(), "q", []string{"a", "b"}, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "empty" {
			if len(got) != 0 {
				t.Fatalf("empty: %+v", got)
			}
			continue
		}
		want := 1
		if name == "ties" {
			want = 0
		}
		if got[0].Index != want {
			t.Fatalf("%s: %+v", name, got)
		}
	}
}

func TestHTTPProviderInvalidAnswers(t *testing.T) {
	f := newFakeServer(t)
	p := NewHTTPProvider(Config{APIURL: f.URL})
	for _, body := range []string{
		`not json`, `[not json`, `{"other":1}`,
		`{"results":[{"index":5,"score":1}]}`, `{"results":[{"score":1}]}`,
		`{"results":[{"index":0}]}`, `{"results":[{"index":0,"score":1},{"index":0,"score":2}]}`,
	} {
		f.body.Store(body)
		_, err := p.Rerank(context.Background(), "q", []string{"a", "b"}, 0)
		if !errors.Is(err, ErrInvalidResponse) || Reason(err) != "invalidResponse" {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

func TestHTTPProviderErrorsAndTimeout(t *testing.T) {
	f := newFakeServer(t)
	f.code.Store(http.StatusInternalServerError)
	p := NewHTTPProvider(Config{APIURL: f.URL})
	_, err := p.Rerank(context.Background(), "q", []string{"a"}, 0)
	if err == nil || Reason(err) != "error" || !strings.Contains(err.Error(), "500") {
		t.Fatalf("status: %v", err)
	}
	f.code.Store(0)
	f.delay.Store(int64(200 * time.Millisecond))
	p = NewHTTPProvider(Config{APIURL: f.URL, Timeout: 20 * time.Millisecond})
	_, err = p.Rerank(context.Background(), "q", []string{"a"}, 0)
	if Reason(err) != "timeout" {
		t.Fatalf("timeout: %v", err)
	}
	// A URL no client can build a request from.
	bad := NewHTTPProvider(Config{APIURL: "http://[::1"})
	if _, err := bad.Rerank(context.Background(), "q", []string{"a"}, 0); err == nil {
		t.Fatal("bad url accepted")
	}
	// Nothing listening.
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if _, err := NewHTTPProvider(Config{APIURL: closed.URL}).Rerank(context.Background(), "q", []string{"a"}, 0); err == nil {
		t.Fatal("closed server answered")
	}
}

func TestReadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "{")
	}))
	defer srv.Close()
	_, err := NewHTTPProvider(Config{APIURL: srv.URL}).Rerank(context.Background(), "q", []string{"a"}, 0)
	if err == nil {
		t.Fatal("short body accepted")
	}
}

func TestReasonOther(t *testing.T) {
	if Reason(errors.New("x")) != "error" || Reason(context.DeadlineExceeded) != "timeout" {
		t.Fatal("reason")
	}
}

func TestConfig(t *testing.T) {
	t.Setenv("MDDB_RERANK_PROVIDER", "http")
	t.Setenv("MDDB_RERANK_API_URL", "http://127.0.0.1:9")
	t.Setenv("MDDB_RERANK_API_KEY", "secret")
	t.Setenv("MDDB_RERANK_MODEL", "m1")
	t.Setenv("MDDB_RERANK_TIMEOUT_MS", "150")
	t.Setenv("MDDB_RERANK_MAX_CANDIDATES", "999")
	c := ConfigFromEnv()
	if !c.Enabled() || c.Timeout != 150*time.Millisecond || c.MaxCandidates != 999 {
		t.Fatalf("env: %+v", c)
	}
	if n := c.Normalized(); n.MaxCandidates != MaxCandidatesCeiling {
		t.Fatalf("ceiling: %d", n.MaxCandidates)
	}
	if n := (Config{}).Normalized(); n.Timeout != DefaultTimeout || n.MaxCandidates != DefaultMaxCandidates {
		t.Fatalf("defaults: %+v", n)
	}
	p := FromEnv()
	if _, ok := p.(*CachingProvider); !ok || p.Model() != "m1" {
		t.Fatalf("from env: %T", p)
	}
	t.Setenv("MDDB_RERANK_CACHE_SIZE", "0")
	if _, ok := FromEnv().(*HTTPProvider); !ok {
		t.Fatal("cache size 0 must give the bare provider")
	}
}

func TestNewRefuses(t *testing.T) {
	for _, c := range []Config{
		{}, {Provider: ProviderNone}, {Provider: "other", APIURL: "http://127.0.0.1:1"},
		{Provider: ProviderHTTP}, {Provider: ProviderHTTP, APIURL: "ftp://x"},
	} {
		if New(c, 10) != nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestCachingProvider(t *testing.T) {
	f := newFakeServer(t)
	p := NewCachingProvider(NewHTTPProvider(Config{APIURL: f.URL}), 2, 0).(*CachingProvider)
	ctx := context.Background()
	if _, err := p.Rerank(ctx, "a", []string{"a", "aa"}, 0); err != nil {
		t.Fatal(err)
	}
	u := &Usage{}
	got, err := p.Rerank(WithUsage(ctx, u), "a", []string{"aa", "a", "aaa"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 || u.Cached() != 2 || u.Calls() != 1 {
		t.Fatalf("calls %d cached %d", f.calls.Load(), u.Cached())
	}
	if len(got) != 2 || got[0].Index != 2 {
		t.Fatalf("order %+v", got)
	}
	// All cached: no call.
	if _, err := p.Rerank(ctx, "a", []string{"aaa"}, 0); err != nil || f.calls.Load() != 2 {
		t.Fatalf("cached call: %v %d", err, f.calls.Load())
	}
	if p.Len() != 2 {
		t.Fatalf("bound: %d", p.Len())
	}
	// Re-putting an existing key keeps one entry.
	p.put(p.key("a", "aaa"), 9)
	if p.Len() != 2 {
		t.Fatalf("re-put: %d", p.Len())
	}
	// Expiry.
	p.now = func() time.Time { return time.Now().Add(2 * DefaultCacheTTL) }
	if _, ok := p.get(p.key("a", "aaa")); ok {
		t.Fatal("expired entry served")
	}
	// Errors pass through and are not cached.
	f.code.Store(http.StatusBadGateway)
	if _, err := p.Rerank(ctx, "zzz", []string{"q"}, 0); err == nil {
		t.Fatal("error swallowed")
	}
	if NewCachingProvider(p.inner, 0, 0) != p.inner {
		t.Fatal("size 0 must return inner")
	}
}

func TestUsageNil(t *testing.T) {
	var u *Usage
	u.addCall("q", []string{"d"})
	u.addCached(1)
	if u.Calls() != 0 || u.Chars() != 0 || u.Cached() != 0 || UsageFrom(context.Background()) != nil {
		t.Fatal("nil usage")
	}
}
