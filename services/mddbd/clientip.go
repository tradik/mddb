package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// trustedProxiesEnv names the variable listing the proxies whose forwarding
// headers mddbd believes (#366).
const trustedProxiesEnv = "MDDB_TRUSTED_PROXIES"

// trustedProxies holds the networks parsed from MDDB_TRUSTED_PROXIES. It is
// set once at startup (configureTrustedProxies); nil means no proxy is trusted
// and every client IP header is ignored on every request.
var trustedProxies atomic.Pointer[[]netip.Prefix]

// untrustedForwardWarning logs, once per process, that a client IP header
// arrived from a peer that is not a trusted proxy and was ignored.
var untrustedForwardWarning sync.Once

// parseTrustedProxies parses a comma-separated list of IP addresses and CIDR
// prefixes. Blank entries are skipped; any other entry that is neither an IP
// nor a prefix is an error naming that entry. IPv4-mapped IPv6 entries are
// stored as plain IPv4 so they match the unmapped peer address.
func parseTrustedProxies(spec string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, raw := range strings.Split(spec, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		p, err := parseProxyEntry(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not an IP address or CIDR prefix", trustedProxiesEnv, entry)
		}
		out = append(out, p)
	}
	return out, nil
}

// parseProxyEntry turns one MDDB_TRUSTED_PROXIES entry into a masked prefix;
// a bare address becomes a single-host prefix.
func parseProxyEntry(entry string) (netip.Prefix, error) {
	if !strings.Contains(entry, "/") {
		a, err := netip.ParseAddr(entry)
		if err != nil {
			return netip.Prefix{}, err
		}
		a = a.Unmap().WithZone("")
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(entry)
	if err != nil {
		return netip.Prefix{}, err
	}
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
	}
	return p.Masked(), nil
}

// configureTrustedProxies parses spec (the value of MDDB_TRUSTED_PROXIES) and
// installs it for ClientIP. On error the previous list is kept.
func configureTrustedProxies(spec string) error {
	prefixes, err := parseTrustedProxies(spec)
	if err != nil {
		return err
	}
	trustedProxies.Store(&prefixes)
	return nil
}

// isTrustedProxy reports whether a lies in one of the configured proxy
// networks. Zones and the IPv4-in-IPv6 mapping are ignored for the match.
func isTrustedProxy(a netip.Addr) bool {
	list := trustedProxies.Load()
	if list == nil {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range *list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// trustsEveryone reports whether list holds a prefix that matches every
// address of its family (0.0.0.0/0 or ::/0), which makes every client a
// trusted proxy again.
func trustsEveryone(list []netip.Prefix) bool {
	for _, p := range list {
		if p.Bits() == 0 {
			return true
		}
	}
	return false
}

// configureClientAddressing reads every client-address setting through getenv
// (MDDB_TRUSTED_PROXIES, MDDB_CLIENT_IP_HEADER, MDDB_PROXY_PROTOCOL), installs
// it, and warns about combinations that undo the protection. The first bad
// value is returned as an error naming the variable and the value.
func configureClientAddressing(getenv func(string) string) error {
	if err := configureTrustedProxies(getenv(trustedProxiesEnv)); err != nil {
		return err
	}
	if err := configureClientIPHeader(getenv(clientIPHeaderEnv)); err != nil {
		return err
	}
	if err := configureProxyProtocol(getenv(proxyProtocolEnv)); err != nil {
		return err
	}
	list := *trustedProxies.Load()
	if trustsEveryone(list) {
		slog.Warn(trustedProxiesEnv+" contains 0.0.0.0/0 or ::/0: every client is a trusted proxy and can choose its own IP for rate limits and the audit log; list the proxies' addresses instead",
			"value", getenv(trustedProxiesEnv))
	}
	if proxyProtocolOn.Load() && len(list) == 0 {
		slog.Warn(proxyProtocolEnv + "=on but " + trustedProxiesEnv + " is empty: every PROXY header will be refused; list the load balancer's addresses")
	}
	return nil
}

// ClientIP is the address of the client that sent r, the single source for
// the rate-limit key, the MCP key and access log, the SSE per-IP cap and the
// audit trail. See resolveClientIP for the rule.
func ClientIP(r *http.Request) string {
	return resolveClientIP(r.RemoteAddr, r.Header.Values)
}

// grpcPeerIP is ClientIP for a gRPC call: the peer address plus the
// configured header, read from the metadata under its lower-case key, with
// the same rule. A call without a peer is "unknown".
func grpcPeerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.Addr == nil {
		return "unknown"
	}
	md, _ := metadata.FromIncomingContext(ctx)
	return resolveClientIP(p.Addr.String(), md.Get)
}

// resolveClientIP picks the client address from a transport peer (remote,
// "host:port" or a bare host) and the request headers, read through values
// (every line of one header, by name):
//
//   - The peer is the client unless it is a trusted proxy; then every header
//     is ignored, so a client cannot choose its own key or audit IP.
//   - From a trusted proxy, the header named by MDDB_CLIENT_IP_HEADER is read
//     (see clientIPHeader.client); if it yields nothing, the peer.
//
// Addresses come back in canonical form with IPv4-mapped IPv6 unmapped. A peer
// that is not an IP (a Unix socket) is returned as is.
func resolveClientIP(remote string, values func(string) []string) string {
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	peerAddr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	header := currentClientIPHeader()
	if !isTrustedProxy(peerAddr) {
		if header.present(values) {
			untrustedForwardWarning.Do(func() {
				slog.Warn("client IP headers from a peer that is not a trusted proxy are ignored; set "+trustedProxiesEnv+" if mddbd runs behind a proxy",
					"peer", peerAddr.Unmap().String(), "header", header.name)
			})
		}
		return peerAddr.Unmap().String()
	}
	if a, ok := header.client(values); ok {
		return a.Unmap().String()
	}
	return peerAddr.Unmap().String()
}

// rightmostUntrusted walks forwarding hops (in the order sent) from the right
// and returns the first hop that is not a trusted proxy. Blank hops are
// skipped; a hop that is not an IP ends the walk without a result.
func rightmostUntrusted(hops []string) (netip.Addr, bool) {
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		a, ok := parseHop(hop)
		if !ok {
			return netip.Addr{}, false
		}
		if !isTrustedProxy(a) {
			return a.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

// parseHop parses one forwarded address: a bare IP, or an IP with a port
// ("1.2.3.4:80", "[::1]:80") as some proxies write it.
func parseHop(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		return a, true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	return netip.Addr{}, false
}
