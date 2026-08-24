// Package main — Unix Domain Socket aware listener helpers.
//
// Callers pass a listen address string that may be either a classical
// `host:port` (TCP) or a `unix:/absolute/path.sock` form. openListener returns
// an appropriate net.Listener and takes care of stale-socket cleanup and
// filesystem permissions for UDS paths.
package main

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// unixScheme is the prefix marking a Unix Domain Socket listen address.
const unixScheme = "unix:"

// parseListenAddr inspects a listen address string and returns the network
// family and the address suitable for net.Listen.
//
// Accepted UDS forms:
//
//	unix:/tmp/mddb.sock       → ("unix", "/tmp/mddb.sock")
//	unix:///tmp/mddb.sock     → ("unix", "/tmp/mddb.sock")
//
// Anything else is passed through as "tcp" (preserving legacy :port and
// host:port semantics).
func parseListenAddr(addr string) (network, address string) {
	if !strings.HasPrefix(addr, unixScheme) {
		return "tcp", addr
	}
	path := strings.TrimPrefix(addr, unixScheme)
	// Tolerate unix:///path triple-slash form from URI parsers.
	path = strings.TrimPrefix(path, "//")
	return "unix", path
}

// isUnixAddr reports whether addr is a UDS listen address.
func isUnixAddr(addr string) bool {
	return strings.HasPrefix(addr, unixScheme)
}

// isLoopbackListenAddr reports whether a listen address is confined to the
// local host: a Unix domain socket, "localhost", or a loopback IP literal.
//
// A bare ":port" or "0.0.0.0:port" (all interfaces) and any routable host/IP
// are NOT loopback. Used to decide whether an unauthenticated listener is
// network-exposed (SEC-002).
func isLoopbackListenAddr(addr string) bool {
	if isUnixAddr(addr) {
		// UDS is filesystem-scoped, not reachable over the network.
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No host:port split (e.g. a bare hostname) — treat the whole string
		// as the host.
		host = addr
	}
	switch host {
	case "":
		// ":9000" → bound to every interface.
		return false
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// A non-IP hostname (external DNS name) is not loopback.
	return false
}

// openListener opens a TCP or UDS listener based on the scheme embedded in
// addr. For UDS it removes any stale socket file left by a previous run and
// restricts permissions to owner-only (0600).
//
// The caller owns the returned listener and is responsible for closing it.
// For UDS the socket file must also be removed on shutdown; closeListener
// performs that cleanup.
func openListener(addr string) (net.Listener, error) {
	network, address := parseListenAddr(addr)
	if network == "unix" {
		// Best-effort removal of a stale socket; ignore ENOENT.
		if err := os.Remove(address); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove stale socket %q: %w", address, err)
		}
		lis, err := net.Listen("unix", address)
		if err != nil {
			return nil, fmt.Errorf("listen unix %q: %w", address, err)
		}
		// Restrict to owner-only so the socket behaves as a single-user IPC
		// channel. Auth middleware still enforces API keys / JWT on top.
		// Platform-split: Windows cannot express this, and says so rather than
		// listening without it (WIN-002).
		if err := restrictSocketToOwner(address); err != nil {
			_ = lis.Close()
			_ = os.Remove(address)
			return nil, err
		}
		return lis, nil
	}
	return net.Listen("tcp", address)
}

// closeListener closes lis and, if addr is a UDS address, removes the socket
// file from the filesystem. Errors are returned so callers may log them but
// the listener is always closed even on removal failure.
func closeListener(lis net.Listener, addr string) error {
	err := lis.Close()
	if isUnixAddr(addr) {
		_, path := parseListenAddr(addr)
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			if err == nil {
				return fmt.Errorf("remove unix socket %q: %w", path, rmErr)
			}
		}
	}
	return err
}
