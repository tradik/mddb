package main

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	json "mddb/internal/jsonx"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- MCP API Key Authentication (#29, #33) ----

// MCPAPIKeyMiddleware validates API keys for MCP HTTP endpoints.
// Two sources:
//   - Static keys from env: MDDB_MCP_API_KEYS=key1:name1,key2:name2
//   - Dynamic keys from BoltDB store: managed via REST API (POST/GET/DELETE /v1/mcp/keys)
//
// Enable with MDDB_MCP_API_KEY_ENABLED=true.
type MCPAPIKeyMiddleware struct {
	enabled  bool
	keys     map[string]string // static: key → name
	keyStore *mcpAPIKeyStore   // dynamic: BoltDB-backed store
	cacheTTL time.Duration
	cacheMu  sync.RWMutex
	cache    map[string]*apiKeyCacheEntry
}

type apiKeyCacheEntry struct {
	name    string
	valid   bool
	expires time.Time
}

// NewMCPAPIKeyMiddleware creates API key middleware from environment variables.
func NewMCPAPIKeyMiddleware() *MCPAPIKeyMiddleware {
	m := &MCPAPIKeyMiddleware{
		enabled: os.Getenv("MDDB_MCP_API_KEY_ENABLED") == "true",
		keys:    make(map[string]string),
		cache:   make(map[string]*apiKeyCacheEntry),
	}
	if !m.enabled {
		return m
	}

	// Static keys from env
	raw := os.Getenv("MDDB_MCP_API_KEYS")
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		key := parts[0]
		name := key
		if len(name) > 8 {
			name = name[:8] + "..."
		}
		if len(parts) == 2 && parts[1] != "" {
			name = parts[1]
		}
		m.keys[key] = name
	}

	ttl, err := time.ParseDuration(os.Getenv("MDDB_MCP_API_KEY_CACHE_TTL"))
	if err != nil || ttl <= 0 {
		ttl = 60 * time.Second
	}
	m.cacheTTL = ttl

	return m
}

// SetKeyStore attaches the BoltDB key store for dynamic key validation.
// Called after Server init.
func (m *MCPAPIKeyMiddleware) SetKeyStore(store *mcpAPIKeyStore) {
	m.keyStore = store
	storeCount := 0
	if store != nil {
		storeCount = store.Count()
	}
	slog.Info("MCP API key auth enabled (static, stored, cache TTL)", "keysCount", len(m.keys), "storeCount", storeCount, "cacheTTL", m.cacheTTL)
}

// Wrap wraps an HTTP handler with API key validation.
func (m *MCPAPIKeyMiddleware) Wrap(next http.Handler) http.Handler {
	if !m.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractAPIKey(r)
		if key == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32001,"message":"API key required. Set X-API-Key header or Authorization: Bearer <key>"}}`))
			return
		}

		name, ok := m.validateKey(key)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32001,"message":"Invalid API key"}}`))
			return
		}

		// Store key name in header for downstream logging
		r.Header.Set("X-MCP-Key-Name", name)
		next.ServeHTTP(w, r)
	})
}

func (m *MCPAPIKeyMiddleware) validateKey(provided string) (string, bool) {
	// 1. Check static keys (constant-time comparison)
	for key, name := range m.keys {
		if subtle.ConstantTimeCompare([]byte(provided), []byte(key)) == 1 {
			return name, true
		}
	}

	// 2. Check dynamic keys from BoltDB store
	if m.keyStore == nil {
		return "", false
	}
	return m.validateFromStore(provided)
}

func extractAPIKey(r *http.Request) string {
	// Check X-API-Key header first
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	// Check Authorization: Bearer <key>
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// Check query param (for SSE connections that can't set headers)
	if key := r.URL.Query().Get("api_key"); key != "" {
		return key
	}
	return ""
}

// ---- MCP Rate Limiting (#31) ----

// MCPRateLimiter provides per-client rate limiting for MCP endpoints.
// Enable with MDDB_MCP_RATE_LIMIT_ENABLED=true.
type MCPRateLimiter struct {
	enabled   bool
	limit     int           // requests per window
	window    time.Duration // window duration
	burst     int           // burst allowance
	by        string        // "ip", "api_key", or "session"
	mu        sync.Mutex
	clients   map[string]*rateLimitBucket
	cleanupAt time.Time
}

type rateLimitBucket struct {
	count    int
	windowAt time.Time
}

// NewMCPRateLimiter creates rate limiter from environment variables.
func NewMCPRateLimiter() *MCPRateLimiter {
	rl := &MCPRateLimiter{
		enabled: os.Getenv("MDDB_MCP_RATE_LIMIT_ENABLED") == "true",
		clients: make(map[string]*rateLimitBucket),
	}
	if !rl.enabled {
		return rl
	}

	rl.limit, _ = strconv.Atoi(os.Getenv("MDDB_MCP_RATE_LIMIT_REQUESTS"))
	if rl.limit <= 0 {
		rl.limit = 100
	}

	windowSec, _ := strconv.Atoi(os.Getenv("MDDB_MCP_RATE_LIMIT_WINDOW"))
	if windowSec <= 0 {
		windowSec = 60
	}
	rl.window = time.Duration(windowSec) * time.Second

	rl.burst, _ = strconv.Atoi(os.Getenv("MDDB_MCP_RATE_LIMIT_BURST"))
	if rl.burst <= 0 {
		rl.burst = 20
	}

	rl.by = os.Getenv("MDDB_MCP_RATE_LIMIT_BY")
	if rl.by == "" {
		rl.by = "ip"
	}

	slog.Info("MCP rate limiting enabled (req/s,,)", "limit", rl.limit, "windowSec", windowSec, "burst", rl.burst, "by", rl.by) // #nosec G706 -- config values only, not user input
	return rl
}

// Wrap wraps an HTTP handler with rate limiting.
func (rl *MCPRateLimiter) Wrap(next http.Handler) http.Handler {
	if !rl.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := rl.clientID(r)
		remaining, resetAt, allowed := rl.allow(clientID)

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt, 10))

		if !allowed {
			retryAfter := resetAt - time.Now().Unix()
			if retryAfter < 1 {
				retryAfter = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","error":{"code":-32429,"message":"Rate limit exceeded. Retry after %d seconds."}}`, retryAfter)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *MCPRateLimiter) clientID(r *http.Request) string {
	switch rl.by {
	case "api_key":
		if name := r.Header.Get("X-MCP-Key-Name"); name != "" {
			return "key:" + name
		}
		return "ip:" + clientIP(r)
	case "session":
		if sid := r.Header.Get("MCP-Session-Id"); sid != "" {
			return "session:" + sid
		}
		return "ip:" + clientIP(r)
	default: // "ip"
		return "ip:" + clientIP(r)
	}
}

func (rl *MCPRateLimiter) allow(clientID string) (remaining int, resetAt int64, allowed bool) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Periodic cleanup of expired buckets
	if now.After(rl.cleanupAt) {
		for k, b := range rl.clients {
			if now.After(b.windowAt) {
				delete(rl.clients, k)
			}
		}
		rl.cleanupAt = now.Add(rl.window * 2)
	}

	bucket, ok := rl.clients[clientID]
	if !ok || now.After(bucket.windowAt) {
		bucket = &rateLimitBucket{count: 0, windowAt: now.Add(rl.window)}
		rl.clients[clientID] = bucket
	}

	effectiveLimit := rl.limit + rl.burst
	bucket.count++
	remaining = effectiveLimit - bucket.count
	if remaining < 0 {
		remaining = 0
	}
	resetAt = bucket.windowAt.Unix()
	allowed = bucket.count <= effectiveLimit
	return
}

// clientIP is defined in sse.go — reused here.

// ---- MCP Request Logging / Audit Trail (#30) ----

// MCPRequestLogger logs MCP tool calls for audit/analytics.
// Enable with MDDB_MCP_LOGGING_ENABLED=true.
type MCPRequestLogger struct {
	enabled bool
	level   string // "debug", "info", "warn", "error"
}

// NewMCPRequestLogger creates request logger from environment variables.
func NewMCPRequestLogger() *MCPRequestLogger {
	rl := &MCPRequestLogger{
		enabled: os.Getenv("MDDB_MCP_LOGGING_ENABLED") == "true",
		level:   os.Getenv("MDDB_MCP_LOGGING_LEVEL"),
	}
	if rl.level == "" {
		rl.level = "info"
	}
	if rl.enabled {
		slog.Info("MCP request logging enabled (level=" + rl.level + ")") // #nosec G706 -- config value only, not user input
	}
	return rl
}

// Wrap wraps an HTTP handler with request logging.
func (rl *MCPRequestLogger) Wrap(next http.Handler) http.Handler {
	if !rl.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}

		next.ServeHTTP(sw, r)

		duration := time.Since(start)
		rl.logRequest(r, sw.status, duration)
	})
}

func (rl *MCPRequestLogger) logRequest(r *http.Request, status int, duration time.Duration) {
	keyName := r.Header.Get("X-MCP-Key-Name")
	if keyName == "" {
		keyName = "-"
	}
	sessionID := r.Header.Get("MCP-Session-Id")
	if sessionID == "" {
		sessionID = "-"
	}
	ip := clientIP(r)

	entry := MCPLogEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Method:     r.Method,
		Path:       r.URL.Path,
		Status:     status,
		DurationMs: duration.Milliseconds(),
		ClientIP:   ip,
		KeyName:    keyName,
		SessionID:  sessionID,
		UserAgent:  r.Header.Get("User-Agent"),
	}

	data, _ := json.Marshal(entry)
	slog.Info("MCP-AUDIT", "string", string(data)) // #nosec G706 -- structured JSON, no injection
}

// MCPLogEntry represents a single MCP request audit log entry.
type MCPLogEntry struct {
	Timestamp  string `json:"timestamp"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	ClientIP   string `json:"client_ip"`
	KeyName    string `json:"key_name,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
}

// statusWriter wraps http.ResponseWriter to capture status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
