// Package httpclient — SSRF protection for outbound HTTP (SEC-004).
//
// Webhooks, import-url, bulk callbacks and automation triggers all dial URLs
// supplied by users. Without address checks those become Server-Side Request
// Forgery vectors: reading cloud-metadata (169.254.169.254), hitting internal
// admin panels, or port-scanning the cluster. SafeDialContext resolves the
// host and refuses private/loopback/link-local targets, then dials the
// already-resolved IP to defeat DNS rebinding. validateOutboundURL re-applies
// the same policy on each redirect hop.
//
// Internal service clients (embedding providers / Ollama) build their OWN
// http.Client and do NOT go through the shared pooled transport, so legitimate
// private-network calls keep working. Operators on trusted intranets can opt
// out with MDDB_OUTBOUND_ALLOW_PRIVATE=true or allowlist specific hosts via
// MDDB_OUTBOUND_ALLOWLIST=host1,host2.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var errSSRFBlocked = errors.New("destination address is not allowed (private/loopback/link-local)")

// outboundAllowPrivate reports whether private/loopback destinations are
// explicitly permitted (intranet deployments).
func outboundAllowPrivate() bool {
	return os.Getenv("MDDB_OUTBOUND_ALLOW_PRIVATE") == "true"
}

// outboundAllowlistHas reports whether host is in MDDB_OUTBOUND_ALLOWLIST.
func outboundAllowlistHas(host string) bool {
	raw := os.Getenv("MDDB_OUTBOUND_ALLOWLIST")
	if raw == "" {
		return false
	}
	host = strings.ToLower(host)
	for _, h := range strings.Split(raw, ",") {
		if strings.ToLower(strings.TrimSpace(h)) == host && host != "" {
			return true
		}
	}
	return false
}

// hostExempt reports whether SSRF checks should be skipped for host.
func hostExempt(host string) bool {
	return outboundAllowPrivate() || outboundAllowlistHas(host)
}

// extraDenyCIDRs covers ranges net.IP's predicates miss (SEC-011): RFC 6598
// CGNAT 100.64.0.0/10 (cloud/k8s fabrics put node & service endpoints there),
// RFC 6890 192.0.0.0/24, RFC 2544 benchmarking 198.18.0.0/15 and the limited
// broadcast address.
var extraDenyCIDRs = func() []*net.IPNet {
	cidrs := []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "255.255.255.255/32"}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("httpclient: bad deny CIDR " + c) // unreachable: literals above
		}
		out = append(out, n)
	}
	return out
}()

// isDisallowedIP reports whether ip is in a range that outbound user requests
// must not reach.
func isDisallowedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, n := range extraDenyCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// SafeDialContext is a net.Dialer DialContext that blocks SSRF targets.
func SafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if hostExempt(host) {
		return dialer.DialContext(ctx, network, addr)
	}

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if isDisallowedIP(ip) {
			return nil, errSSRFBlocked
		}
	}
	// Dial the already-resolved IP so a rebinding attack can't swap in a
	// private address between the lookup and the connect.
	return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// validateOutboundURL re-checks a (possibly redirect) URL's host against the
// SSRF policy. Literal IPs are checked directly; hostnames are resolved.
func validateOutboundURL(u *url.URL) error {
	if u == nil {
		return errSSRFBlocked
	}
	host := u.Hostname()
	if hostExempt(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedIP(ip) {
			return errSSRFBlocked
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if isDisallowedIP(ip) {
			return errSSRFBlocked
		}
	}
	return nil
}

// ValidateOutboundURL checks a destination before a request is made.
//
// The pooled transport already refuses these addresses at dial time, so this
// is defence in depth rather than the only guard — but it is worth having for
// two reasons beyond redundancy. It fails with "destination address is not
// allowed" instead of a dial error that reads like a network problem, and it
// makes the check visible at the call site: a reader (and a static analyser)
// can see the URL being validated, where a dialer buried in a transport is
// invisible to both. CodeQL's go/request-forgery reported exactly that
// blindness as three critical findings on guarded code.
// It returns the URL to use rather than only an error. That is the difference
// between a check and a barrier: a caller that validates and then reaches for
// the original variable has validated nothing, and an error-only signature
// does not stop them. Returning the value makes the unvalidated string
// impossible to use by accident — and it is also the shape a taint tracker can
// follow, where "call this, then check err" is not.
func ValidateOutboundURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("URL must be http or https, got %q", u.Scheme)
	}
	if err := validateOutboundURL(u); err != nil {
		return "", err
	}
	return u.String(), nil
}

// ssrfCheckRedirect is the http.Client.CheckRedirect that re-validates every
// redirect hop and caps the chain length.
func ssrfCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	return validateOutboundURL(req.URL)
}
