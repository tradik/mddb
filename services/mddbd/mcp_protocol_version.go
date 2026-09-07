package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"mddb/internal/envconf"
)

// MCPModernProtocolVersion is the newest revision MDDB implements: stateless,
// no handshake, every request carrying its own version and capabilities.
const MCPModernProtocolVersion = "2026-07-28"

// MCPProtocolVersion is the newest revision MDDB answers an `initialize`
// handshake with.
//
// It is deliberately NOT the newest revision overall. 2026-07-28 removed
// `initialize`, so a handshake cannot negotiate its way there: a client that
// asks for it over `initialize` is telling us it has not made the move, and
// naming a revision without a handshake in the handshake's own reply would
// leave it speaking a protocol it did not implement.
const MCPProtocolVersion = "2025-11-25"

// supportedMCPVersions lists every MCP spec revision this server can speak,
// newest first.
//
// It is a list rather than a constant because a client and a server do not
// upgrade on the same day. A client pinned to an older revision should keep
// working against a newer server, and that is only possible if the server
// knows which revisions it can still answer for. The first entry is what MDDB
// offers a client that asks for something it does not recognise.
var supportedMCPVersions = []string{
	MCPModernProtocolVersion,
	MCPProtocolVersion,
}

// modernMCPVersions are the revisions with no handshake: version, identity and
// capabilities arrive with each request instead of being established once.
//
// Membership of this set, rather than the ordering of the registry, is what
// decides how a revision is served. Every revision not named here is
// handshake-based, which keeps the rule stable when the registry is narrowed
// by a pin or replaced in a test.
var modernMCPVersions = map[string]bool{
	MCPModernProtocolVersion: true,
}

// mcpVersionIsLegacy reports whether a revision is handshake-based.
func mcpVersionIsLegacy(version string) bool {
	return !modernMCPVersions[version]
}

// legacyMCPVersionsServed returns the handshake revisions this server will
// answer for right now, newest first.
func legacyMCPVersionsServed() []string {
	var out []string
	for _, v := range activeMCPVersions() {
		if mcpVersionIsLegacy(v) {
			out = append(out, v)
		}
	}
	return out
}

// activeMCPVersions returns the revisions this server will answer for right
// now: every revision the build implements, or only the pinned one.
//
// The pin is what makes this a function rather than the slice above. An
// operator who pins a revision is saying the others must not be reachable, and
// that has to hold everywhere a version is named — `server/discover`, the
// version check on each request, and the handshake — not only in the one place
// negotiation used to happen.
func activeMCPVersions() []string {
	if pinned := PinnedMCPVersion(); pinned != "" {
		return []string{pinned}
	}
	return SupportedMCPVersions()
}

// mcpVersionServed reports whether this server will answer a request declaring
// a revision, with the operator's pin applied.
func mcpVersionServed(version string) bool {
	for _, v := range activeMCPVersions() {
		if v == version {
			return true
		}
	}
	return false
}

// mcpProtocolVersionEnv pins the revision, overriding negotiation entirely.
const mcpProtocolVersionEnv = "MDDB_MCP_PROTOCOL_VERSION"

// SupportedMCPVersions returns the revisions this build can speak, newest
// first. Returned as a copy: a caller rendering it into a diagnostic must not
// be able to reorder the registry.
func SupportedMCPVersions() []string {
	out := make([]string, len(supportedMCPVersions))
	copy(out, supportedMCPVersions)
	return out
}

// mcpVersionSupported reports whether this build can speak a revision.
func mcpVersionSupported(version string) bool {
	for _, v := range supportedMCPVersions {
		if v == version {
			return true
		}
	}
	return false
}

// PinnedMCPVersion returns the operator's forced revision, or "" when
// negotiation is left to run normally.
func PinnedMCPVersion() string {
	return strings.TrimSpace(envconf.String(mcpProtocolVersionEnv, ""))
}

// ValidateMCPProtocolVersion checks the pin at startup.
//
// An unknown pin is refused rather than ignored. Pretending to speak a
// revision MDDB does not implement would produce a handshake that succeeds and
// a session that then misbehaves in ways neither side can attribute — the
// failure would surface as a broken tool call, far from its cause.
func ValidateMCPProtocolVersion() error {
	pinned := PinnedMCPVersion()
	if pinned == "" || mcpVersionSupported(pinned) {
		return nil
	}
	return fmt.Errorf("%s=%q is not a revision this build implements; supported: %s",
		mcpProtocolVersionEnv, pinned, strings.Join(supportedMCPVersions, ", "))
}

// NegotiateMCPVersion decides which revision to answer a client with.
//
// The rule is the one the MCP spec prescribes: if the client asks for a
// revision the server speaks, the server agrees to it; otherwise the server
// names its own preferred revision and the client decides whether to continue.
// A client that asks for nothing gets the newest.
//
// A pin overrides all of that, because its whole purpose is to be
// non-negotiable: an operator sets it when a client's own version handling is
// the thing that is broken.
func NegotiateMCPVersion(requested string) string {
	if pinned := PinnedMCPVersion(); pinned != "" {
		return pinned
	}
	// Only handshake revisions are on the table here. `initialize` is how a
	// legacy client opens, and 2026-07-28 has no `initialize` to answer — so
	// naming it in a handshake reply would agree to a protocol neither side
	// would then be speaking.
	legacy := legacyMCPVersionsServed()
	if len(legacy) == 0 {
		return ""
	}
	for _, v := range legacy {
		if v == requested {
			return requested
		}
	}
	return legacy[0]
}

// LegacyHandshakeAvailable reports whether `initialize` can still be served —
// false when the operator pinned a revision that has no handshake.
func LegacyHandshakeAvailable() bool {
	return len(legacyMCPVersionsServed()) > 0
}

// requestedMCPVersion reads the client's asked-for revision out of an
// initialize request, tolerating its absence and a non-string value.
func requestedMCPVersion(params interface{}) string {
	m, ok := params.(map[string]interface{})
	if !ok {
		return ""
	}
	v, _ := m["protocolVersion"].(string)
	return v
}

// checkMCPVersionHeader validates an MCP-Protocol-Version header value.
//
// An absent header is fine: the header is optional, and a client that does not
// send one is asking for whatever the server prefers. A header naming a
// revision this build cannot speak is not fine, and neither is one that
// disagrees with an operator's pin — the pin exists precisely to stop a client
// negotiating its way somewhere else.
func checkMCPVersionHeader(header string) error {
	requested := strings.TrimSpace(header)
	if requested == "" {
		return nil
	}
	if pinned := PinnedMCPVersion(); pinned != "" {
		if requested != pinned {
			return fmt.Errorf("this server is pinned to MCP revision %s and cannot serve %s", pinned, requested)
		}
		return nil
	}
	if !mcpVersionSupported(requested) {
		return fmt.Errorf("unsupported MCP revision %s; this server speaks: %s",
			requested, strings.Join(SupportedMCPVersions(), ", "))
	}
	return nil
}

// mcpVersionErrorBody renders a version rejection as the JSON shape the rest
// of this transport's errors use.
func mcpVersionErrorBody(err error) string {
	b, mErr := json.Marshal(map[string]string{"error": err.Error()})
	if mErr != nil {
		return `{"error":"unsupported MCP protocol version"}`
	}
	return string(b)
}
