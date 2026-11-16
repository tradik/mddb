package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"mddb/internal/envconf"
)

// Per-tenant and per-credential request limits (GO-054).
//
// The four surfaces apply them at their access gate, right after the request
// is authorized — REST in httpGate, gRPC in its access interceptors, MCP in
// mcpCallTool, GraphQL per root field — so one tenant's burst is refused the
// same way wherever it comes from, before it costs the server anything:
//
//   - a rate per second for reads (searches included) and for writes, for the
//     tenant as a whole and for each of its credentials;
//   - a number of write requests the tenant may run at once;
//   - a number of the server's search slots (GO-053) the tenant may hold,
//     taken in searchSlot so every surface's searches count;
//   - a number of vector reindexes, per tenant and for the server
//     (reindex_slots.go).
//
// Every default is 0 — no limit — so a server that sets nothing, and a user
// without a tenant, behave exactly as before. Credential limits also apply to
// global users when MDDB_KEY_READ_RPS / MDDB_KEY_WRITE_RPS are set.

// tenantRuntime is the server's limiter state, built on first use.
type tenantRuntime struct {
	defaults tenantDefaults
	wait     time.Duration // how long a request waits for a concurrency slot
	now      func() time.Time

	mu       sync.Mutex
	buckets  map[string]*rateBucket
	slots    map[string]chan struct{}
	refusals map[string]map[string]uint64 // tenant → reason → count
	queues   map[string]*queueStat        // tenant|queue → wait totals

	reindexAll chan struct{} // MDDB_REINDEX_MAX_CONCURRENT; nil = no limit
}

// rateBucket is a token bucket refilled at rate tokens a second, holding at
// most rate: a client may spend one second's allowance at once.
type rateBucket struct {
	tokens float64
	last   time.Time
}

// admittedKey marks a context whose request already passed admitRequest, so
// a request crossing a second gate (a REST route reaching the shared core) is
// counted once.
type admittedKey struct{}

// apiKeyIDKey carries the identity of the API key a request authenticated
// with: a prefix of its hash, never the key.
type apiKeyIDKey struct{}

// withAPIKeyID records the API key a request authenticated with.
func withAPIKeyID(ctx context.Context, apiKey string) context.Context {
	return context.WithValue(ctx, apiKeyIDKey{}, HashAPIKey(apiKey)[:16])
}

// credentialOf is the credential a request is counted against: its API key,
// or its user.
func credentialOf(ctx context.Context, claims *JWTClaims) string {
	if id, ok := ctx.Value(apiKeyIDKey{}).(string); ok {
		return "key:" + id
	}
	return "user:" + claims.Username
}

// tenantLimiter is the server's limiter state, read from the environment the
// first time.
func (s *Server) tenantLimiter() *tenantRuntime {
	s.tenantOnce.Do(func() {
		rt := &tenantRuntime{
			defaults: readTenantDefaults(),
			wait:     time.Duration(max(envconf.Int("MDDB_TENANT_QUEUE_TIMEOUT_MS", 2000), 0)) * time.Millisecond,
			now:      time.Now,
			buckets:  map[string]*rateBucket{},
			slots:    map[string]chan struct{}{},
			refusals: map[string]map[string]uint64{},
			queues:   map[string]*queueStat{},
		}
		if n := envconf.Int("MDDB_REINDEX_MAX_CONCURRENT", 0); n > 0 {
			rt.reindexAll = make(chan struct{}, n)
		}
		s.tenantRT = rt
	})
	return s.tenantRT
}

// tenantDefaults is the server's default tenant limits.
func (s *Server) tenantDefaults() tenantDefaults { return s.tenantLimiter().defaults }

// effectiveLimits is what applies to a tenant: its override over the defaults.
func (s *Server) effectiveLimits(tenant string) TenantLimits {
	return s.tenantDefaults().effective(s.tenantConfig(tenant).Limits)
}

// admitRequest applies the rate limits of the request's class — op is
// PermRead for reads and searches, anything else a write — and, for a write,
// takes one of the tenant's write slots. The returned release must be called
// when the request ends; the returned context marks the request admitted.
func (s *Server) admitRequest(ctx context.Context, op PermissionType) (context.Context, func(), error) {
	claims, ok := GetClaimsFromContext(ctx)
	if !ok || isAdmitted(ctx) {
		return ctx, noRelease, nil
	}
	rt := s.tenantLimiter()
	tenant := claims.Tenant
	lim := s.effectiveLimits(tenant)
	write := op != PermRead
	tenantRate, keyRate, kind := lim.ReadPerSecond, lim.KeyReadPerSecond, limitReadRate
	if write {
		tenantRate, keyRate, kind = lim.WritePerSecond, lim.KeyWritePerSecond, limitWriteRate
	}
	if tenant != "" && !rt.allow("t|"+kind+"|"+tenant, tenantRate) {
		return ctx, noRelease, s.refuse(&TenantLimitError{Tenant: tenant, Scope: "tenant", Kind: kind, Limit: tenantRate, RetryAfter: time.Second})
	}
	if !rt.allow("k|"+kind+"|"+credentialOf(ctx, claims), keyRate) {
		return ctx, noRelease, s.refuse(&TenantLimitError{Tenant: tenant, Scope: "key", Kind: kind, Limit: keyRate, RetryAfter: time.Second})
	}
	release := noRelease
	if write && tenant != "" && lim.MaxConcurrentWrites > 0 {
		var err error
		if release, err = rt.acquire(ctx, "w|"+tenant, lim.MaxConcurrentWrites); err != nil {
			return ctx, noRelease, s.slotRefusal(err, tenant, limitWriteConcurrency, lim.MaxConcurrentWrites)
		}
	}
	return markAdmitted(ctx), release, nil
}

// markAdmitted returns ctx marked as having passed admitRequest.
func markAdmitted(ctx context.Context) context.Context {
	return context.WithValue(ctx, admittedKey{}, true)
}

// isAdmitted reports whether ctx already passed admitRequest.
func isAdmitted(ctx context.Context) bool {
	return ctx.Value(admittedKey{}) != nil
}

// tenantSearchSlot takes one of the tenant's search slots, when it has a
// limit; nil release means there was nothing to take.
func (s *Server) tenantSearchSlot(ctx context.Context) (func(), error) {
	tenant := TenantFromContext(ctx)
	if tenant == "" {
		return nil, nil
	}
	n := s.effectiveLimits(tenant).MaxConcurrentSearches
	if n <= 0 {
		return nil, nil
	}
	release, err := s.tenantLimiter().acquire(ctx, "s|"+tenant, n)
	if err != nil {
		return nil, s.slotRefusal(err, tenant, limitSearchConcurrency, n)
	}
	return release, nil
}

// slotRefusal turns a failed acquire into the error to return: the client's
// own cancellation as it is, a timeout as a counted TenantLimitError.
func (s *Server) slotRefusal(err error, tenant, kind string, limit int) error {
	if !errors.Is(err, errSlotTimeout) {
		return err
	}
	return s.refuse(&TenantLimitError{Tenant: tenant, Scope: "tenant", Kind: kind, Limit: limit, RetryAfter: time.Second})
}

// allow spends one token of key's bucket; rate <= 0 is no limit.
func (rt *tenantRuntime) allow(key string, rate int) bool {
	if rate <= 0 {
		return true
	}
	now := rt.now()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	b := rt.buckets[key]
	if b == nil {
		b = &rateBucket{tokens: float64(rate), last: now}
		rt.buckets[key] = b
	}
	b.tokens = min(float64(rate), b.tokens+now.Sub(b.last).Seconds()*float64(rate))
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// errSlotTimeout is acquire giving up after rt.wait.
var errSlotTimeout = errors.New("no slot free in time")

// acquire takes one of n slots under key, waiting up to rt.wait. A changed n
// takes effect for new requests; the ones running release into the set they
// came from.
func (rt *tenantRuntime) acquire(ctx context.Context, key string, n int) (func(), error) {
	ch := rt.slotChan(key, n)
	timer := time.NewTimer(rt.wait)
	defer timer.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errSlotTimeout
	}
}

// slotChan is the set of n slots under key, made anew when n changed.
func (rt *tenantRuntime) slotChan(key string, n int) chan struct{} {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	ch := rt.slots[key]
	if cap(ch) != n {
		ch = make(chan struct{}, n)
		rt.slots[key] = ch
	}
	return ch
}

// inUse is how many of key's slots are taken.
func (rt *tenantRuntime) inUse(key string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.slots[key])
}

// refuse counts a refusal for the tenant — reason "tenant.<kind>" or
// "key.<kind>" — and returns it.
func (s *Server) refuse(te *TenantLimitError) error {
	s.countTenantRefusal(te.Tenant, te.Scope+"."+te.Kind)
	return te
}
