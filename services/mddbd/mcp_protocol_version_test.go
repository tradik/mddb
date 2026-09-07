package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withSupportedVersions replaces the revision registry for one test.
//
// The registry holds a single entry today, so every interesting case — a
// client on an older revision, an unknown revision, a pin to something other
// than the newest — is unreachable without this. MCP-001 will add a second
// entry, and these tests are what says the mechanism is already correct when
// it does.
func withSupportedVersions(t *testing.T, versions ...string) {
	t.Helper()
	original := supportedMCPVersions
	supportedMCPVersions = versions
	t.Cleanup(func() { supportedMCPVersions = original })
}

// NegotiateMCPVersion answers `initialize`, so the revisions it may choose
// from are the ones that have an `initialize` to answer. 2026-07-28 removed
// the handshake: naming it here would agree, in a handshake, to a protocol
// with no handshake in it.
func TestTheHandshakeOffersTheNewestRevisionThatHasOne(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")

	if got := NegotiateMCPVersion(""); got != "2025-11-25" {
		t.Errorf("a client that asks for nothing got %q, want the newest handshake revision", got)
	}
}

func TestAClientOnAnOlderRevisionIsAgreedWith(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")

	if got := NegotiateMCPVersion("2025-11-25"); got != "2025-11-25" {
		t.Errorf("got %q, want the revision the client asked for", got)
	}
}

// The spec's rule: a server that cannot speak what was asked names its own
// preference and lets the client decide, rather than failing the handshake.
func TestAnUnknownRevisionFallsBackToTheNewest(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")

	if got := NegotiateMCPVersion("2024-01-01"); got != "2025-11-25" {
		t.Errorf("got %q, want the server's own preference among handshake revisions", got)
	}
}

// A client that sends `initialize` asking for a stateless revision has its era
// handling wrong. It is answered with a revision it can actually complete a
// handshake in, rather than with the one it named.
func TestTheHandshakeWillNotAgreeToAStatelessRevision(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")

	if got := NegotiateMCPVersion("2026-07-28"); got != "2025-11-25" {
		t.Errorf("got %q, want a revision that has a handshake", got)
	}
}

// Pinning the stateless revision takes the handshake off the table entirely,
// and `initialize` has to say so — a legacy client has no way to fall forward,
// so this error is the only diagnostic its user will see.
func TestPinningTheStatelessRevisionRefusesTheHandshake(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")
	t.Setenv(mcpProtocolVersionEnv, "2026-07-28")

	if LegacyHandshakeAvailable() {
		t.Fatal("a server pinned to a handshake-less revision still offers a handshake")
	}

	h := &MCPHandler{logLevel: MCPLogWarning}
	resp := h.Handle(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{"protocolVersion": "2025-11-25"},
	})
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("initialize was served by a server that cannot serve it: %v", resp)
	}
	if code, _ := errObj["code"].(int); code != mcpErrUnsupportedProtocolVersion {
		t.Errorf("code = %v, want %d", errObj["code"], mcpErrUnsupportedProtocolVersion)
	}
	data, _ := errObj["data"].(map[string]interface{})
	supported, _ := data["supported"].([]string)
	if len(supported) != 1 || supported[0] != "2026-07-28" {
		t.Errorf("supported = %v, want the pinned revision named so the operator can see it", data["supported"])
	}
}

func TestAPinOverridesWhateverTheClientAsksFor(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")
	t.Setenv(mcpProtocolVersionEnv, "2025-11-25")

	for _, requested := range []string{"", "2026-07-28", "2024-01-01"} {
		if got := NegotiateMCPVersion(requested); got != "2025-11-25" {
			t.Errorf("requested %q: got %q, want the pin — a pin exists to be non-negotiable", requested, got)
		}
	}
}

func TestSurroundingWhitespaceInThePinIsIgnored(t *testing.T) {
	t.Setenv(mcpProtocolVersionEnv, "  "+MCPProtocolVersion+"  ")

	if err := ValidateMCPProtocolVersion(); err != nil {
		t.Errorf("a padded pin was rejected: %v", err)
	}
	if got := NegotiateMCPVersion(""); got != MCPProtocolVersion {
		t.Errorf("got %q, want the trimmed pin", got)
	}
}

func TestAnEmptyPinLeavesNegotiationAlone(t *testing.T) {
	t.Setenv(mcpProtocolVersionEnv, "   ")

	if PinnedMCPVersion() != "" {
		t.Error("whitespace was read as a pin")
	}
	if err := ValidateMCPProtocolVersion(); err != nil {
		t.Errorf("no pin should validate cleanly: %v", err)
	}
}

func TestAPinToAnUnimplementedRevisionIsRefused(t *testing.T) {
	t.Setenv(mcpProtocolVersionEnv, "2099-01-01")

	err := ValidateMCPProtocolVersion()
	if err == nil {
		t.Fatal("a pin to a revision this build cannot speak was accepted")
	}
	// The operator has to be told what they can pin to, or the error is a dead end.
	if !strings.Contains(err.Error(), MCPProtocolVersion) {
		t.Errorf("error %q does not name a revision that would work", err)
	}
}

func TestValidateAcceptsNoPinAndAKnownPin(t *testing.T) {
	if err := ValidateMCPProtocolVersion(); err != nil {
		t.Errorf("unset: %v", err)
	}
	t.Setenv(mcpProtocolVersionEnv, MCPProtocolVersion)
	if err := ValidateMCPProtocolVersion(); err != nil {
		t.Errorf("known pin: %v", err)
	}
}

func TestTheVersionHeaderIsOptional(t *testing.T) {
	if err := checkMCPVersionHeader(""); err != nil {
		t.Errorf("an absent header was rejected: %v", err)
	}
	if err := checkMCPVersionHeader("  "); err != nil {
		t.Errorf("a blank header was rejected: %v", err)
	}
}

func TestTheVersionHeaderIsCheckedAgainstTheRegistry(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")

	if err := checkMCPVersionHeader("2025-11-25"); err != nil {
		t.Errorf("a supported revision was rejected: %v", err)
	}
	err := checkMCPVersionHeader("2024-01-01")
	if err == nil {
		t.Fatal("an unsupported revision was accepted")
	}
	if !strings.Contains(err.Error(), "2026-07-28") {
		t.Errorf("error %q does not say what the server can speak", err)
	}
}

func TestTheVersionHeaderMustMatchThePin(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")
	t.Setenv(mcpProtocolVersionEnv, "2025-11-25")

	if err := checkMCPVersionHeader("2025-11-25"); err != nil {
		t.Errorf("the pinned revision was rejected: %v", err)
	}
	// Supported, but not what this server was pinned to: still refused, or the
	// pin would be negotiable after all.
	if err := checkMCPVersionHeader("2026-07-28"); err == nil {
		t.Error("a client negotiated its way past the pin")
	}
}

func TestTheRegistryCannotBeReorderedByACaller(t *testing.T) {
	got := SupportedMCPVersions()
	if len(got) == 0 {
		t.Fatal("no revisions are advertised")
	}
	got[0] = "tampered"

	if SupportedMCPVersions()[0] == "tampered" {
		t.Error("the returned slice aliases the registry")
	}
}

func TestTheRequestedRevisionIsReadDefensively(t *testing.T) {
	cases := []struct {
		name   string
		params interface{}
		want   string
	}{
		{"absent params", nil, ""},
		{"params of the wrong shape", "not an object", ""},
		{"no protocolVersion", map[string]interface{}{"capabilities": map[string]interface{}{}}, ""},
		{"protocolVersion is not a string", map[string]interface{}{"protocolVersion": 20251125}, ""},
		{"a version", map[string]interface{}{"protocolVersion": "2025-11-25"}, "2025-11-25"},
	}
	for _, c := range cases {
		if got := requestedMCPVersion(c.params); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTheRejectionBodyIsJSON(t *testing.T) {
	body := mcpVersionErrorBody(checkMCPVersionHeaderErr(t))

	var decoded map[string]string
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("body %q is not JSON: %v", body, err)
	}
	if decoded["error"] == "" {
		t.Error("the rejection carries no reason")
	}
}

func checkMCPVersionHeaderErr(t *testing.T) error {
	t.Helper()
	err := checkMCPVersionHeader("2024-01-01")
	if err == nil {
		t.Fatal("expected a rejection to render")
	}
	return err
}

// The handshake is where negotiation becomes visible to a client, so it is
// tested through the handler rather than only through NegotiateMCPVersion.
func TestTheHandshakeAnswersWithTheNegotiatedRevision(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")
	h := &MCPHandler{logLevel: MCPLogWarning}

	initialize := func(params map[string]interface{}) string {
		t.Helper()
		req := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize"}
		if params != nil {
			req["params"] = params
		}
		resp := h.Handle(req)
		result, ok := resp["result"].(map[string]interface{})
		if !ok {
			t.Fatalf("initialize returned no result: %v", resp)
		}
		version, _ := result["protocolVersion"].(string)
		return version
	}

	if got := initialize(map[string]interface{}{"protocolVersion": "2025-11-25"}); got != "2025-11-25" {
		t.Errorf("client asked for 2025-11-25 and was answered %q", got)
	}
	if got := initialize(map[string]interface{}{"protocolVersion": "2024-01-01"}); got != "2025-11-25" {
		t.Errorf("client asked for an unknown revision and was answered %q, want the newest handshake revision", got)
	}
	if got := initialize(map[string]interface{}{}); got != "2025-11-25" {
		t.Errorf("client asked for nothing and was answered %q, want the newest handshake revision", got)
	}
	if got := initialize(nil); got != "2025-11-25" {
		t.Errorf("client sent no params and was answered %q, want the newest handshake revision", got)
	}
}

// The transport documented this header for a year and never read it.
func TestTheTransportRejectsAnUnspeakableRevision(t *testing.T) {
	withSupportedVersions(t, "2025-11-25")
	tr := NewMCPStreamableTransport(&MCPHandler{logLevel: MCPLogWarning})

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("MCP-Protocol-Version", "2024-01-01")
	rec := httptest.NewRecorder()
	tr.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "2025-11-25") {
		t.Errorf("rejection %q does not tell the client what would work", rec.Body.String())
	}
}

func TestTheTransportAcceptsASpeakableRevision(t *testing.T) {
	withSupportedVersions(t, "2025-11-25")
	tr := NewMCPStreamableTransport(&MCPHandler{logLevel: MCPLogWarning})

	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	rec := httptest.NewRecorder()
	tr.Handle(rec, req)

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("a supported revision was refused: %s", rec.Body.String())
	}
}

// An operator has to be able to see which revision is in force without running
// a handshake, because a handshake answers for one client rather than for the
// server: it cannot show that a pin is in force or what else was on offer.
func TestTheConfigReportCarriesTheRevision(t *testing.T) {
	withSupportedVersions(t, "2026-07-28", "2025-11-25")
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	report := func() MCPProtocolStatus {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		var decoded struct {
			Protocols struct {
				MCP MCPProtocolStatus `json:"mcp"`
			} `json:"protocols"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return decoded.Protocols.MCP
	}

	got := report()
	if got.Revision != "2026-07-28" {
		t.Errorf("revision = %q, want the newest supported", got.Revision)
	}
	if len(got.SupportedRevisions) != 2 {
		t.Errorf("supportedRevisions = %v, want both", got.SupportedRevisions)
	}
	if got.RevisionPinned {
		t.Error("nothing is pinned, but the report says otherwise")
	}

	t.Setenv(mcpProtocolVersionEnv, "2025-11-25")
	got = report()
	if got.Revision != "2025-11-25" || !got.RevisionPinned {
		t.Errorf("with a pin: revision = %q, pinned = %v; want the pin reported as such",
			got.Revision, got.RevisionPinned)
	}
}

// A build with no handshake revision left has nothing to answer `initialize`
// with, and says so by naming nothing rather than by inventing a revision.
func TestNegotiationHasNothingToOfferWithoutAHandshakeRevision(t *testing.T) {
	withSupportedVersions(t, MCPModernProtocolVersion)

	if got := NegotiateMCPVersion("2025-11-25"); got != "" {
		t.Errorf("got %q, want no revision at all", got)
	}
	if LegacyHandshakeAvailable() {
		t.Error("a handshake was offered by a build that implements none")
	}
}
