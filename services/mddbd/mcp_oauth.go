package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"mddb/internal/envconf"
	json "mddb/internal/jsonx"
)

// OAuth 2.1 on the MCP listener (#322).
//
// claude.ai custom connectors speak OAuth or nothing, and mddb's MCP listener
// took only API keys, so the one way to connect claude.ai was an endpoint with
// no authentication behind someone else's proxy. This is the smallest
// authorization server the MCP authorization spec asks for: protected-resource
// and authorization-server metadata, dynamic client registration, the
// authorization-code grant with PKCE (S256 only), and refresh tokens.
//
// It adds no identities of its own. The person authorizing a client pastes an
// MCP API key into the consent page, and every token issued is that key,
// sealed: the listener unseals it and hands the key to the API-key
// middleware, which checks it as it checks any key. Deleting the key therefore
// revokes every token made from it, and a token can do exactly what its key
// can.
//
// Tokens and client IDs are AES-256-GCM sealed with MDDB_MCP_OAUTH_SECRET and
// carry no server-side state, so they survive a restart and a second replica.
// Only authorization codes are kept in memory, because a code must be usable
// once and lives for a minute.

const (
	oauthAccessPrefix  = "mddb_oat_"
	oauthRefreshPrefix = "mddb_ort_"
	oauthClientPrefix  = "mddb_oc_"
	oauthCodeTTL       = time.Minute
)

// mcpOAuth is the authorization server and the bearer-token check.
type mcpOAuth struct {
	issuer       string // public base URL, no trailing slash
	aead         cipher.AEAD
	accessTTL    time.Duration
	refreshTTL   time.Duration
	allowedURIs  map[string]bool // exact redirect URIs besides loopback
	keys         *MCPAPIKeyMiddleware
	now          func() time.Time
	codesMu      sync.Mutex
	codes        map[string]oauthCode
	resourcePath string
}

// oauthCode is an issued, not yet redeemed authorization code.
type oauthCode struct {
	clientID, redirectURI, challenge, apiKey string
	expires                                  time.Time
}

// sealedToken is the content of an access token, refresh token or client ID.
type sealedToken struct {
	Kind         string   `json:"t"`
	APIKey       string   `json:"k,omitempty"`
	ClientID     string   `json:"c,omitempty"`
	RedirectURIs []string `json:"r,omitempty"`
	ClientName   string   `json:"n,omitempty"`
	Expires      int64    `json:"e,omitempty"`
}

var errOAuthToken = errors.New("invalid or expired token")

// defaultOAuthRedirectURIs are claude.ai's and claude.com's connector
// callbacks. Loopback redirects (Claude Code, MCP Inspector) are always
// allowed, as OAuth 2.1 allows them for native clients.
const defaultOAuthRedirectURIs = "https://claude.ai/api/mcp/auth_callback,https://claude.com/api/mcp/auth_callback"

// newMCPOAuthFromEnv returns the authorization server, or nil when OAuth is
// off or cannot run. It needs API-key auth, since tokens stand for keys.
func newMCPOAuthFromEnv(keys *MCPAPIKeyMiddleware) *mcpOAuth {
	if os.Getenv("MDDB_MCP_OAUTH_ENABLED") != "true" {
		return nil
	}
	issuer := strings.TrimRight(os.Getenv("MDDB_MCP_OAUTH_ISSUER"), "/")
	if keys == nil || !keys.enabled {
		slog.Error("MCP OAuth is off: it requires MDDB_MCP_API_KEY_ENABLED=true, because every token stands for an MCP API key")
		return nil
	}
	if u, err := url.Parse(issuer); err != nil || u.Host == "" || (u.Scheme != "https" && !isLoopbackHost(u.Hostname())) {
		slog.Error("MCP OAuth is off: MDDB_MCP_OAUTH_ISSUER must be the listener's public https URL, e.g. https://mcp.example.com", "issuer", issuer)
		return nil
	}
	secret := os.Getenv("MDDB_MCP_OAUTH_SECRET")
	if len(secret) < 32 {
		slog.Warn("MDDB_MCP_OAUTH_SECRET is unset or shorter than 32 characters; using a random one, so tokens stop working on restart and differ between replicas")
		secret = randomToken()
	}
	o, err := newMCPOAuth(issuer, secret, keys)
	if err != nil {
		slog.Error("MCP OAuth is off", "err", err)
		return nil
	}
	o.accessTTL = time.Duration(envconf.Int("MDDB_MCP_OAUTH_ACCESS_TTL", 3600)) * time.Second
	o.refreshTTL = time.Duration(envconf.Int("MDDB_MCP_OAUTH_REFRESH_TTL", 30*24*3600)) * time.Second
	o.allowedURIs = map[string]bool{}
	for _, u := range strings.Split(envconf.String("MDDB_MCP_OAUTH_REDIRECT_URIS", defaultOAuthRedirectURIs), ",") {
		if u = strings.TrimSpace(u); u != "" {
			o.allowedURIs[u] = true
		}
	}
	slog.Info("MCP OAuth 2.1 enabled", "issuer", issuer, "redirectURIs", len(o.allowedURIs))
	return o
}

func newMCPOAuth(issuer, secret string, keys *MCPAPIKeyMiddleware) (*mcpOAuth, error) {
	k := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &mcpOAuth{
		issuer: issuer, aead: aead, keys: keys, now: time.Now,
		accessTTL: time.Hour, refreshTTL: 30 * 24 * time.Hour,
		allowedURIs: map[string]bool{}, codes: map[string]oauthCode{}, resourcePath: "/mcp",
	}, nil
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read does not fail on supported platforms
	return base64.RawURLEncoding.EncodeToString(b)
}

// seal encrypts t under prefix; open reverses it and checks kind and expiry.
func (o *mcpOAuth) seal(prefix string, t sealedToken) string {
	plain, _ := json.Marshal(t)
	nonce := make([]byte, o.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return prefix + base64.RawURLEncoding.EncodeToString(o.aead.Seal(nonce, nonce, plain, []byte(prefix)))
}

func (o *mcpOAuth) open(prefix, kind, token string) (*sealedToken, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, prefix))
	if !strings.HasPrefix(token, prefix) || err != nil || len(raw) < o.aead.NonceSize() {
		return nil, errOAuthToken
	}
	n := o.aead.NonceSize()
	plain, err := o.aead.Open(nil, raw[:n], raw[n:], []byte(prefix))
	if err != nil {
		return nil, errOAuthToken
	}
	var t sealedToken
	if json.Unmarshal(plain, &t) != nil || t.Kind != kind || (t.Expires != 0 && o.now().Unix() > t.Expires) {
		return nil, errOAuthToken
	}
	return &t, nil
}

// resourceMetadataURL is what a 401 points the client at.
func (o *mcpOAuth) resourceMetadataURL() string {
	return o.issuer + "/.well-known/oauth-protected-resource"
}

// Wrap serves the OAuth endpoints and turns an OAuth access token into the
// API key it stands for. A request with neither gets a 401 that tells the
// client where to authorize, as the MCP spec requires.
func (o *mcpOAuth) Wrap(next http.Handler) http.Handler {
	if o == nil {
		return next
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", o.handleResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-protected-resource/", o.handleResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server", o.handleServerMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server/", o.handleServerMetadata)
	mux.HandleFunc("/oauth/register", o.handleRegister)
	mux.HandleFunc("/oauth/authorize", o.handleAuthorize)
	mux.HandleFunc("/oauth/token", o.handleToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/") || strings.HasPrefix(r.URL.Path, "/oauth/") {
			mux.ServeHTTP(w, r)
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch {
		case strings.HasPrefix(bearer, oauthAccessPrefix):
			t, err := o.open(oauthAccessPrefix, "access", bearer)
			if err == nil {
				if _, valid := o.keys.validateKey(t.APIKey); !valid {
					err = errOAuthToken // its key was deleted: 401, so the client re-authorizes
				}
			}
			if err != nil {
				o.challenge(w, `error="invalid_token", error_description="The access token is invalid or expired"`)
				return
			}
			r.Header.Set("Authorization", "Bearer "+t.APIKey)
		case extractAPIKey(r) == "":
			o.challenge(w, "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// challenge writes a 401 with the WWW-Authenticate header MCP clients follow.
func (o *mcpOAuth) challenge(w http.ResponseWriter, extra string) {
	h := fmt.Sprintf(`Bearer resource_metadata=%q`, o.resourceMetadataURL())
	if extra != "" {
		h += ", " + extra
	}
	w.Header().Set("WWW-Authenticate", h)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32001,"message":"Authorization required"}}`))
}

// validRedirect reports whether a redirect URI may be registered: one of the
// configured URIs exactly, or an http loopback URI on any port.
func (o *mcpOAuth) validRedirect(raw string) bool {
	if o.allowedURIs[raw] {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http" && isLoopbackHost(u.Hostname()) && u.Fragment == ""
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
