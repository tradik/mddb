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
// and X-Forwarded-For / X-Real-IP are ignored on every request.
var trustedProxies atomic.Pointer[[]netip.Prefix]

// untrustedForwardWarning logs, once per process, that a forwarding header
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

// ClientIP is the address of the client that sent r, the single source for
// the rate-limit key, the MCP key and access log, the SSE per-IP cap and the
// audit trail. See resolveClientIP for the rule.
func ClientIP(r *http.Request) string {
	return resolveClientIP(r.RemoteAddr, r.Header.Values("X-Forwarded-For"), r.Header.Get("X-Real-IP"))
}

// grpcPeerIP is ClientIP for a gRPC call: the peer address plus the
// x-forwarded-for / x-real-ip metadata, under the same rule. A call without a
// peer is "unknown".
func grpcPeerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.Addr == nil {
		return "unknown"
	}
	md, _ := metadata.FromIncomingContext(ctx)
	realIP := ""
	if v := md.Get("x-real-ip"); len(v) > 0 {
		realIP = v[0]
	}
	return resolveClientIP(p.Addr.String(), md.Get("x-forwarded-for"), realIP)
}

// resolveClientIP picks the client address from a transport peer (remote,
// "host:port" or a bare host) and the forwarding headers:
//
//   - The peer is the client unless it is a trusted proxy; then both headers
//     are ignored, so a client cannot choose its own key or audit IP.
//   - From a trusted proxy, X-Forwarded-For is walked right to left skipping
//     trusted proxies; the first other address is the client. A hop that is
//     not an IP stops the walk.
//   - If that yields nothing, X-Real-IP (when it is an IP), then the peer.
//
// Addresses come back in canonical form with IPv4-mapped IPv6 unmapped. A peer
// that is not an IP (a Unix socket) is returned as is.
func resolveClientIP(remote string, forwardedFor []string, realIP string) string {
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	peerAddr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if !isTrustedProxy(peerAddr) {
		if len(forwardedFor) > 0 || realIP != "" {
			untrustedForwardWarning.Do(func() {
				slog.Warn("forwarding headers from a peer that is not a trusted proxy are ignored; set "+trustedProxiesEnv+" if mddbd runs behind a proxy",
					"peer", peerAddr.Unmap().String())
			})
		}
		return peerAddr.Unmap().String()
	}
	if a, ok := rightmostUntrusted(strings.Join(forwardedFor, ",")); ok {
		return a.String()
	}
	if a, ok := parseHop(realIP); ok {
		return a.Unmap().String()
	}
	return peerAddr.Unmap().String()
}

// rightmostUntrusted walks an X-Forwarded-For value from the right and
// returns the first hop that is not a trusted proxy. Blank hops are skipped;
// a hop that is not an IP ends the walk without a result.
func rightmostUntrusted(chain string) (netip.Addr, bool) {
	hops := strings.Split(chain, ",")
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
