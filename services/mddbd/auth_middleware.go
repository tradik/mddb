package main

import (
	"context"
	"mddb/internal/audit"
	"net/http"
	"os"
	"strings"
	"time"
)

// buildPublicEndpoints returns the set of endpoints exempt from authentication.
//
// /health, /v1/health and /v1/auth/login are always public (liveness probes
// and the login endpoint itself). /metrics is public ONLY when explicitly
// opted in via MDDB_METRICS_PUBLIC=true (SEC-009): with auth enabled the
// Prometheus counters — operation tallies, collection labels, traffic
// volumes, build version — would otherwise be readable without credentials,
// handing an attacker free reconnaissance while the rest of the API is gated.
// When auth is disabled the HTTP middleware short-circuits before consulting
// this set, so /metrics stays reachable exactly as before (opt-in only
// matters once MDDB_AUTH_ENABLED=true).
func buildPublicEndpoints() map[string]bool {
	eps := map[string]bool{
		"/health":        true,
		"/v1/health":     true,
		"/v1/auth/login": true,
	}
	if os.Getenv("MDDB_METRICS_PUBLIC") == "true" {
		eps["/metrics"] = true
	}
	return eps
}

// apiKeySessionToken signs the short-lived JWT an API key is exchanged for.
// A variable so a test can make signing fail: with HS256 and any secret it
// cannot, and the refusal path would otherwise go unexercised.
var apiKeySessionToken = GenerateTenantJWT

// HTTPMiddleware wraps HTTP handlers with authentication
func (am *AuthManager) HTTPMiddleware(next http.Handler) http.Handler {
	if !am.enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for public endpoints
		if am.isPublicEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		apiKey, token := requestCredentials(r)

		// If no bearer token, try API key
		if token == "" && apiKey != "" {
			var ok bool
			if token, r, ok = am.apiKeySession(w, r, apiKey); !ok {
				return
			}
		}

		claims, ok := am.sessionClaims(w, r, token)
		if !ok {
			return
		}

		// Inject claims into context
		ctx := context.WithValue(r.Context(), authContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestCredentials reads a request's API key and bearer token.
//
// An API key sent as `Authorization: Bearer` is an API key, not a JWT.
//
// It used to be parsed as one and refused with "invalid token", which
// names the credential when the problem was the header — and the MCP
// middleware next door has always accepted the key in either place, so
// two surfaces of one server disagreed and clients met the stricter one
// first (#212). The prefix decides it outright: nothing is validated
// twice, and a JWT can never be mistaken for a key.
func requestCredentials(r *http.Request) (apiKey, token string) {
	token = extractTokenFromRequest(r)
	apiKey = r.Header.Get("X-API-Key")
	// INT-019: the Elasticsearch clients send api_key=... as
	// `Authorization: ApiKey <key>`. Read only on /es, so /v1 accepts
	// exactly the credentials it always has.
	if apiKey == "" && isESPath(r.URL.Path) {
		apiKey = esAPIKey(r)
	}
	if apiKey == "" && strings.HasPrefix(token, APIKeyPrefix) {
		apiKey, token = token, ""
	}
	return apiKey, token
}

// apiKeySession turns a valid API key into a short-lived session token — the
// tenant confinement travels with it (GenerateTenantJWT strips Admin for
// tenant users) — and records the key on the request (GO-054: per-key
// limits). ok is false when it answered a refusal.
func (am *AuthManager) apiKeySession(w http.ResponseWriter, r *http.Request, apiKey string) (string, *http.Request, bool) {
	username, err := am.ValidateAPIKey(apiKey)
	if err != nil {
		am.auditAuth(r, "", "auth.apikey", "fail", err.Error())
		http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
		return "", r, false
	}
	isAdmin := am.IsAdmin(username)
	tenant := am.UserTenant(username)
	token, err := apiKeySessionToken(username, tenant, isAdmin, am.config.JWTSecret, 1*3600*time.Second) // 1h
	if err != nil {
		am.auditAuth(r, username, "auth.apikey", "fail", "jwt generation failed")
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return "", r, false
	}
	am.auditAuth(r, username, "auth.apikey", "ok", "")
	return token, r.WithContext(withAPIKeyID(r.Context(), apiKey)), true
}

// sessionClaims validates the session token and its user. ok is false when
// it answered a refusal.
func (am *AuthManager) sessionClaims(w http.ResponseWriter, r *http.Request, token string) (*JWTClaims, bool) {
	if token == "" {
		am.auditAuth(r, "", "auth.missing", "fail", "")
		http.Error(w, `{"error":"missing authentication"}`, http.StatusUnauthorized)
		return nil, false
	}
	claims, err := ValidateJWT(token, am.config.JWTSecret)
	if err != nil {
		am.auditAuth(r, "", "auth.jwt", "fail", "invalid token")
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return nil, false
	}
	// Check if user still exists and is not disabled.
	// Return identical response for both cases to avoid
	// leaking user existence (timing/enumeration side-channel).
	user, err := am.GetUser(claims.Username)
	if err != nil || user.Disabled {
		am.auditAuth(r, claims.Username, "auth.jwt", "fail", "user disabled or not found")
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return nil, false
	}
	am.refreshClaims(claims)
	return claims, true
}

// auditAuth records an authentication attempt. Safe on nil server.
// When the attempt failed, also feeds the sliding-window tracker so
// a burst of failures from the same actor/IP fires the security
// incident event.
func (am *AuthManager) auditAuth(r *http.Request, actor, action, result, detail string) {
	if am == nil || am.server == nil {
		return
	}
	ip := ClientIP(r)
	if am.server.AuditManager != nil {
		am.server.AuditManager.Record(audit.AuditEvent{
			Actor:     actor,
			Action:    action,
			Resource:  r.URL.Path,
			Result:    result,
			IP:        ip,
			UserAgent: r.UserAgent(),
			Detail:    detail,
		})
	}
	if result == "fail" && am.server.AuthFailureTracker != nil {
		am.server.AuthFailureTracker.Record(actor, ip)
	}
}

// extractTokenFromRequest extracts JWT token from Authorization header
func extractTokenFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	// Bearer token format: "Bearer <token>"
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}

	return parts[1]
}

// isPublicEndpoint reports whether path is exempt from authentication for
// this AuthManager. The exempt set is computed once at construction
// (NewAuthManager) from the environment; a nil set (manager built via a raw
// struct literal) is treated as "nothing public" so it fails closed. A
// sync's webhook (/v1/automation/{id}/hook, #421) is public too: it proves
// itself with the signature its handler verifies.
func (am *AuthManager) isPublicEndpoint(path string) bool {
	if am.publicEndpoints == nil {
		return false
	}
	return am.publicEndpoints[path] || isAutomationHookPath(path)
}
