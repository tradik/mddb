package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"mddb/internal/audit"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// withTrustedProxies installs spec as MDDB_TRUSTED_PROXIES for one test and
// restores the previous list afterwards.
func withTrustedProxies(t *testing.T, spec string) {
	t.Helper()
	prev := trustedProxies.Load()
	t.Cleanup(func() { trustedProxies.Store(prev) })
	if err := configureTrustedProxies(spec); err != nil {
		t.Fatalf("configureTrustedProxies(%q): %v", spec, err)
	}
}

// requestFrom builds a request from remote with the given forwarding headers
// ("" leaves a header unset).
func requestFrom(remote, xff, realIP string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/fts", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	if realIP != "" {
		r.Header.Set("X-Real-IP", realIP)
	}
	return r
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := parseTrustedProxies(" 127.0.0.1 , 10.0.0.0/8,,::1, fd00::/8, ::ffff:192.168.1.1, ::ffff:172.16.0.0/108, 10.1.2.3/8, ::ffff:0:0/80 ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1/32", "10.0.0.0/8", "::1/128", "fd00::/8", "192.168.1.1/32", "172.16.0.0/12", "10.0.0.0/8", "::/80"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("entry %d = %s, want %s", i, got[i], want[i])
		}
	}

	if got, err := parseTrustedProxies(""); err != nil || len(got) != 0 {
		t.Errorf("empty spec = %v, %v; want no proxies", got, err)
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"proxy.local", "10.0.0.0/33", "300.1.1.1", "10.0.0.1/x", "; DROP"} {
		_, err := parseTrustedProxies("127.0.0.1," + bad)
		if err == nil {
			t.Errorf("%q: want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), strconv.Quote(strings.TrimSpace(bad))) || !strings.Contains(err.Error(), trustedProxiesEnv) {
			t.Errorf("%q: error %q does not name the entry and the variable", bad, err)
		}
	}
}

func TestConfigureTrustedProxiesKeepsListOnError(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8")
	if err := configureTrustedProxies("nonsense"); err == nil {
		t.Fatal("want an error")
	}
	if got := ClientIP(requestFrom("10.0.0.1:80", "203.0.113.7", "")); got != "203.0.113.7" {
		t.Errorf("previous list lost: ClientIP = %q", got)
	}
}

func TestClientIPUntrustedPeerIgnoresHeaders(t *testing.T) {
	for _, spec := range []string{"", "10.0.0.0/8"} {
		withTrustedProxies(t, spec)
		got := ClientIP(requestFrom("198.51.100.9:5555", "1.2.3.4, 10.0.0.1", "5.6.7.8"))
		if got != "198.51.100.9" {
			t.Errorf("trusted=%q: ClientIP = %q, want the peer", spec, got)
		}
	}
}

func TestClientIPTrustedChain(t *testing.T) {
	withTrustedProxies(t, "127.0.0.1/32, 10.0.0.0/8, ::1")
	cases := []struct {
		name, remote, xff, realIP, want string
	}{
		{"spoofed first entry", "127.0.0.1:80", "6.6.6.6, 203.0.113.5", "", "203.0.113.5"},
		{"skips trusted hops", "127.0.0.1:80", "6.6.6.6, 203.0.113.5, 10.0.0.2", "", "203.0.113.5"},
		{"several header lines", "127.0.0.1:80", "", "", "203.0.113.5"},
		{"hop with port", "127.0.0.1:80", "203.0.113.5:4711", "", "203.0.113.5"},
		{"ipv6 hop with port", "127.0.0.1:80", "[2001:db8::7]:443", "", "2001:db8::7"},
		{"blank hops skipped", "127.0.0.1:80", "203.0.113.5, ,", "", "203.0.113.5"},
		{"malformed hop stops the walk", "127.0.0.1:80", "203.0.113.5, ; DROP, 10.0.0.2", "198.51.100.1", "198.51.100.1"},
		{"malformed hop, no real ip", "127.0.0.1:80", "203.0.113.5, garbage", "", "127.0.0.1"},
		{"only trusted hops, real ip", "127.0.0.1:80", "10.0.0.3, 10.0.0.2", "198.51.100.1", "198.51.100.1"},
		{"no xff, real ip", "10.9.9.9:80", "", "198.51.100.1", "198.51.100.1"},
		{"garbage real ip", "10.9.9.9:80", "", "not-an-ip", "10.9.9.9"},
		{"mapped hop is unmapped", "127.0.0.1:80", "::ffff:203.0.113.5", "", "203.0.113.5"},
		{"mapped real ip is unmapped", "127.0.0.1:80", "", "::ffff:198.51.100.1", "198.51.100.1"},
		{"ipv6 trusted peer", "[::1]:1234", "2001:db8::1", "", "2001:db8::1"},
		{"mapped trusted peer", "[::ffff:10.0.0.5]:80", "203.0.113.5", "", "203.0.113.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := requestFrom(tc.remote, tc.xff, tc.realIP)
			if tc.name == "several header lines" {
				r.Header.Add("X-Forwarded-For", "6.6.6.6")
				r.Header.Add("X-Forwarded-For", "203.0.113.5, 10.0.0.2")
			}
			if got := ClientIP(r); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientIPPeerForms(t *testing.T) {
	withTrustedProxies(t, "")
	cases := map[string]string{
		"203.0.113.5:5555":     "203.0.113.5",
		"203.0.113.5":          "203.0.113.5",
		"[::1]:1234":           "::1",
		"[::ffff:1.2.3.4]:80":  "1.2.3.4",
		"[fe80::1%eth0]:80":    "fe80::1%eth0",
		"":                     "",
		"@":                    "@",
		"/run/mddb/mddb.sock":  "/run/mddb/mddb.sock",
		"[2001:DB8::0001]:443": "2001:db8::1",
	}
	for remote, want := range cases {
		if got := ClientIP(requestFrom(remote, "", "")); got != want {
			t.Errorf("RemoteAddr %q: ClientIP = %q, want %q", remote, got, want)
		}
	}
}

func TestIsTrustedProxyZonedPeer(t *testing.T) {
	withTrustedProxies(t, "fe80::/10")
	if got := ClientIP(requestFrom("[fe80::1%eth0]:80", "203.0.113.5", "")); got != "203.0.113.5" {
		t.Errorf("zoned trusted peer: ClientIP = %q", got)
	}
}

// TestRateLimitSpoofedForwardedForIsLimited is #366's reproduction: one peer
// rotating X-Forwarded-For must still land in one bucket.
func TestRateLimitSpoofedForwardedForIsLimited(t *testing.T) {
	withTrustedProxies(t, "")
	rl := setupRateLimiter(t, 5, 0, 60, "ip")
	h := rl.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	limited := 0
	for i := 1; i <= 50; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, requestFrom("127.0.0.1:40000", "10.0.0."+strconv.Itoa(i), "10.1.0."+strconv.Itoa(i)))
		if w.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited != 45 {
		t.Fatalf("%d of 50 requests limited, want 45 (5 allowed)", limited)
	}
}

// TestRateLimitTrustedProxyKeysOnClient: behind a trusted proxy the clients
// keep separate buckets.
func TestRateLimitTrustedProxyKeysOnClient(t *testing.T) {
	withTrustedProxies(t, "127.0.0.1")
	rl := setupRateLimiter(t, 1, 0, 60, "ip")
	h := rl.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for i := 1; i <= 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, requestFrom("127.0.0.1:40000", "203.0.113."+strconv.Itoa(i), ""))
		if w.Code != http.StatusOK {
			t.Fatalf("client %d: got %d, want 200", i, w.Code)
		}
	}
}

// TestClientIPOneSourceForAllKeys: the rate-limit key, the MCP key and caller,
// the SSE cap and the audit IP all come from ClientIP.
func TestClientIPOneSourceForAllKeys(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8")
	r := requestFrom("10.0.0.1:80", "6.6.6.6, 203.0.113.5, 10.0.0.2", "")
	const want = "203.0.113.5"

	if got := (&RateLimiter{by: "ip"}).httpClientID(r); got != "ip:"+want {
		t.Errorf("rate-limit key = %q", got)
	}
	if got := (&MCPRateLimiter{by: "ip"}).clientID(r); got != "ip:"+want {
		t.Errorf("MCP key = %q", got)
	}
	if got := mcpCallerFrom(withMCPCaller(context.Background(), r)).clientIP; got != want {
		t.Errorf("MCP caller ip = %q", got)
	}

	am, cleanup := newAuditTestManager(t, 90)
	defer cleanup()
	auth := &AuthManager{server: &Server{AuditManager: am}}
	auth.auditAuth(r, "mallory", "auth.login", "fail", "bad password")
	waitFlush(t, am, 1)
	events, err := am.Query(audit.QueryFilter{Limit: 10})
	if err != nil || len(events) != 1 || events[0].IP != want {
		t.Fatalf("audit events = %+v, %v; want IP %s", events, err, want)
	}
}

func TestGrpcPeerIPUsesTheSameRule(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8")
	ctxFrom := func(ip string, md metadata.MD) context.Context {
		ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 9000}})
		if md != nil {
			ctx = metadata.NewIncomingContext(ctx, md)
		}
		return ctx
	}
	cases := []struct {
		name   string
		peerIP string
		md     metadata.MD
		want   string
	}{
		{"untrusted peer ignores metadata", "198.51.100.9", metadata.Pairs("x-forwarded-for", "6.6.6.6", "x-real-ip", "7.7.7.7"), "198.51.100.9"},
		{"trusted peer, forwarded", "10.0.0.1", metadata.Pairs("x-forwarded-for", "6.6.6.6, 203.0.113.5"), "203.0.113.5"},
		{"trusted peer, real ip", "10.0.0.1", metadata.Pairs("x-real-ip", "203.0.113.6"), "203.0.113.6"},
		{"trusted peer, no metadata", "10.0.0.1", nil, "10.0.0.1"},
		{"ipv6 peer", "2001:db8::9", nil, "2001:db8::9"},
	}
	for _, tc := range cases {
		if got := grpcPeerIP(ctxFrom(tc.peerIP, tc.md)); got != tc.want {
			t.Errorf("%s: grpcPeerIP = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := grpcClientID(ctxFrom("198.51.100.9", nil), "ip"); got != "ip:198.51.100.9" {
		t.Errorf("gRPC rate-limit key = %q", got)
	}
}

func TestRunServerRejectsBadTrustedProxies(t *testing.T) {
	withClientAddressing(t)
	t.Setenv(trustedProxiesEnv, "10.0.0.0/8, proxy.internal")
	c := covAExpectFatal(t, func() { runServer(ServerConfig{}) })
	if c.msg != "startup step failed" || !strings.Contains(c.args, `"proxy.internal"`) || !strings.Contains(c.args, trustedProxiesEnv) {
		t.Fatalf("fatal = %+v", c)
	}
}
