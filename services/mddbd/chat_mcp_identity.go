package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	json "mddb/internal/jsonx"
)

// Forwarding the signed-in user's identity to an external MCP server (#414).
// A server with forwardIdentity gets, next to its own credential, a
// short-lived token for the chat user a call is made for:
//
//	token   = "v1." + payloadB64 + "." + signatureB64
//	payload = {"aud":"mcp","sub":…,"scenario":…,"server":…,"iat":…,"exp":…}
//	signatureB64 = base64url(HMAC-SHA256(key, "v1." + payloadB64)), no padding
//
// The key is a sealed MCP secret of its own, never the server's credential:
// a leaked request then shows one user's identity until exp, not the means
// to mint one. sub comes only from the caller — mddb-chat, which takes it
// from the verified join token — never from a tool's arguments. docs/API.md,
// "The MCP identity token", has the verification order and a test vector.

// Defaults and bounds of forwardIdentity.
const (
	chatMcpIdentityAudience      = "mcp"
	chatMcpIdentityHeaderDefault = "X-MDDB-Chat-Identity"
	chatMcpIdentityTTLDefault    = 120
	chatMcpIdentityTTLMin        = 10
	chatMcpIdentityTTLMax        = 300
	chatMcpIdentityKeyMinBytes   = 32
	chatMcpIdentityTokenVersion  = "v1"
)

// errChatMcpIdentityRequired refuses a call to a forwardIdentity server that
// names no user: it is never made without one.
var errChatMcpIdentityRequired = fmt.Errorf("%w: the server forwards the user's identity and the call names no signed-in user (sub)",
	errChatMcpNotAllowed)

// ChatMcpForwardIdentity is a server's identity forwarding; nil is off.
type ChatMcpForwardIdentity struct {
	SecretRef  string `json:"secretRef"`
	Header     string `json:"header"`
	TTLSeconds int    `json:"ttlSeconds"`
}

// normalize checks the setting of server, whose own credential is
// serverSecretRef, and fills its defaults.
func (f *ChatMcpForwardIdentity) normalize(server, serverSecretRef string) error {
	if err := validChatKeyName("mcp server "+server+": identity secret", f.SecretRef); err != nil {
		return err
	}
	if f.SecretRef == serverSecretRef {
		return fmt.Errorf("mcp server %s: the identity key must be a secret of its own, not the server's secretRef", server)
	}
	if f.Header == "" {
		f.Header = chatMcpIdentityHeaderDefault
	}
	canonical := http.CanonicalHeaderKey(f.Header)
	if !chatMcpHeaderName.MatchString(f.Header) || chatMcpReservedHeaders[canonical] || canonical == "Authorization" {
		return fmt.Errorf("mcp server %s: %q is not a header the identity may be sent in", server, f.Header)
	}
	return boundOrDefault("mcp server "+server+": forwardIdentity.ttlSeconds", &f.TTLSeconds,
		chatMcpIdentityTTLDefault, chatMcpIdentityTTLMin, chatMcpIdentityTTLMax)
}

// chatMcpIdentityClaims is the token's payload, in the documented order.
type chatMcpIdentityClaims struct {
	Aud      string `json:"aud"`
	Sub      string `json:"sub"`
	Scenario string `json:"scenario"`
	Server   string `json:"server"`
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
}

// signChatMcpIdentity encodes and signs claims with key.
func signChatMcpIdentity(key []byte, claims chatMcpIdentityClaims) string {
	payload, _ := json.Marshal(claims) // strings and numbers: it encodes
	signed := chatMcpIdentityTokenVersion + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(signed)) // a hash never fails to write
	return signed + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// chatMcpIdentityKey opens the signing key a secret holds, refused when it
// is shorter than chatMcpIdentityKeyMinBytes. Its value never appears in an
// error.
func (s *Server) chatMcpIdentityKey(ref string) ([]byte, error) {
	_, key, err := s.ChatScenarios.McpSecretHeader(ref)
	switch {
	case err != nil:
		return nil, err
	case len(key) < chatMcpIdentityKeyMinBytes:
		return nil, fmt.Errorf("identity secret %s is shorter than %d bytes", ref, chatMcpIdentityKeyMinBytes)
	}
	return []byte(key), nil
}

// checkChatMcpIdentityKey refuses a scenario whose server names an identity
// key that is missing or too short.
func (s *Server) checkChatMcpIdentityKey(m *ChatMcpServer) error {
	if m.ForwardIdentity == nil {
		return nil
	}
	if _, err := s.chatMcpIdentityKey(m.ForwardIdentity.SecretRef); err != nil {
		return fmt.Errorf("mcp server %s: %w", m.Name, err)
	}
	return nil
}

// admitChatMcpIdentity checks a call to a forwardIdentity server before
// anything is sent: it names a user, and the caller may act for users — the
// write permission on the chat collection that storing a user's
// conversation takes (#405) — so a reader of the scenario cannot mint
// another user's identity. A server without forwardIdentity passes.
func (s *Server) admitChatMcpIdentity(ctx context.Context, m *ChatMcpServer, req *chatMcpCallRequest) error {
	if m.ForwardIdentity == nil {
		return nil
	}
	if req.Sub == "" {
		return errChatMcpIdentityRequired
	}
	if err := validChatName("sub", req.Sub, true); err != nil {
		return err
	}
	return s.authorize(ctx, chatRule(true), []string{chatCollection(ctx)})
}

// chatMcpIdentityHeader is the header carrying a fresh identity token for
// req's user, nil for a server without forwardIdentity, plus the token, to
// be kept out of errors. The call is logged with its user, never with the
// token, the key or the arguments.
func (s *Server) chatMcpIdentityHeader(m *ChatMcpServer, req *chatMcpCallRequest) (http.Header, string, error) {
	f := m.ForwardIdentity
	if f == nil {
		return nil, "", nil
	}
	key, err := s.chatMcpIdentityKey(f.SecretRef)
	if err != nil {
		return nil, "", err
	}
	iat := s.ChatScenarios.now().Unix()
	token := signChatMcpIdentity(key, chatMcpIdentityClaims{Aud: chatMcpIdentityAudience, Sub: req.Sub,
		Scenario: req.Scenario, Server: m.Name, Iat: iat, Exp: iat + int64(f.TTLSeconds)})
	slog.Info("chat mcp call with the user's identity", "sub", req.Sub, "scenario", req.Scenario, "server", m.Name, "tool", req.Tool)
	return http.Header{http.CanonicalHeaderKey(f.Header): {token}}, token, nil
}

// mergeChatMcpIdentity adds the identity header to a server's credential
// header, refusing one that would replace it.
func mergeChatMcpIdentity(auth, identity http.Header) (http.Header, error) {
	if identity == nil {
		return auth, nil
	}
	out := http.Header{}
	for k, v := range auth {
		out[k] = v
	}
	for k, v := range identity {
		if _, clash := out[k]; clash {
			return nil, errors.New("the identity header " + k + " is the header of the server's own secret")
		}
		out[k] = v
	}
	return out, nil
}
