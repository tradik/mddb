package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"

	json "mddb/internal/jsonx"
)

// The OAuth 2.1 endpoints (#322): RFC 9728 and RFC 8414 metadata, RFC 7591
// registration, the authorization endpoint with its consent page, and the
// token endpoint. Every client is public (PKCE, no client secret), which is
// what claude.ai and Claude Code register as.

const oauthMaxBody = 64 << 10

func writeOAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, desc string) {
	writeOAuthJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func (o *mcpOAuth) handleResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"resource":                 o.issuer + o.resourcePath,
		"authorization_servers":    []string{o.issuer},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "mddb",
	})
}

func (o *mcpOAuth) handleServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"issuer":                                o.issuer,
		"authorization_endpoint":                o.issuer + "/oauth/authorize",
		"token_endpoint":                        o.issuer + "/oauth/token",
		"registration_endpoint":                 o.issuer + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}

// handleRegister is dynamic client registration. The client ID it returns
// is the registration itself, sealed, so nothing is stored.
func (o *mcpOAuth) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, oauthMaxBody)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be JSON client metadata")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 || len(req.ClientName) > 200 {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "1-10 redirect_uris and a client_name of at most 200 characters")
		return
	}
	for _, u := range req.RedirectURIs {
		if !o.validRedirect(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri",
				"redirect URI not allowed: "+u+" (add it to MDDB_MCP_OAUTH_REDIRECT_URIS)")
			return
		}
	}
	id := o.seal(oauthClientPrefix, sealedToken{Kind: "client", RedirectURIs: req.RedirectURIs, ClientName: req.ClientName})
	writeOAuthJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        o.now().Unix(),
		"client_name":                req.ClientName,
		"redirect_uris":              req.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

// authzRequest is an authorization request, from the query or the consent form.
type authzRequest struct {
	ClientID, RedirectURI, State, Challenge, Method, Scope, Resource string
	ClientName                                                       string
}

func readAuthzRequest(v url.Values) authzRequest {
	return authzRequest{
		ClientID: v.Get("client_id"), RedirectURI: v.Get("redirect_uri"), State: v.Get("state"),
		Challenge: v.Get("code_challenge"), Method: v.Get("code_challenge_method"),
		Scope: v.Get("scope"), Resource: v.Get("resource"),
	}
}

// checkClient validates the client and its redirect URI. Until both are
// known good, an error is shown here and never sent to the redirect URI.
// On success a.RedirectURI is replaced by the registered copy from the
// sealed client ID, so the browser is only ever sent to a value the server
// issued, never to the request parameter itself.
func (o *mcpOAuth) checkClient(a *authzRequest) bool {
	c, err := o.open(oauthClientPrefix, "client", a.ClientID)
	if err != nil || !o.validRedirect(a.RedirectURI) {
		return false
	}
	for _, u := range c.RedirectURIs {
		if u == a.RedirectURI {
			a.RedirectURI, a.ClientName = u, c.ClientName
			return true
		}
	}
	return false
}

func (o *mcpOAuth) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	}
	if err := r.ParseForm(); err != nil {
		renderOAuthError(w, "The authorization request could not be read.")
		return
	}
	a := readAuthzRequest(r.Form)
	if !o.checkClient(&a) {
		renderOAuthError(w, "Unknown client or redirect URI. The application has to register again.")
		return
	}
	if r.Form.Get("response_type") != "code" || a.Method != "S256" || len(a.Challenge) < 43 || len(a.Challenge) > 128 {
		o.redirectWith(w, r, a.RedirectURI, url.Values{"error": {"invalid_request"},
			"error_description": {"response_type=code with an S256 code_challenge is required"}, "state": {a.State}})
		return
	}
	if r.Method != http.MethodPost {
		renderOAuthConsent(w, http.StatusOK, a, "")
		return
	}
	if r.Form.Get("decision") == "deny" {
		o.redirectWith(w, r, a.RedirectURI, url.Values{"error": {"access_denied"}, "state": {a.State}})
		return
	}
	key := r.Form.Get("api_key")
	if _, valid := o.keys.validateKey(key); key == "" || !valid {
		renderOAuthConsent(w, http.StatusUnauthorized, a, "That is not a valid MCP API key.")
		return
	}
	code := randomToken()
	o.codesMu.Lock()
	now := o.now()
	for c, v := range o.codes {
		if now.After(v.expires) {
			delete(o.codes, c)
		}
	}
	o.codes[code] = oauthCode{clientID: a.ClientID, redirectURI: a.RedirectURI, challenge: a.Challenge,
		apiKey: key, expires: now.Add(oauthCodeTTL)}
	o.codesMu.Unlock()
	o.redirectWith(w, r, a.RedirectURI, url.Values{"code": {code}, "state": {a.State}, "iss": {o.issuer}})
}

// redirectWith sends the browser back to the client. target is always the
// registered redirect URI checkClient took from the sealed client ID.
// The final address is checked again before the redirect (CodeQL alert 64):
// a later change that let an unchecked value reach this point would then
// fail closed instead of becoming an open redirect.
func (o *mcpOAuth) redirectWith(w http.ResponseWriter, r *http.Request, target string, v url.Values) {
	u, err := url.Parse(target)
	if err != nil {
		renderOAuthError(w, "The redirect address could not be read.")
		return
	}
	q := u.Query()
	for k, vals := range v {
		if len(vals) > 0 && vals[0] != "" {
			q.Set(k, vals[0])
		}
	}
	u.RawQuery = q.Encode()
	location := u.String()
	if !o.isValidRedirect(location) {
		renderOAuthError(w, "Refusing to redirect to an address that is not allowed.")
		return
	}
	http.Redirect(w, r, location, http.StatusFound)
}

// isValidRedirect reports whether the browser may be sent to location: its
// address without the query is an allowed redirect URI (the query carries
// code, state and iss) or an http loopback URI.
func (o *mcpOAuth) isValidRedirect(location string) bool {
	base := withoutQuery(location)
	if base == "" {
		return false
	}
	for allowed := range o.allowedURIs {
		if withoutQuery(allowed) == base {
			return true
		}
	}
	return o.validRedirect(base)
}

// withoutQuery is raw without its query and fragment, or "" if unparseable.
func withoutQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String()
}

func (o *mcpOAuth) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		oauthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "form body required")
		return
	}
	clientID := r.PostForm.Get("client_id")
	if _, err := o.open(oauthClientPrefix, "client", clientID); err != nil {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	var apiKey string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		apiKey = o.redeemCode(r.PostForm.Get("code"), clientID, r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
	case "refresh_token":
		if t, err := o.open(oauthRefreshPrefix, "refresh", r.PostForm.Get("refresh_token")); err == nil && t.ClientID == clientRef(clientID) {
			apiKey = t.APIKey
		}
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "authorization_code or refresh_token")
		return
	}
	// The key is checked again on every grant, so a deleted key stops
	// yielding tokens at the next refresh, not when its last token expires.
	if _, valid := o.keys.validateKey(apiKey); apiKey == "" || !valid {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code or refresh token is invalid, expired, or its API key was revoked")
		return
	}
	now := o.now()
	ref := clientRef(clientID)
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"access_token":  o.seal(oauthAccessPrefix, sealedToken{Kind: "access", APIKey: apiKey, ClientID: ref, Expires: now.Add(o.accessTTL).Unix()}),
		"token_type":    "Bearer",
		"expires_in":    int(o.accessTTL.Seconds()),
		"refresh_token": o.seal(oauthRefreshPrefix, sealedToken{Kind: "refresh", APIKey: apiKey, ClientID: ref, Expires: now.Add(o.refreshTTL).Unix()}),
	})
}

// redeemCode consumes a code — once, whatever the outcome — and returns the
// API key it was issued for, or "" when it does not check out.
func (o *mcpOAuth) redeemCode(code, clientID, redirectURI, verifier string) string {
	o.codesMu.Lock()
	c, ok := o.codes[code]
	delete(o.codes, code)
	o.codesMu.Unlock()
	if !ok || o.now().After(c.expires) || c.clientID != clientID || c.redirectURI != redirectURI {
		return ""
	}
	sum := sha256.Sum256([]byte(verifier))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
		return ""
	}
	return c.apiKey
}

// clientRef is the short form of a client ID that tokens carry.
func clientRef(clientID string) string {
	sum := sha256.Sum256([]byte(clientID))
	return hex.EncodeToString(sum[:8])
}
