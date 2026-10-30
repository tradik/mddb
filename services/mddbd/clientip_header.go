package main

import (
	"fmt"
	"net/netip"
	"net/textproto"
	"slices"
	"strings"
	"sync/atomic"

	"golang.org/x/net/http/httpguts"
)

// clientIPHeaderEnv names the variable choosing the header a trusted proxy
// carries the client address in (#366). Unset means X-Forwarded-For.
const clientIPHeaderEnv = "MDDB_CLIENT_IP_HEADER"

// Header names with a meaning of their own.
const (
	xForwardedForHeader = "X-Forwarded-For"
	forwardedHeader     = "Forwarded" // RFC 7239
	xRealIPHeader       = "X-Real-IP"
)

// headerKind says how a client IP header is read.
type headerKind int

const (
	// kindForwardedFor is X-Forwarded-For: a chain of addresses, walked right
	// to left past trusted proxies, with X-Real-IP as the fallback.
	kindForwardedFor headerKind = iota
	// kindForwarded is RFC 7239 Forwarded: a chain of elements whose for=
	// nodes are walked like X-Forwarded-For.
	kindForwarded
	// kindSingle is any other header: one address set by the nearest proxy
	// (X-Real-IP, CF-Connecting-IP, True-Client-IP, X-Cluster-Client-Ip...).
	kindSingle
)

// clientIPHeader is the configured client IP header: its canonical HTTP name
// and how to read it. gRPC metadata is read under the lower-cased name.
type clientIPHeader struct {
	name string
	kind headerKind
}

// clientIPHeaderSetting holds the header installed at startup; nil means the
// default, X-Forwarded-For.
var clientIPHeaderSetting atomic.Pointer[clientIPHeader]

// parseClientIPHeader validates name (the value of MDDB_CLIENT_IP_HEADER) as
// an HTTP field name and classifies it. Blank means X-Forwarded-For.
func parseClientIPHeader(name string) (clientIPHeader, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = xForwardedForHeader
	}
	if !httpguts.ValidHeaderFieldName(name) {
		return clientIPHeader{}, fmt.Errorf("%s: %q is not a valid HTTP header name", clientIPHeaderEnv, name)
	}
	h := clientIPHeader{name: textproto.CanonicalMIMEHeaderKey(name), kind: kindSingle}
	switch {
	case strings.EqualFold(name, xForwardedForHeader):
		h.kind = kindForwardedFor
	case strings.EqualFold(name, forwardedHeader):
		h.kind = kindForwarded
	}
	return h, nil
}

// configureClientIPHeader installs name as the client IP header. On error the
// previous setting is kept.
func configureClientIPHeader(name string) error {
	h, err := parseClientIPHeader(name)
	if err != nil {
		return err
	}
	clientIPHeaderSetting.Store(&h)
	return nil
}

// currentClientIPHeader is the installed header, X-Forwarded-For by default.
func currentClientIPHeader() clientIPHeader {
	if h := clientIPHeaderSetting.Load(); h != nil {
		return *h
	}
	return clientIPHeader{name: xForwardedForHeader, kind: kindForwardedFor}
}

// present reports whether the request carries the header (or, for the
// default, its X-Real-IP fallback), for the untrusted-peer warning.
func (h clientIPHeader) present(values func(string) []string) bool {
	if len(values(h.name)) > 0 {
		return true
	}
	return h.kind == kindForwardedFor && len(values(xRealIPHeader)) > 0
}

// client reads the client address a trusted proxy put in the header:
//
//   - X-Forwarded-For: every line joined, walked right to left skipping
//     trusted proxies; if that finds nothing, X-Real-IP.
//   - Forwarded: the for= node of every element, walked the same way. An
//     element without for=, or with "unknown" or an obfuscated node, ends
//     the walk like a hop that is not an IP.
//   - Any other header: its last line, which must be one IP (a port is
//     allowed). The last line is the one the nearest proxy added.
func (h clientIPHeader) client(values func(string) []string) (netip.Addr, bool) {
	switch h.kind {
	case kindForwardedFor:
		if a, ok := rightmostUntrusted(strings.Split(strings.Join(values(h.name), ","), ",")); ok {
			return a, true
		}
		return lastIP(values(xRealIPHeader))
	case kindForwarded:
		return rightmostUntrusted(forwardedNodes(values(h.name)))
	default:
		return lastIP(values(h.name))
	}
}

// lastIP parses the last line of a single-value header as an address.
func lastIP(lines []string) (netip.Addr, bool) {
	if len(lines) == 0 {
		return netip.Addr{}, false
	}
	return parseHop(lines[len(lines)-1])
}

// forwardedNodes returns the address in the for= parameter of every element
// of the Forwarded header lines, in the order sent. A blank element gives ""
// (skipped by the walk); an element whose node is missing, "unknown" or
// obfuscated gives a value that is not an IP (ending the walk).
func forwardedNodes(lines []string) []string {
	elems := splitUnquoted(strings.Join(lines, ","), ',')
	nodes := make([]string, len(elems))
	for i, e := range elems {
		nodes[i] = forwardedFor(e)
	}
	return nodes
}

// forwardedFor extracts the node of the for= parameter of one Forwarded
// element, without its port and brackets.
func forwardedFor(elem string) string {
	if strings.TrimSpace(elem) == "" {
		return ""
	}
	for _, pair := range splitUnquoted(elem, ';') {
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), "for") {
			return nodeAddr(unquote(strings.TrimSpace(value)))
		}
	}
	return "unknown"
}

// unquote removes the quotes of an RFC 7230 quoted-string. Escapes are not
// expected in a node; one that is left in fails the IP parse later. A value
// opened but not closed by a quote is malformed and becomes "unknown".
func unquote(v string) string {
	if !strings.HasPrefix(v, `"`) {
		return v
	}
	if len(v) < 2 || !strings.HasSuffix(v, `"`) {
		return "unknown"
	}
	return v[1 : len(v)-1]
}

// nodeAddr strips the port from an RFC 7239 node: "[v6]:port" and "[v6]"
// give the IPv6 address, "v4:port" the IPv4 address. A port may be an
// obfuscated "_id". Anything else is returned as is for the IP parse.
func nodeAddr(node string) string {
	if strings.HasPrefix(node, "[") {
		end := strings.IndexByte(node, ']')
		if end < 0 || (end+1 < len(node) && node[end+1] != ':') {
			return node
		}
		return node[1:end]
	}
	if strings.Count(node, ":") == 1 {
		host, _, _ := strings.Cut(node, ":")
		return host
	}
	return node
}

// splitUnquoted splits s at every sep outside a quoted string. It scans from
// the right, so an unbalanced quote a client sent on the left cannot swallow
// the well-formed elements its proxies appended on the right.
func splitUnquoted(s string, sep byte) []string {
	var out []string
	inQuote := false
	end := len(s)
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case '"':
			inQuote = !inQuote
		case sep:
			if !inQuote {
				out = append(out, s[i+1:end])
				end = i
			}
		}
	}
	out = append(out, s[:end])
	slices.Reverse(out)
	return out
}
