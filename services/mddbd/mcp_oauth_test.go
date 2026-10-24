package main

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
)

// #322: claude.ai connectors authorize with OAuth 2.1. The flow ends in a
// token that stands for an MCP API key, and the listener treats it as that key.

const oauthTestKey = "mcp-key-0123456789"

func newTestOAuth(t *testing.T) (*mcpOAuth, *MCPAPIKeyMiddleware, http.Handler, *string) {
	t.Helper()
	keys := &MCPAPIKeyMiddleware{enabled: true, keys: map[string]string{oauthTestKey: "claude"}, cache: map[string]*apiKeyCacheEntry{}}
	o, err := newMCPOAuth("https://mcp.example.com", strings.Repeat("s", 32), keys)
	if err != nil {
		t.Fatal(err)
	}
	o.allowedURIs["https://claude.ai/api/mcp/auth_callback"] = true
	seen := new(string)
	h := o.Wrap(keys.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get("X-MCP-Key-Name")
		w.WriteHeader(http.StatusOK)
	})))
	return o, keys, h, seen
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func postForm(h http.Handler, path string, v url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return serve(h, r)
}

func registerClient(t *testing.T, h http.Handler, redirect string) string {
	t.Helper()
	rec := serve(h, httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"client_name":"Claude","redirect_uris":["`+redirect+`"]}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out["client_id"].(string)
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func authorizeValues(clientID, redirect, challenge string) url.Values {
	return url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"state": {"xyz"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
}

// authorize posts the consent form and returns the code from the redirect.
func authorize(t *testing.T, h http.Handler, v url.Values, key string) string {
	t.Helper()
	v.Set("api_key", key)
	v.Set("decision", "allow")
	rec := postForm(h, "/oauth/authorize", v)
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize: %d %s", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("state") != "xyz" || loc.Query().Get("iss") != "https://mcp.example.com" {
		t.Errorf("redirect lost state or iss: %s", loc)
	}
	return loc.Query().Get("code")
}

func tokens(t *testing.T, rec *httptest.ResponseRecorder) (access, refresh string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out["access_token"].(string), out["refresh_token"].(string)
}

func TestOAuthFlowEndsInATokenThatActsAsTheKey(t *testing.T) {
	_, _, h, seen := newTestOAuth(t)
	redirect := "https://claude.ai/api/mcp/auth_callback"

	// Unauthenticated: 401 pointing at the metadata.
	rec := serve(h, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Header().Get("WWW-Authenticate"), `resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`) {
		t.Fatalf("challenge: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	var meta map[string]any
	_ = json.Unmarshal(serve(h, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)).Body.Bytes(), &meta)
	if meta["resource"] != "https://mcp.example.com/mcp" {
		t.Errorf("resource metadata: %v", meta)
	}
	_ = json.Unmarshal(serve(h, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)).Body.Bytes(), &meta)
	if meta["token_endpoint"] != "https://mcp.example.com/oauth/token" || meta["registration_endpoint"] == nil {
		t.Errorf("server metadata: %v", meta)
	}

	clientID := registerClient(t, h, redirect)
	verifier, challenge := pkce()
	v := authorizeValues(clientID, redirect, challenge)
	page := serve(h, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+v.Encode(), nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Connect Claude to mddb") ||
		page.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("consent page: %d", page.Code)
	}

	code := authorize(t, h, v, oauthTestKey)
	tokenReq := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier}}
	access, refresh := tokens(t, postForm(h, "/oauth/token", tokenReq))

	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+access)
	if rec := serve(h, r); rec.Code != http.StatusOK || *seen != "claude" {
		t.Fatalf("access token: %d, key %q", rec.Code, *seen)
	}

	// A code works once.
	if rec := postForm(h, "/oauth/token", tokenReq); rec.Code != http.StatusBadRequest {
		t.Errorf("code reused: %d", rec.Code)
	}
	// Refresh gives a new pair.
	access2, _ := tokens(t, postForm(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {refresh}, "client_id": {clientID}}))
	if access2 == access {
		t.Error("refresh returned the same access token")
	}
	// An API key still works beside OAuth.
	r = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("X-API-Key", oauthTestKey)
	if rec := serve(h, r); rec.Code != http.StatusOK {
		t.Errorf("plain API key: %d", rec.Code)
	}
}

func TestOAuthRejectsWhatItShould(t *testing.T) {
	o, keys, h, _ := newTestOAuth(t)
	redirect := "http://127.0.0.1:33418/callback" // loopback: always allowed
	clientID := registerClient(t, h, redirect)
	verifier, challenge := pkce()

	if rec := serve(h, httptest.NewRequest(http.MethodPost, "/oauth/register",
		strings.NewReader(`{"redirect_uris":["https://evil.example/cb"]}`))); rec.Code != http.StatusBadRequest {
		t.Errorf("foreign redirect registered: %d", rec.Code)
	}
	for _, body := range []string{"{", `{"redirect_uris":[]}`} {
		if rec := serve(h, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))); rec.Code != http.StatusBadRequest {
			t.Errorf("register %s: %d", body, rec.Code)
		}
	}
	for _, path := range []string{"/oauth/register", "/oauth/token"} {
		if rec := serve(h, httptest.NewRequest(http.MethodGet, path, nil)); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: %d", path, rec.Code)
		}
	}

	// A redirect URI the client did not register: error page, no redirect.
	v := authorizeValues(clientID, "http://127.0.0.1:9/other", challenge)
	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+v.Encode(), nil)); rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
		t.Errorf("unregistered redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// No PKCE: error sent to the client.
	v = authorizeValues(clientID, redirect, "")
	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+v.Encode(), nil)); !strings.Contains(rec.Header().Get("Location"), "error=invalid_request") {
		t.Errorf("missing PKCE: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Wrong key, then deny.
	v = authorizeValues(clientID, redirect, challenge)
	v.Set("api_key", "wrong")
	if rec := postForm(h, "/oauth/authorize", v); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "not a valid") {
		t.Errorf("wrong key: %d", rec.Code)
	}
	v.Set("decision", "deny")
	if rec := postForm(h, "/oauth/authorize", v); !strings.Contains(rec.Header().Get("Location"), "error=access_denied") {
		t.Errorf("deny: %q", rec.Header().Get("Location"))
	}

	// Wrong verifier burns the code.
	code := authorize(t, h, authorizeValues(clientID, redirect, challenge), oauthTestKey)
	bad := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {"nope"}}
	if rec := postForm(h, "/oauth/token", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("wrong verifier: %d", rec.Code)
	}
	bad.Set("code_verifier", verifier)
	if rec := postForm(h, "/oauth/token", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("code survived a failed redemption: %d", rec.Code)
	}
	for _, v := range []url.Values{
		{"grant_type": {"password"}, "client_id": {clientID}},
		{"grant_type": {"refresh_token"}, "client_id": {"forged"}},
	} {
		if rec := postForm(h, "/oauth/token", v); rec.Code < 400 {
			t.Errorf("%v accepted: %d", v, rec.Code)
		}
	}

	// Tokens: tampered, expired, and outliving their key.
	code = authorize(t, h, authorizeValues(clientID, redirect, challenge), oauthTestKey)
	access, refresh := tokens(t, postForm(h, "/oauth/token", url.Values{"grant_type": {"authorization_code"},
		"code": {code}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {verifier}}))
	call := func(tok string) int {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		return serve(h, r).Code
	}
	if c := call(access[:len(access)-2] + "AA"); c != http.StatusUnauthorized {
		t.Errorf("tampered token: %d", c)
	}
	if c := call(refresh); c != http.StatusForbidden {
		t.Errorf("refresh token used as access token: %d", c) // not OAuth-shaped, so treated as a (bad) key
	}
	o.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if c := call(access); c != http.StatusUnauthorized {
		t.Errorf("expired token: %d", c)
	}
	o.now = time.Now
	delete(keys.keys, oauthTestKey)
	if c := call(access); c != http.StatusUnauthorized {
		t.Errorf("token outlived its key: %d", c)
	}
	if rec := postForm(h, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"client_id": {clientID}}); rec.Code != http.StatusBadRequest {
		t.Errorf("refresh after the key was deleted: %d", rec.Code)
	}
}

func TestOAuthFromEnv(t *testing.T) {
	keys := &MCPAPIKeyMiddleware{enabled: true, keys: map[string]string{}, cache: map[string]*apiKeyCacheEntry{}}
	if newMCPOAuthFromEnv(keys) != nil {
		t.Error("on without MDDB_MCP_OAUTH_ENABLED")
	}
	t.Setenv("MDDB_MCP_OAUTH_ENABLED", "true")
	t.Setenv("MDDB_MCP_OAUTH_ISSUER", "http://mcp.example.com")
	if newMCPOAuthFromEnv(keys) != nil {
		t.Error("accepted a plain-http public issuer")
	}
	t.Setenv("MDDB_MCP_OAUTH_ISSUER", "https://mcp.example.com/")
	if newMCPOAuthFromEnv(&MCPAPIKeyMiddleware{}) != nil {
		t.Error("on without API-key auth")
	}
	t.Setenv("MDDB_MCP_OAUTH_REDIRECT_URIS", "https://a.example/cb, ")
	o := newMCPOAuthFromEnv(keys)
	if o == nil || o.issuer != "https://mcp.example.com" || !o.allowedURIs["https://a.example/cb"] || len(o.allowedURIs) != 1 {
		t.Fatalf("from env: %+v", o)
	}
	var none *mcpOAuth
	inner := http.NotFoundHandler()
	if none.Wrap(inner) == nil {
		t.Error("nil OAuth should pass requests through")
	}
}

func TestOAuthForgedClientAndStaleCodes(t *testing.T) {
	o, _, h, _ := newTestOAuth(t)
	_, challenge := pkce()
	v := authorizeValues("mddb_oc_forged", "http://localhost:1/cb", challenge)
	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+v.Encode(), nil)); rec.Code != http.StatusBadRequest {
		t.Errorf("forged client: %d", rec.Code)
	}
	if rec := serve(h, httptest.NewRequest(http.MethodGet, "/oauth/authorize?%zz", nil)); rec.Code != http.StatusBadRequest {
		t.Errorf("unparseable request: %d", rec.Code)
	}
	o.codes["stale"] = oauthCode{expires: time.Now().Add(-time.Second)}
	clientID := registerClient(t, h, "http://localhost:1/cb")
	authorize(t, h, authorizeValues(clientID, "http://localhost:1/cb", challenge), oauthTestKey)
	if _, left := o.codes["stale"]; left {
		t.Error("an expired code was not swept")
	}
}
