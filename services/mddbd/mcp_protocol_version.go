package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"mddb/internal/envconf"
)

// MCPProtocolVersion is the MCP spec revision MDDB offers by default: the
// newest one it implements.
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
	MCPProtocolVersion,
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
	if mcpVersionSupported(requested) {
		return requested
	}
	return supportedMCPVersions[0]
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
			requested, strings.Join(supportedMCPVersions, ", "))
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
