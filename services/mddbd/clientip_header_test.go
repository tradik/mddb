package main

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// withClientAddressing saves the trusted proxies, the client IP header and
// the PROXY protocol switch, and restores them when the test ends.
func withClientAddressing(t *testing.T) {
	t.Helper()
	prevProxies := trustedProxies.Load()
	prevHeader := clientIPHeaderSetting.Load()
	prevPP := proxyProtocolOn.Load()
	t.Cleanup(func() {
		trustedProxies.Store(prevProxies)
		clientIPHeaderSetting.Store(prevHeader)
		proxyProtocolOn.Store(prevPP)
	})
}

// withClientIPHeader installs name as MDDB_CLIENT_IP_HEADER for one test.
func withClientIPHeader(t *testing.T, name string) {
	t.Helper()
	prev := clientIPHeaderSetting.Load()
	t.Cleanup(func() { clientIPHeaderSetting.Store(prev) })
	if err := configureClientIPHeader(name); err != nil {
		t.Fatalf("configureClientIPHeader(%q): %v", name, err)
	}
}

// mustAddr parses s as an IP address or fails the test.
func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// requestWith builds a request from remote carrying the header lines given
// as name, value pairs.
func requestWith(remote string, pairs ...string) *http.Request {
	r := requestFrom(remote, "", "")
	for i := 0; i+1 < len(pairs); i += 2 {
		r.Header.Add(pairs[i], pairs[i+1])
	}
	return r
}

func TestParseClientIPHeader(t *testing.T) {
	cases := []struct {
		in, name string
		kind     headerKind
	}{
		{"", "X-Forwarded-For", kindForwardedFor},
		{"  ", "X-Forwarded-For", kindForwardedFor},
		{"x-forwarded-for", "X-Forwarded-For", kindForwardedFor},
		{"FORWARDED", "Forwarded", kindForwarded},
		{"x-real-ip", "X-Real-Ip", kindSingle},
		{"CF-Connecting-IP", "Cf-Connecting-Ip", kindSingle},
		{"X-Cluster-Client-Ip", "X-Cluster-Client-Ip", kindSingle},
		{" Client-IP ", "Client-Ip", kindSingle},
	}
	for _, tc := range cases {
		h, err := parseClientIPHeader(tc.in)
		if err != nil || h.name != tc.name || h.kind != tc.kind {
			t.Errorf("%q: got %+v, %v; want %s kind %d", tc.in, h, err, tc.name, tc.kind)
		}
	}
}

func TestParseClientIPHeaderRejectsInvalidNames(t *testing.T) {
	for _, bad := range []string{"X Real IP", "X-Real-IP:", "a\r\nb", "Zażółć", "(x)", `"x"`} {
		_, err := parseClientIPHeader(bad)
		if err == nil {
			t.Errorf("%q: want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), clientIPHeaderEnv) || !strings.Contains(err.Error(), strings.TrimSpace(bad)[:1]) {
			t.Errorf("%q: error %q does not name the variable and the value", bad, err)
		}
	}
}

func TestConfigureClientIPHeaderKeepsSettingOnError(t *testing.T) {
	withClientIPHeader(t, "CF-Connecting-IP")
	if err := configureClientIPHeader("bad name"); err == nil {
		t.Fatal("want an error")
	}
	if got := currentClientIPHeader().name; got != "Cf-Connecting-Ip" {
		t.Errorf("previous header lost: %q", got)
	}
}

func TestCurrentClientIPHeaderDefault(t *testing.T) {
	withClientAddressing(t)
	clientIPHeaderSetting.Store(nil)
	if h := currentClientIPHeader(); h.name != xForwardedForHeader || h.kind != kindForwardedFor {
		t.Errorf("default = %+v", h)
	}
}

func TestClientIPForwardedHeader(t *testing.T) {
	withTrustedProxies(t, "127.0.0.1, 10.0.0.0/8")
	withClientIPHeader(t, "Forwarded")
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"single element", []string{"for=203.0.113.5"}, "203.0.113.5"},
		{"spoofed left, trusted right", []string{"for=6.6.6.6, for=203.0.113.5;proto=https;by=10.0.0.1, for=10.0.0.2"}, "203.0.113.5"},
		{"quoted ipv6 with port", []string{`for="[2001:db8:cafe::17]:4711"`}, "2001:db8:cafe::17"},
		{"quoted ipv6 without port", []string{`for="[2001:DB8::1]"`}, "2001:db8::1"},
		{"ipv4 with port, name in caps", []string{`For="203.0.113.5:8080"`}, "203.0.113.5"},
		{"obfuscated port", []string{`for="203.0.113.5:_p1"`}, "203.0.113.5"},
		{"obfuscated ipv6 port", []string{`for="[2001:db8::2]:_p1"`}, "2001:db8::2"},
		{"lenient bare ipv6", []string{"for=2001:db8::5"}, "2001:db8::5"},
		{"several header lines", []string{"for=6.6.6.6", "for=203.0.113.5", "for=10.0.0.2"}, "203.0.113.5"},
		{"blank elements", []string{", for=203.0.113.5,,"}, "203.0.113.5"},
		{"comma inside a quoted parameter", []string{`for=203.0.113.5;ext="a,b"`}, "203.0.113.5"},
		{"client's unbalanced quote stays on the left", []string{`for="6.6.6.6, for=203.0.113.5`}, "203.0.113.5"},
		{"mapped address unmapped", []string{`for="[::ffff:203.0.113.5]"`}, "203.0.113.5"},
		{"unknown ends the walk", []string{"for=203.0.113.5, for=unknown"}, "127.0.0.1"},
		{"obfuscated node ends the walk", []string{"for=203.0.113.5, for=_hidden"}, "127.0.0.1"},
		{"element without for ends the walk", []string{"for=203.0.113.5, proto=https;by=10.0.0.1"}, "127.0.0.1"},
		{"unterminated quote", []string{`for="203.0.113.5`}, "127.0.0.1"},
		{"lone quote", []string{`for="`}, "127.0.0.1"},
		{"bracket never closed", []string{`for="[2001:db8::1"`}, "127.0.0.1"},
		{"junk after bracket", []string{`for="[2001:db8::1]x"`}, "127.0.0.1"},
		{"only trusted hops", []string{"for=10.0.0.3, for=10.0.0.2"}, "127.0.0.1"},
		{"no header", nil, "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := requestFrom("127.0.0.1:80", "", "")
			for _, l := range tc.lines {
				r.Header.Add("Forwarded", l)
			}
			if got := ClientIP(r); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClientIPForwardedIgnoresOtherHeaders: with Forwarded configured,
// X-Forwarded-For and X-Real-IP are not read, and an untrusted peer's
// Forwarded is ignored.
func TestClientIPForwardedIgnoresOtherHeaders(t *testing.T) {
	withTrustedProxies(t, "127.0.0.1")
	withClientIPHeader(t, "Forwarded")
	if got := ClientIP(requestWith("127.0.0.1:80", "X-Forwarded-For", "203.0.113.5", "X-Real-IP", "203.0.113.6")); got != "127.0.0.1" {
		t.Errorf("trusted peer without Forwarded: ClientIP = %q", got)
	}
	if got := ClientIP(requestWith("198.51.100.9:80", "Forwarded", "for=203.0.113.5")); got != "198.51.100.9" {
		t.Errorf("untrusted peer: ClientIP = %q", got)
	}
}

func TestClientIPSingleValueHeader(t *testing.T) {
	withTrustedProxies(t, "127.0.0.1, ::1")
	withClientIPHeader(t, "CF-Connecting-IP")
	cases := []struct {
		name, remote string
		pairs        []string
		want         string
	}{
		{"address", "127.0.0.1:80", []string{"Cf-Connecting-Ip", "203.0.113.5"}, "203.0.113.5"},
		{"case of the name does not matter", "127.0.0.1:80", []string{"cf-connecting-ip", "203.0.113.5"}, "203.0.113.5"},
		{"ipv6", "[::1]:80", []string{"CF-Connecting-IP", "2001:DB8::9"}, "2001:db8::9"},
		{"with a port", "127.0.0.1:80", []string{"CF-Connecting-IP", "203.0.113.5:4711"}, "203.0.113.5"},
		{"last line wins", "127.0.0.1:80", []string{"CF-Connecting-IP", "6.6.6.6", "CF-Connecting-IP", "203.0.113.5"}, "203.0.113.5"},
		{"a list is not one address", "127.0.0.1:80", []string{"CF-Connecting-IP", "6.6.6.6, 203.0.113.5"}, "127.0.0.1"},
		{"garbage", "127.0.0.1:80", []string{"CF-Connecting-IP", "not-an-ip"}, "127.0.0.1"},
		{"other headers are not read", "127.0.0.1:80", []string{"X-Forwarded-For", "203.0.113.5", "X-Real-IP", "203.0.113.6"}, "127.0.0.1"},
		{"untrusted peer", "198.51.100.9:80", []string{"CF-Connecting-IP", "203.0.113.5"}, "198.51.100.9"},
		{"mapped", "127.0.0.1:80", []string{"CF-Connecting-IP", "::ffff:203.0.113.5"}, "203.0.113.5"},
	}
	for _, tc := range cases {
		if got := ClientIP(requestWith(tc.remote, tc.pairs...)); got != tc.want {
			t.Errorf("%s: ClientIP = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestClientIPRealIPAsConfiguredHeader: X-Real-IP named explicitly is a
// single-value header; X-Forwarded-For is then not read.
func TestClientIPRealIPAsConfiguredHeader(t *testing.T) {
	withTrustedProxies(t, "127.0.0.1")
	withClientIPHeader(t, "X-Real-IP")
	if got := ClientIP(requestFrom("127.0.0.1:80", "203.0.113.5", "203.0.113.6")); got != "203.0.113.6" {
		t.Errorf("ClientIP = %q, want the X-Real-IP value", got)
	}
	if got := ClientIP(requestFrom("127.0.0.1:80", "203.0.113.5", "")); got != "127.0.0.1" {
		t.Errorf("ClientIP = %q, want the peer", got)
	}
}

func TestClientIPHeaderPresent(t *testing.T) {
	values := func(m map[string][]string) func(string) []string {
		return func(name string) []string { return m[name] }
	}
	xff := clientIPHeader{name: xForwardedForHeader, kind: kindForwardedFor}
	single := clientIPHeader{name: "Cf-Connecting-Ip", kind: kindSingle}
	if !xff.present(values(map[string][]string{xRealIPHeader: {"1.2.3.4"}})) {
		t.Error("X-Real-IP alone must count for the default header")
	}
	if single.present(values(map[string][]string{xRealIPHeader: {"1.2.3.4"}})) {
		t.Error("X-Real-IP must not count for a single-value header")
	}
	if !single.present(values(map[string][]string{"Cf-Connecting-Ip": {"1.2.3.4"}})) {
		t.Error("the configured header must count")
	}
	if xff.present(values(nil)) {
		t.Error("no header must not count")
	}
}

func TestGrpcPeerIPConfiguredHeader(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8")
	ctxFrom := func(ip string, md metadata.MD) context.Context {
		ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 9000}})
		return metadata.NewIncomingContext(ctx, md)
	}

	withClientIPHeader(t, "CF-Connecting-IP")
	if got := grpcPeerIP(ctxFrom("10.0.0.1", metadata.Pairs("cf-connecting-ip", "203.0.113.5", "x-forwarded-for", "6.6.6.6"))); got != "203.0.113.5" {
		t.Errorf("single-value metadata: grpcPeerIP = %q", got)
	}
	if got := grpcPeerIP(ctxFrom("198.51.100.9", metadata.Pairs("cf-connecting-ip", "203.0.113.5"))); got != "198.51.100.9" {
		t.Errorf("untrusted peer: grpcPeerIP = %q", got)
	}

	withClientIPHeader(t, "Forwarded")
	if got := grpcPeerIP(ctxFrom("10.0.0.1", metadata.Pairs("forwarded", `for=6.6.6.6, for="[2001:db8::7]:443"`))); got != "2001:db8::7" {
		t.Errorf("Forwarded metadata: grpcPeerIP = %q", got)
	}
}

func TestConfigureClientAddressing(t *testing.T) {
	withClientAddressing(t)
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	logs := covACaptureLogs(t)
	if err := configureClientAddressing(env(map[string]string{
		trustedProxiesEnv: "10.0.0.0/8", clientIPHeaderEnv: "X-Cluster-Client-Ip", proxyProtocolEnv: "on",
	})); err != nil {
		t.Fatal(err)
	}
	if !isTrustedProxy(mustAddr(t, "10.1.2.3")) || currentClientIPHeader().name != "X-Cluster-Client-Ip" || !proxyProtocolOn.Load() {
		t.Fatal("settings not installed")
	}
	if logs.String() != "" {
		t.Errorf("a sound configuration logged:\n%s", logs.String())
	}

	if err := configureClientAddressing(env(map[string]string{trustedProxiesEnv: "10.0.0.1, 0.0.0.0/0"})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "0.0.0.0/0 or ::/0") || !strings.Contains(logs.String(), "10.0.0.1, 0.0.0.0/0") {
		t.Errorf("trust-everyone not warned:\n%s", logs.String())
	}
	if currentClientIPHeader().name != xForwardedForHeader || proxyProtocolOn.Load() {
		t.Error("unset variables must restore the defaults")
	}

	if err := configureClientAddressing(env(map[string]string{proxyProtocolEnv: "ON"})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "every PROXY header will be refused") {
		t.Errorf("PROXY protocol without trusted proxies not warned:\n%s", logs.String())
	}

	for name, bad := range map[string]map[string]string{
		trustedProxiesEnv: {trustedProxiesEnv: "proxy.local"},
		clientIPHeaderEnv: {clientIPHeaderEnv: "Bad Header"},
		proxyProtocolEnv:  {proxyProtocolEnv: "yes"},
	} {
		err := configureClientAddressing(env(bad))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v, want one naming the variable", name, err)
		}
	}
}

func TestRunServerRejectsBadClientIPHeader(t *testing.T) {
	withClientAddressing(t)
	t.Setenv(trustedProxiesEnv, "")
	t.Setenv(proxyProtocolEnv, "")
	t.Setenv(clientIPHeaderEnv, "X Forwarded For")
	c := covAExpectFatal(t, func() { runServer(ServerConfig{}) })
	if c.msg != "startup step failed" || !strings.Contains(c.args, `"X Forwarded For"`) || !strings.Contains(c.args, clientIPHeaderEnv) {
		t.Fatalf("fatal = %+v", c)
	}
}

func TestRunServerRejectsBadProxyProtocol(t *testing.T) {
	withClientAddressing(t)
	t.Setenv(trustedProxiesEnv, "")
	t.Setenv(clientIPHeaderEnv, "")
	t.Setenv(proxyProtocolEnv, "v2")
	c := covAExpectFatal(t, func() { runServer(ServerConfig{}) })
	if c.msg != "startup step failed" || !strings.Contains(c.args, `"v2"`) || !strings.Contains(c.args, proxyProtocolEnv) {
		t.Fatalf("fatal = %+v", c)
	}
}
