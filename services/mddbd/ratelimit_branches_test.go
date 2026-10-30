package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

func TestRateLimiterSetWebhookManager(t *testing.T) {
	var nilRL *RateLimiter
	nilRL.SetWebhookManager(nil) // safe on nil

	rl := setupRateLimiter(t, 1, 0, 60, "ip")
	rl.SetWebhookManager(nil)
	if rl.onReject != nil {
		t.Fatal("a nil webhook manager must leave onReject unset")
	}
	rl.SetWebhookManager(newIncidentWebhookManager(t))
	if rl.onReject == nil {
		t.Fatal("onReject not wired")
	}
	rl.onReject("ip:203.0.113.5", "http") // fires the event; no subscribers, no panic
}

func TestNewRateLimiterInvalidBurst(t *testing.T) {
	setupRateLimiter(t, 5, 0, 60, "ip")
	t.Setenv("MDDB_RATE_LIMIT_BURST", "abc")
	rl := NewRateLimiter()
	if rl.burst != 50 {
		t.Errorf("burst = %d, want the default 50", rl.burst)
	}
}

func TestRateLimiterCleanupDropsExpiredBuckets(t *testing.T) {
	rl := setupRateLimiter(t, 5, 0, 60, "ip")
	rl.clients["ip:old"] = &rlBucket{count: 3, windowAt: time.Now().Add(-time.Minute)}
	rl.cleanupAt = time.Now().Add(-time.Second)
	rl.allow("ip:new")
	if _, ok := rl.clients["ip:old"]; ok {
		t.Error("expired bucket survived the cleanup")
	}
	if _, ok := rl.clients["ip:new"]; !ok {
		t.Error("new bucket missing")
	}
}

// TestRateLimiterRejectHooksAndShortRetry covers onReject on every transport
// and the Retry-After floor of one second for a window ending within the second.
func TestRateLimiterRejectHooksAndShortRetry(t *testing.T) {
	withTrustedProxies(t, "")
	rl := setupRateLimiter(t, 1, 0, 60, "ip")
	var rejected []string
	rl.onReject = func(_, transport string) { rejected = append(rejected, transport) }

	h := rl.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	rl.clients["ip:203.0.113.5"] = &rlBucket{count: 1, windowAt: time.Now().Add(200 * time.Millisecond)}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, requestFrom("203.0.113.5:1", "", ""))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Errorf("HTTP = %d Retry-After %q, want 429 and 1", w.Code, w.Header().Get("Retry-After"))
	}

	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.7"), Port: 1}})
	unary := rl.UnaryInterceptor()
	uh := func(context.Context, interface{}) (interface{}, error) { return nil, nil }
	_, _ = unary(ctx, nil, nil, uh)
	if _, err := unary(ctx, nil, nil, uh); err == nil {
		t.Error("second unary call should be rejected")
	}

	stream := rl.StreamInterceptor()
	ss := &mockServerStream{ctx: peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.8"), Port: 1}})}
	noop := grpc.StreamHandler(func(interface{}, grpc.ServerStream) error { return nil })
	_ = stream(nil, ss, nil, noop)
	if err := stream(nil, ss, nil, noop); err == nil {
		t.Error("second stream should be rejected")
	}

	want := []string{"http", "grpc", "grpc-stream"}
	if len(rejected) != len(want) {
		t.Fatalf("rejected = %v, want %v", rejected, want)
	}
	for i := range want {
		if rejected[i] != want[i] {
			t.Errorf("rejected[%d] = %q, want %q", i, rejected[i], want[i])
		}
	}
}

func TestRateLimiterStreamDisabledPassthrough(t *testing.T) {
	t.Setenv("MDDB_RATE_LIMIT_ENABLED", "")
	stream := NewRateLimiter().StreamInterceptor()
	called := 0
	noop := grpc.StreamHandler(func(interface{}, grpc.ServerStream) error { called++; return nil })
	for i := 0; i < 3; i++ {
		_ = stream(nil, &mockServerStream{ctx: context.Background()}, nil, noop)
	}
	if called != 3 {
		t.Errorf("disabled stream interceptor called the handler %d times, want 3", called)
	}
}
