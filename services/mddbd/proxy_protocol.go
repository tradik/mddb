package main

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

// proxyProtocolEnv names the switch for PROXY protocol v1/v2 on the TCP
// listeners (#366): off (the default) or on.
const proxyProtocolEnv = "MDDB_PROXY_PROTOCOL"

// proxyProtocolOn is set once at startup by configureProxyProtocol.
var proxyProtocolOn atomic.Bool

// proxyHeaderTimeout bounds how long a new connection may take to send its
// PROXY header, like http.Server's ReadHeaderTimeout. The header is read in
// the connection's own goroutine, never in the accept loop, so a slow peer
// holds only its own connection, and only this long. A variable so tests can
// shorten it.
var proxyHeaderTimeout = 10 * time.Second

// configureProxyProtocol parses v (the value of MDDB_PROXY_PROTOCOL): "on" or
// "off", any case; blank is off. Any other value is an error naming it.
func configureProxyProtocol(v string) error {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "off":
		proxyProtocolOn.Store(false)
	case "on":
		proxyProtocolOn.Store(true)
	default:
		return fmt.Errorf("%s: %q is not on or off", proxyProtocolEnv, v)
	}
	return nil
}

// withProxyProtocol wraps lis so connections may start with a PROXY header
// when MDDB_PROXY_PROTOCOL=on; otherwise lis is returned unchanged. After the
// header, the connection's RemoteAddr is the client the header names, so
// ClientIP needs nothing more. Wrap the raw listener, before TLS.
func withProxyProtocol(lis net.Listener) net.Listener {
	if !proxyProtocolOn.Load() {
		return lis
	}
	return &proxyproto.Listener{Listener: lis, ConnPolicy: proxyHeaderPolicy, ReadHeaderTimeout: proxyHeaderTimeout}
}

// proxyHeaderPolicy decides per connection, from the connecting peer:
//
//   - A peer in MDDB_TRUSTED_PROXIES must send a PROXY header (REQUIRE): a
//     load balancer that stops sending one is a misconfiguration, and
//     guessing would put every client behind it in one bucket. A LOCAL
//     header (a health check) keeps the peer's own address.
//   - Any other peer must not send one (REJECT): its connection fails on the
//     header instead of choosing its own address. Without a header it is
//     served as itself, so health checks and direct clients keep working.
func proxyHeaderPolicy(o proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
	if o.Upstream == nil {
		return proxyproto.REJECT, nil
	}
	ap, err := netip.ParseAddrPort(o.Upstream.String())
	if err == nil && isTrustedProxy(ap.Addr()) {
		return proxyproto.REQUIRE, nil
	}
	return proxyproto.REJECT, nil
}
