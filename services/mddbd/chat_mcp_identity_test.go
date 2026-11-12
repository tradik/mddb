package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/proto"
)

// #414: the identity token mddbd signs for a forwardIdentity server.

// The documented test vector (docs/API.md, "The MCP identity token"),
// computed independently with Python's hmac module.
const (
	identityVectorKey   = "0123456789abcdef0123456789abcdef"
	identityVectorToken = "v1.eyJhdWQiOiJtY3AiLCJzdWIiOiJ1c2VyLTQyIiwic2NlbmFyaW8iOiJzdXBwb3J0Iiwic2VydmVyIjoiYWNjb3VudCIsImlhdCI6" +
		"MTc2NzIyNTYwMCwiZXhwIjoxNzY3MjI1NzIwfQ.hlY9TBl5ZxNybqBHuIRuWJI5jDKgH6N0GDHDj6fGaog"
)

// verifyIdentityToken is a host's verifier, in the documented order: three
// parts, v1, a constant-time HMAC, then the payload — aud "mcp", exp in the
// future and at most 300 s ahead, sub present.
func verifyIdentityToken(token string, key []byte, now int64) (*chatMcpIdentityClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return nil, errors.New("not a v1 token")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, errors.New("bad signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c chatMcpIdentityClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}
	switch {
	case c.Aud != "mcp":
		return nil, errors.New("wrong audience")
	case c.Exp <= now || c.Exp > now+chatMcpIdentityTTLMax:
		return nil, errors.New("expired or too far ahead")
	case c.Sub == "":
		return nil, errors.New("no sub")
	}
	return &c, nil
}

func TestChatMcpIdentityTestVector(t *testing.T) {
	claims := chatMcpIdentityClaims{Aud: "mcp", Sub: "user-42", Scenario: "support", Server: "account", Iat: 1767225600, Exp: 1767225720}
	token := signChatMcpIdentity([]byte(identityVectorKey), claims)
	if token != identityVectorToken {
		t.Fatalf("token:\n got %s\nwant %s", token, identityVectorToken)
	}
	got, err := verifyIdentityToken(token, []byte(identityVectorKey), 1767225600)
	if err != nil || *got != claims {
		t.Fatalf("verify: %+v %v", got, err)
	}
	if _, err := verifyIdentityToken(token, []byte(identityVectorKey+"x"), 1767225600); err == nil {
		t.Error("another key verified")
	}
	if _, err := verifyIdentityToken(token, []byte(identityVectorKey), 1767225720); err == nil {
		t.Error("an expired token verified")
	}
	// A join token has no aud: it never passes as an identity token.
	joinLike := signChatMcpIdentity([]byte(identityVectorKey), chatMcpIdentityClaims{Sub: "user-42", Iat: 1, Exp: 100})
	if _, err := verifyIdentityToken(joinLike, []byte(identityVectorKey), 1); err == nil || err.Error() != "wrong audience" {
		t.Errorf("a token without aud: %v", err)
	}
}

func TestChatMcpForwardIdentityNormalize(t *testing.T) {
	for _, c := range []struct {
		f    ChatMcpForwardIdentity
		want string
	}{
		{ChatMcpForwardIdentity{SecretRef: "id"}, ""},
		{ChatMcpForwardIdentity{SecretRef: "id", Header: "X-User", TTLSeconds: 300}, ""},
		{ChatMcpForwardIdentity{}, "identity secret name"},
		{ChatMcpForwardIdentity{SecretRef: "wiki"}, "a secret of its own"},
		{ChatMcpForwardIdentity{SecretRef: "id", Header: "authorization"}, "not a header the identity"},
		{ChatMcpForwardIdentity{SecretRef: "id", Header: "Mcp-Session-Id"}, "not a header the identity"},
		{ChatMcpForwardIdentity{SecretRef: "id", Header: "X Bad"}, "not a header the identity"},
		{ChatMcpForwardIdentity{SecretRef: "id", TTLSeconds: 301}, "between 10 and 300"},
		{ChatMcpForwardIdentity{SecretRef: "id", TTLSeconds: 5}, "between 10 and 300"},
	} {
		f := c.f
		err := f.normalize("wiki", "wiki")
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%+v: %v", c.f, err)
		}
	}
	f := ChatMcpForwardIdentity{SecretRef: "id"}
	if err := f.normalize("wiki", ""); err != nil || f.Header != chatMcpIdentityHeaderDefault || f.TTLSeconds != chatMcpIdentityTTLDefault {
		t.Errorf("defaults: %+v %v", f, err)
	}
}

func TestMergeChatMcpIdentity(t *testing.T) {
	auth := map[string][]string{"Authorization": {"Bearer s"}}
	if out, err := mergeChatMcpIdentity(auth, nil); err != nil || len(out) != 1 {
		t.Errorf("no identity: %v %v", out, err)
	}
	out, err := mergeChatMcpIdentity(auth, map[string][]string{"X-Mddb-Chat-Identity": {"t"}})
	if err != nil || out.Get("Authorization") != "Bearer s" || out.Get("X-MDDB-Chat-Identity") != "t" || len(auth) != 1 {
		t.Errorf("merged: %v %v", out, err)
	}
	if _, err := mergeChatMcpIdentity(auth, map[string][]string{"Authorization": {"t"}}); err == nil {
		t.Error("a clash was merged")
	}
	if out, err := mergeChatMcpIdentity(nil, map[string][]string{"X-A": {"t"}}); err != nil || out.Get("X-A") != "t" {
		t.Errorf("no credential: %v %v", out, err)
	}
}

func TestChatMcpIdentityInactiveReason(t *testing.T) {
	sc := &ChatScenario{Enabled: true}
	m := &ChatMcpServer{Enabled: true, ForwardIdentity: &ChatMcpForwardIdentity{SecretRef: "id"}}
	if got := chatMcpInactiveReason(sc, m, "", func(string) bool { return false }); got != "identity secret id does not exist" {
		t.Errorf("reason: %q", got)
	}
	if got := chatMcpInactiveReason(sc, m, "", func(string) bool { return true }); got != "" {
		t.Errorf("reason with the key: %q", got)
	}
	sc.McpServers = []ChatMcpServer{*m}
	if st := chatMcpStates(sc, "", func(string) bool { return true }); !st[0].ForwardIdentity {
		t.Errorf("state: %+v", st)
	}
}

// The clock the token's iat comes from.
func identityClock(s *Server, at time.Time) { s.ChatScenarios.now = func() time.Time { return at } }

// Saving: the key must exist, be at least 32 bytes and not be the server's
// secret; it counts as in use; the setting round-trips over REST and gRPC.
func TestChatMcpIdentitySave(t *testing.T) {
	f := newMcpFixture(t)
	s := identityServer(t, f, true)
	if _, err := s.PutChatMcpSecret("short", "", "too-short"); err != nil {
		t.Fatal(err)
	}
	put := func(identity map[string]any) (int, string) {
		server := map[string]any{"name": "wiki", "url": f.URL, "enabled": true, "secretRef": "wiki", "allowedTools": []string{"search"},
			"forwardIdentity": identity}
		rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", chatMcpBody(t, server))
		return rec.Code, rec.Body.String()
	}
	for want, identity := range map[string]map[string]any{
		"mcp secret not found: ghost": {"secretRef": "ghost"},
		"shorter than 32 bytes":       {"secretRef": "short"},
		"a secret of its own":         {"secretRef": "wiki"},
		"between 10 and 300":          {"secretRef": "wiki-id", "ttlSeconds": 900},
	} {
		if code, body := put(identity); code != http.StatusBadRequest || !strings.Contains(body, want) {
			t.Errorf("%v: %d %s", identity, code, body)
		}
	}
	if code, body := put(map[string]any{"secretRef": "wiki-id", "header": "X-User", "ttlSeconds": 60}); code != http.StatusOK {
		t.Fatalf("save: %d %s", code, body)
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/mcp-secrets/wiki-id", ""); rec.Code != http.StatusConflict {
		t.Errorf("delete a key in use: %d %s", rec.Code, rec.Body.String())
	}
	g := NewGRPCServer(s)
	got, err := g.GetChatScenario(asClaims(root), &proto.GetChatScenarioRequest{Name: "support"})
	fi := got.GetMcpServers()[0].GetForwardIdentity()
	if err != nil || fi.GetSecretRef() != "wiki-id" || fi.GetHeader() != "X-User" || fi.GetTtlSeconds() != 60 {
		t.Fatalf("gRPC read: %+v %v", fi, err)
	}
	back := chatMcpServersFromProto(got.GetMcpServers())
	if back[0].ForwardIdentity == nil || *back[0].ForwardIdentity != (ChatMcpForwardIdentity{SecretRef: "wiki-id", Header: "X-User", TTLSeconds: 60}) {
		t.Errorf("from proto: %+v", back[0].ForwardIdentity)
	}
	prompt, err := g.GetChatScenarioPrompt(asClaims(root), &proto.GetChatScenarioPromptRequest{Name: "support"})
	if err != nil || !prompt.GetMcpServers()[0].GetForwardIdentity() {
		t.Errorf("state: %+v %v", prompt.GetMcpServers(), err)
	}
	if err := s.checkChatMcpIdentityKey(&ChatMcpServer{}); err != nil {
		t.Errorf("no forwardIdentity: %v", err)
	}
}
