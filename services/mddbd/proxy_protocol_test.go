package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// withProxyProtocolOn turns MDDB_PROXY_PROTOCOL on with spec as the trusted
// proxies and a short header timeout, all restored when the test ends.
func withProxyProtocolOn(t *testing.T, spec string, headerTimeout time.Duration) {
	t.Helper()
	withClientAddressing(t)
	withTrustedProxies(t, spec)
	prevTimeout := proxyHeaderTimeout
	proxyHeaderTimeout = headerTimeout
	t.Cleanup(func() { proxyHeaderTimeout = prevTimeout })
	if err := configureProxyProtocol("on"); err != nil {
		t.Fatal(err)
	}
}

// serveClientIP serves an HTTP endpoint answering with ClientIP on a
// loopback listener wrapped by withProxyProtocol, optionally under TLS, and
// returns its address.
func serveClientIP(t *testing.T, tlsCfg *tls.Config) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wrapped := withProxyProtocol(lis)
	if tlsCfg != nil {
		wrapped = tls.NewListener(wrapped, tlsCfg)
	}
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, ClientIP(r)) }),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(wrapped) }()
	t.Cleanup(func() { _ = srv.Close() })
	return lis.Addr().String()
}

// proxyHeader builds a PROXY header of the given version for a TCP
// connection from src to dst.
func proxyHeader(t *testing.T, version byte, src, dst string) []byte {
	t.Helper()
	srcAddr, err := net.ResolveTCPAddr("tcp", src)
	if err != nil {
		t.Fatal(err)
	}
	dstAddr, err := net.ResolveTCPAddr("tcp", dst)
	if err != nil {
		t.Fatal(err)
	}
	proto := proxyproto.TCPv4
	if srcAddr.IP.To4() == nil {
		proto = proxyproto.TCPv6
	}
	h := &proxyproto.Header{Version: version, Command: proxyproto.PROXY, TransportProtocol: proto, SourceAddr: srcAddr, DestinationAddr: dstAddr}
	b, err := h.Format()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fetchClientIP dials addr, writes prefix (a PROXY header or nothing), sends
// one GET (over TLS when tlsOn) and returns the body. ok is false when the
// server closed the connection instead of answering.
func fetchClientIP(t *testing.T, addr string, prefix []byte, tlsOn bool, extra string) (string, bool) {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
	if len(prefix) > 0 {
		if _, err := raw.Write(prefix); err != nil {
			return "", false
		}
	}
	conn := raw
	if tlsOn {
		tc := tls.Client(raw, &tls.Config{InsecureSkipVerify: true}) // #nosec G402 -- test client against a self-signed test certificate
		if err := tc.Handshake(); err != nil {
			return "", false
		}
		conn = tc
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: mddb\r\n"+extra+"Connection: close\r\n\r\n"); err != nil {
		return "", false
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode == http.StatusOK
}

func TestConfigureProxyProtocol(t *testing.T) {
	withClientAddressing(t)
	for v, want := range map[string]bool{"": false, "off": false, "OFF": false, " on ": true, "On": true} {
		if err := configureProxyProtocol(v); err != nil || proxyProtocolOn.Load() != want {
			t.Errorf("%q: on=%v err=%v, want on=%v", v, proxyProtocolOn.Load(), err, want)
		}
	}
	for _, bad := range []string{"yes", "1", "true", "v2"} {
		err := configureProxyProtocol(bad)
		if err == nil || !strings.Contains(err.Error(), proxyProtocolEnv) || !strings.Contains(err.Error(), `"`+bad+`"`) {
			t.Errorf("%q: err = %v, want one naming the variable and the value", bad, err)
		}
	}
}

func TestWithProxyProtocolOffKeepsListener(t *testing.T) {
	withClientAddressing(t)
	proxyProtocolOn.Store(false)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lis.Close() }()
	if got := withProxyProtocol(lis); got != lis {
		t.Fatalf("off must return the listener unchanged, got %T", got)
	}
}

func TestProxyHeaderPolicy(t *testing.T) {
	withTrustedProxies(t, "10.0.0.0/8, fd00::/8")
	cases := []struct {
		name string
		addr net.Addr
		want proxyproto.Policy
	}{
		{"trusted ipv4", &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 1}, proxyproto.REQUIRE},
		{"trusted ipv6", &net.TCPAddr{IP: net.ParseIP("fd00::5"), Port: 1}, proxyproto.REQUIRE},
		{"untrusted", &net.TCPAddr{IP: net.ParseIP("198.51.100.1"), Port: 1}, proxyproto.REJECT},
		{"unix socket", &net.UnixAddr{Name: "/run/mddb.sock", Net: "unix"}, proxyproto.REJECT},
		{"no address", nil, proxyproto.REJECT},
	}
	for _, tc := range cases {
		got, err := proxyHeaderPolicy(proxyproto.ConnPolicyOptions{Upstream: tc.addr})
		if err != nil || got != tc.want {
			t.Errorf("%s: policy %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}

// TestProxyProtocolTrustedPeer: from a trusted peer, v1 and v2 headers for
// IPv4 and IPv6 clients set the client; no header refuses the connection.
func TestProxyProtocolTrustedPeer(t *testing.T) {
	withProxyProtocolOn(t, "127.0.0.1", 2*time.Second)
	addr := serveClientIP(t, nil)
	for _, tc := range []struct {
		version  byte
		src, dst string
	}{
		{1, "203.0.113.7:51000", "192.0.2.1:443"},
		{2, "203.0.113.8:51000", "192.0.2.1:443"},
		{1, "[2001:db8::7]:51000", "[2001:db8::1]:443"},
		{2, "[2001:db8::8]:51000", "[2001:db8::1]:443"},
	} {
		body, ok := fetchClientIP(t, addr, proxyHeader(t, tc.version, tc.src, tc.dst), false, "")
		want, _, _ := net.SplitHostPort(tc.src)
		if !ok || body != want {
			t.Errorf("v%d from %s: body %q ok=%v, want %q", tc.version, tc.src, body, ok, want)
		}
	}
	if body, ok := fetchClientIP(t, addr, nil, false, ""); ok {
		t.Errorf("a trusted peer without a header was served: %q", body)
	}
}

// TestProxyProtocolLocalCommand: a LOCAL header (a load balancer's health
// check) is accepted and the peer stays the client.
func TestProxyProtocolLocalCommand(t *testing.T) {
	withProxyProtocolOn(t, "127.0.0.1", 2*time.Second)
	addr := serveClientIP(t, nil)
	local, err := (&proxyproto.Header{Version: 2, Command: proxyproto.LOCAL}).Format()
	if err != nil {
		t.Fatal(err)
	}
	if body, ok := fetchClientIP(t, addr, local, false, ""); !ok || body != "127.0.0.1" {
		t.Errorf("LOCAL: body %q ok=%v", body, ok)
	}
}

// TestProxyProtocolUntrustedPeer: a peer outside MDDB_TRUSTED_PROXIES cannot
// send a header, and without one it is served as itself.
func TestProxyProtocolUntrustedPeer(t *testing.T) {
	withProxyProtocolOn(t, "10.0.0.0/8", 2*time.Second)
	addr := serveClientIP(t, nil)
	for _, v := range []byte{1, 2} {
		if body, ok := fetchClientIP(t, addr, proxyHeader(t, v, "203.0.113.7:1", "192.0.2.1:2"), false, ""); ok {
			t.Errorf("v%d header from an untrusted peer was served: %q", v, body)
		}
	}
	if body, ok := fetchClientIP(t, addr, nil, false, "X-Forwarded-For: 203.0.113.9\r\n"); !ok || body != "127.0.0.1" {
		t.Errorf("untrusted peer without a header: body %q ok=%v", body, ok)
	}
}

// TestProxyProtocolThenForwardedFor: when the PROXY header names another
// trusted proxy, the client IP header is read after it.
func TestProxyProtocolThenForwardedFor(t *testing.T) {
	withProxyProtocolOn(t, "127.0.0.1, 10.0.0.0/8", 2*time.Second)
	addr := serveClientIP(t, nil)
	body, ok := fetchClientIP(t, addr, proxyHeader(t, 2, "10.0.0.5:1", "192.0.2.1:2"), false, "X-Forwarded-For: 6.6.6.6, 203.0.113.9\r\n")
	if !ok || body != "203.0.113.9" {
		t.Errorf("body %q ok=%v, want the forwarded client", body, ok)
	}
}

// TestProxyProtocolUnderTLS: the header precedes the TLS handshake.
func TestProxyProtocolUnderTLS(t *testing.T) {
	withProxyProtocolOn(t, "127.0.0.1", 2*time.Second)
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg.NextProtos = nil
	addr := serveClientIP(t, tlsCfg)
	if body, ok := fetchClientIP(t, addr, proxyHeader(t, 2, "203.0.113.7:1", "192.0.2.1:2"), true, ""); !ok || body != "203.0.113.7" {
		t.Errorf("TLS after v2: body %q ok=%v", body, ok)
	}
	if body, ok := fetchClientIP(t, addr, nil, true, ""); ok {
		t.Errorf("TLS without a header from a trusted peer was served: %q", body)
	}
}

// TestProxyProtocolSlowPeer: a peer that never sends its header holds only
// its own connection, and only for the header timeout; others are served
// meanwhile.
func TestProxyProtocolSlowPeer(t *testing.T) {
	withProxyProtocolOn(t, "127.0.0.1", 300*time.Millisecond)
	addr := serveClientIP(t, nil)

	slow, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slow.Close() }()
	start := time.Now()
	var wg sync.WaitGroup
	var slowErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = slow.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, slowErr = slow.Read(make([]byte, 1))
	}()

	if body, ok := fetchClientIP(t, addr, proxyHeader(t, 1, "203.0.113.7:1", "192.0.2.1:2"), false, ""); !ok || body != "203.0.113.7" {
		t.Errorf("a client behind a slow peer: body %q ok=%v", body, ok)
	}
	wg.Wait()
	if ne, ok := slowErr.(net.Error); ok && ne.Timeout() {
		t.Fatal("the slow connection was held open past the header timeout")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("slow connection closed after %v", elapsed)
	}
}

// TestProxyProtocolGRPC: a gRPC call through a v2 header sees the header's
// client in grpcPeerIP, the way startGRPCServer serves; without the header a
// trusted peer is refused.
func TestProxyProtocolGRPC(t *testing.T) {
	withProxyProtocolOn(t, "127.0.0.1", 2*time.Second)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 1)
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		seen <- grpcPeerIP(ctx)
		return h(ctx, req)
	}))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(withProxyProtocol(lis)) }()
	defer srv.Stop()

	header := proxyHeader(t, 2, "[2001:db8::7]:1", "[2001:db8::1]:2")
	conn, err := grpc.NewClient("passthrough:///"+lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, a string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, "tcp", a)
			if err != nil {
				return nil, err
			}
			if _, err := c.Write(header); err != nil {
				_ = c.Close()
				return nil, err
			}
			return c, nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "2001:db8::7" {
		t.Errorf("grpcPeerIP = %q, want the PROXY header's client", got)
	}

	// A gRPC client that sends no header to a listener that requires one
	// never gets an answer.
	plain, err := grpc.NewClient("passthrough:///"+lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	short, cancelShort := context.WithTimeout(context.Background(), time.Second)
	defer cancelShort()
	if _, err := healthpb.NewHealthClient(plain).Check(short, &healthpb.HealthCheckRequest{}); err == nil {
		t.Error("a trusted peer without a header was served over gRPC")
	}
}
