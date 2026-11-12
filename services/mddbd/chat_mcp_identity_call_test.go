package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	"mddb/proto"

	"google.golang.org/grpc/codes"
)

// #414: calls to a forwardIdentity server carry the user's identity — on
// every request, for the caller's sub only — and are refused without one.

const identityKey = "identity-key-of-thirty-two-bytes!!"

// identityRequests records the credential and identity header of every
// request the fixture gets.
type identityRequests struct {
	mu   sync.Mutex
	auth []string
	ids  []string
}

func (r *identityRequests) watch(f *mcpFixture) {
	seen := func(req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.auth = append(r.auth, req.Header.Get("Authorization"))
		r.ids = append(r.ids, req.Header.Get(chatMcpIdentityHeaderDefault))
	}
	f.seen.Store(&seen)
}

func (r *identityRequests) take() (auth, ids []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, ids, r.auth, r.ids = r.auth, r.ids, nil, nil
	return auth, ids
}

// identityServer stores scenario "support" with server "wiki" at f, whose
// credential is "tok" and, when forward, whose identity key is "wiki-id".
func identityServer(t *testing.T, f *mcpFixture, forward bool) *Server {
	t.Helper()
	chatMcpKey(t)
	f.token = "tok"
	s := chatServer(t)
	externalMcpOn(t, s)
	for name, value := range map[string]string{"wiki": "tok", "wiki-id": identityKey} {
		if _, err := s.PutChatMcpSecret(name, "", value); err != nil {
			t.Fatal(err)
		}
	}
	server := map[string]any{"name": "wiki", "url": f.URL, "enabled": true, "secretRef": "wiki", "allowedTools": []string{"search", "boom"}}
	if forward {
		server["forwardIdentity"] = map[string]any{"secretRef": "wiki-id"}
	}
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/support", chatMcpBody(t, server)); rec.Code != http.StatusCreated {
		t.Fatalf("scenario: %d %s", rec.Code, rec.Body.String())
	}
	identityClock(s, time.Now())
	return s
}

// identityCall calls search as c for sub with args over REST.
func identityCall(t *testing.T, s *Server, c *JWTClaims, sub string, args map[string]any) (int, chatMcpCallResult, string) {
	t.Helper()
	body, _ := json.Marshal(chatMcpCallRequest{Scenario: "support", Server: "wiki", Tool: "search", Arguments: args, Sub: sub})
	rec := chatREST(t, s, c, http.MethodPost, "/v1/chat/mcp/call", string(body))
	var out chatMcpCallResult
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out, rec.Body.String()
}

func TestChatMcpIdentityForwarded(t *testing.T) {
	f := newMcpFixture(t)
	s := identityServer(t, f, true)
	var seen identityRequests
	seen.watch(f)
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	subs := map[string]string{}
	for _, sub := range []string{"user-1", "user-2"} {
		// A tool argument named sub changes nothing.
		code, out, body := identityCall(t, s, root, sub, map[string]any{"q": "mddb", "sub": "someone-else"})
		if code != http.StatusOK || out.IsError || out.Content != "found mddb" {
			t.Fatalf("%s: %d %s", sub, code, body)
		}
		auth, ids := seen.take()
		if len(ids) != 5 { // initialize, notifications/initialized, two pages of tools/list, tools/call
			t.Fatalf("%s: %d requests", sub, len(ids))
		}
		for i, id := range ids {
			c, err := verifyIdentityToken(id, []byte(identityKey), time.Now().Unix())
			if err != nil || auth[i] != "Bearer tok" || c.Sub != sub || c.Scenario != "support" || c.Server != "wiki" ||
				c.Exp-c.Iat != chatMcpIdentityTTLDefault {
				t.Fatalf("%s request %d: %q %+v %v", sub, i, auth[i], c, err)
			}
			subs[c.Sub] = id
		}
		if strings.Contains(logs.String(), ids[0]) || strings.Contains(logs.String(), identityKey) ||
			!strings.Contains(logs.String(), "sub="+sub+" scenario=support server=wiki tool=search") {
			t.Fatalf("log: %s", logs.String())
		}
	}
	if len(subs) != 2 || subs["user-1"] == subs["user-2"] {
		t.Errorf("two users, tokens: %v", subs)
	}
	if strings.Contains(logs.String(), "someone-else") || strings.Contains(logs.String(), "mddb") {
		t.Errorf("the arguments were logged: %s", logs.String())
	}
}

func TestChatMcpIdentityRefusals(t *testing.T) {
	f := newMcpFixture(t)
	s := identityServer(t, f, true)
	var seen identityRequests
	seen.watch(f)
	// No sub: refused, and nothing reaches the server.
	if code, _, body := identityCall(t, s, root, "", nil); code != http.StatusForbidden || !strings.Contains(body, "names no signed-in user") {
		t.Errorf("no sub: %d %s", code, body)
	}
	// A reader of the scenario who may not act for users cannot name one.
	addTestDoc(t, s, chatConversationsCollection, "seed", "en", "x", nil)
	if code, _, body := identityCall(t, s, alice, "user-1", nil); code != http.StatusForbidden {
		t.Errorf("a reader: %d %s", code, body)
	}
	if code, _, body := identityCall(t, s, root, "a\x00b", nil); code != http.StatusBadRequest {
		t.Errorf("a control character: %d %s", code, body)
	}
	if auth, _ := seen.take(); len(auth) != 0 {
		t.Errorf("refused calls reached the server: %d requests", len(auth))
	}
	// gRPC: FAILED_PRECONDITION without a sub; with one, the call is made.
	g := NewGRPCServer(s)
	req := &proto.CallChatMcpToolRequest{Scenario: "support", Server: "wiki", Tool: "search", ArgumentsJson: `{"q":"x"}`}
	if _, err := g.CallChatMcpTool(asClaims(root), req); grpcCode(err) != codes.FailedPrecondition {
		t.Errorf("gRPC without sub: %v", err)
	}
	req.Sub = "user-9"
	if res, err := g.CallChatMcpTool(asClaims(root), req); err != nil || res.GetIsError() {
		t.Errorf("gRPC with sub: %+v %v", res, err)
	}
	if _, ids := seen.take(); len(ids) != 5 || ids[4] == "" {
		t.Errorf("gRPC call: %v", ids)
	}
	// The key replaced by a short one: the call fails short, without it.
	if _, err := s.PutChatMcpSecret("wiki-id", "", "short-key"); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := identityCall(t, s, root, "user-1", nil); !out.IsError || !strings.Contains(out.Content, "shorter than 32 bytes") ||
		strings.Contains(out.Content, "short-key") {
		t.Errorf("a short key: %+v", out)
	}
	// The server's own secret moved to the identity header: refused, not replaced.
	if _, err := s.PutChatMcpSecret("wiki-id", "", identityKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutChatMcpSecret("wiki", chatMcpIdentityHeaderDefault, "tok"); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := identityCall(t, s, root, "user-1", nil); !out.IsError || !strings.Contains(out.Content, "header of the server's own secret") {
		t.Errorf("a clash: %+v", out)
	}
}

// A server without forwardIdentity behaves as before: no identity header,
// and a sub is ignored.
func TestChatMcpIdentityOffByDefault(t *testing.T) {
	f := newMcpFixture(t)
	s := identityServer(t, f, false)
	var seen identityRequests
	seen.watch(f)
	for _, sub := range []string{"", "user-1"} {
		if code, out, body := identityCall(t, s, alice, sub, map[string]any{"q": "mddb"}); code != http.StatusOK || out.Content != "found mddb" {
			t.Fatalf("sub %q: %d %s", sub, code, body)
		}
		auth, ids := seen.take()
		for i := range ids {
			if ids[i] != "" || auth[i] != "Bearer tok" {
				t.Fatalf("sub %q request %d: %q %q", sub, i, auth[i], ids[i])
			}
		}
	}
	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/support", "")
	if strings.Contains(rec.Body.String(), "forwardIdentity") {
		t.Errorf("a scenario without it lists it: %s", rec.Body.String())
	}
}
