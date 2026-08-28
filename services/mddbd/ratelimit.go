package main

import (
	"context"
	"fmt"
	"log/slog"
	"mddb/internal/webhooks"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// RateLimiter is a per-client sliding-window limiter used by both
// the HTTP middleware and the gRPC interceptor so both transports
// share a single budget and a single pool of buckets per client.
//
// Configuration comes from environment variables and is evaluated
// once at startup:
//
//	MDDB_RATE_LIMIT_ENABLED   — bool  (default false)
//	MDDB_RATE_LIMIT_REQUESTS  — int   (default 100)
//	MDDB_RATE_LIMIT_WINDOW    — int seconds (default 60)
//	MDDB_RATE_LIMIT_BURST     — int   (default 50)
//	MDDB_RATE_LIMIT_BY        — "ip" | "user" (default "ip")
//
// The algorithm mirrors MCPRateLimiter (see mcp_middleware.go) so
// behaviour and headers line up across transports.
type RateLimiter struct {
	enabled   bool
	limit     int
	window    time.Duration
	burst     int
	by        string
	mu        sync.Mutex
	clients   map[string]*rlBucket
	cleanupAt time.Time
	exempt    map[string]struct{} // HTTP paths that bypass the limit
	// onReject fires security.rate_limit_exceeded on the incident
	// webhook channel. Wired from Server after NewWebhookManager —
	// stays nil in unit tests so they don't need a full server.
	onReject func(clientID, transport string)
}

// SetWebhookManager wires the incident-event publisher used to fire
// security.rate_limit_exceeded on burst. Safe on nil.
func (rl *RateLimiter) SetWebhookManager(wm *webhooks.WebhookManager) {
	if rl == nil || wm == nil {
		return
	}
	rl.onReject = func(clientID, transport string) {
		wm.FireEvent(webhooks.EventRateLimitExceeded, map[string]interface{}{
			"clientId":  clientID,
			"transport": transport,
		})
	}
}

type rlBucket struct {
	count    int
	windowAt time.Time
}

// NewRateLimiter builds a limiter from the environment.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		enabled: os.Getenv("MDDB_RATE_LIMIT_ENABLED") == "true",
		clients: make(map[string]*rlBucket),
		exempt: map[string]struct{}{
			"/health":    {},
			"/v1/health": {},
			"/metrics":   {},
		},
	}
	if !rl.enabled {
		return rl
	}
	rl.limit, _ = strconv.Atoi(os.Getenv("MDDB_RATE_LIMIT_REQUESTS"))
	if rl.limit <= 0 {
		rl.limit = 100
	}
	windowSec, _ := strconv.Atoi(os.Getenv("MDDB_RATE_LIMIT_WINDOW"))
	if windowSec <= 0 {
		windowSec = 60
	}
	rl.window = time.Duration(windowSec) * time.Second
	if v := os.Getenv("MDDB_RATE_LIMIT_BURST"); v == "" {
		rl.burst = 50
	} else {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			rl.burst = 50
		} else {
			rl.burst = n
		}
	}
	rl.by = os.Getenv("MDDB_RATE_LIMIT_BY")
	if rl.by != "user" {
		rl.by = "ip"
	}
	// #nosec G706 -- all values come from env config, not request data
	slog.Info("rate limiting enabled",
		"limit", rl.limit, "windowSec", windowSec, "burst", rl.burst, "by", rl.by)
	return rl
}

// Enabled reports whether the limiter will actually enforce anything.
func (rl *RateLimiter) Enabled() bool { return rl != nil && rl.enabled }

// allow is the core accounting step. Returns remaining budget,
// reset timestamp (unix seconds) and whether the request may proceed.
func (rl *RateLimiter) allow(clientID string) (remaining int, resetAt int64, allowed bool) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

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
		bucket = &rlBucket{windowAt: now.Add(rl.window)}
		rl.clients[clientID] = bucket
	}

	effective := rl.limit + rl.burst
	bucket.count++
	remaining = effective - bucket.count
	if remaining < 0 {
		remaining = 0
	}
	resetAt = bucket.windowAt.Unix()
	allowed = bucket.count <= effective
	return
}

// HTTPMiddleware returns a net/http middleware that enforces the
// limit and populates X-RateLimit-* response headers.
func (rl *RateLimiter) HTTPMiddleware(next http.Handler) http.Handler {
	if !rl.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := rl.exempt[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		id := rl.httpClientID(r)
		remaining, resetAt, allowed := rl.allow(id)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.limit+rl.burst))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt, 10))
		if !allowed {
			retryAfter := resetAt - time.Now().Unix()
			if retryAfter < 1 {
				retryAfter = 1
			}
			if rl.onReject != nil {
				rl.onReject(id, "http")
			}
			w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprintf(w, `{"error":"rate limit exceeded","retryAfter":%d}`, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) httpClientID(r *http.Request) string {
	if rl.by == "user" {
		if claims, ok := r.Context().Value(authContextKey).(*JWTClaims); ok && claims != nil && claims.Username != "" {
			return "user:" + claims.Username
		}
	}
	return "ip:" + ClientIP(r)
}

// UnaryInterceptor returns a grpc UnaryServerInterceptor enforcing
// the same budget as HTTPMiddleware. Emits ResourceExhausted when
// the limit is exceeded.
func (rl *RateLimiter) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !rl.Enabled() {
			return handler(ctx, req)
		}
		id := grpcClientID(ctx, rl.by)
		if _, _, allowed := rl.allow(id); !allowed {
			if rl.onReject != nil {
				rl.onReject(id, "grpc")
			}
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor is the streaming counterpart — the budget is
// spent once per RPC start (not per message) so long-lived streams
// do not get starved by per-message accounting.
func (rl *RateLimiter) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !rl.Enabled() {
			return handler(srv, ss)
		}
		id := grpcClientID(ss.Context(), rl.by)
		if _, _, allowed := rl.allow(id); !allowed {
			if rl.onReject != nil {
				rl.onReject(id, "grpc-stream")
			}
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, ss)
	}
}

func grpcClientID(ctx context.Context, by string) string {
	if by == "user" {
		if claims, ok := ctx.Value(authContextKey).(*JWTClaims); ok && claims != nil && claims.Username != "" {
			return "user:" + claims.Username
		}
	}
	return "ip:" + grpcPeerIP(ctx)
}

func grpcPeerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.Addr == nil {
		return "unknown"
	}
	addr := p.Addr.String()
	// Strip port for tcp-style addrs. Unix sockets carry the path and
	// are identified by the whole string — the limit still applies.
	if i := strings.LastIndex(addr, ":"); i > 0 && !strings.HasPrefix(addr, "/") {
		return addr[:i]
	}
	return addr
}
