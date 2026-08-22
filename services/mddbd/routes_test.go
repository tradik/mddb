package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TEST-002. These 107 registrations were unreachable inside a 1089-line main(),
// so nothing ever asserted what the server actually exposes. A route quietly
// dropped in a refactor, or registered twice, would have shipped.

// registeredRoutes walks the mux by asking it to resolve each candidate path,
// which is the only way to learn what a ServeMux holds.
func registeredRoutes(t *testing.T, mux *http.ServeMux, candidates []string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, path := range candidates {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		_, pattern := mux.Handler(req)
		if pattern != "" {
			found[path] = true
		}
	}
	return found
}

func routeServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)
	srv.CollectionManager = NewCollectionManager(srv.DB)
	if err := srv.CollectionManager.EnsureBucket(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	return srv, cleanup
}

// The endpoints listed by /v1/endpoints are MDDB's own statement of what it
// serves. Every one of them must actually be registered, or the server is
// advertising something that returns 404.
func TestEveryAdvertisedEndpointIsRegistered(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	srv.registerRoutes(mux, false)

	var missing []string
	for _, ep := range srv.httpEndpointCatalogue(false) {
		// Only exact paths can be checked this way; a trailing-slash pattern
		// matches by prefix and is covered separately below.
		if strings.HasSuffix(ep.Path, "/") || strings.Contains(ep.Path, "{") {
			continue
		}
		req := httptest.NewRequest(http.MethodGet, ep.Path, nil)
		if _, pattern := mux.Handler(req); pattern == "" || pattern == "/" {
			missing = append(missing, ep.Path)
		}
	}

	if len(missing) > 0 {
		t.Errorf("the server advertises %d endpoints it does not serve:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// Auth routes must appear only when auth is on. Registering them regardless
// would expose login and user management on a server configured without it.
func TestAuthRoutesFollowTheAuthFlag(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()

	authPaths := []string{
		"/v1/auth/login",
		"/v1/auth/register",
		"/v1/auth/users",
		"/v1/auth/groups",
		"/v1/auth/permissions",
	}

	off := http.NewServeMux()
	srv.registerRoutes(off, false)
	if got := registeredRoutes(t, off, authPaths); len(got) != 0 {
		t.Errorf("auth routes are served with auth disabled: %v", got)
	}

	on := http.NewServeMux()
	srv.registerRoutes(on, true)
	got := registeredRoutes(t, on, authPaths)
	for _, p := range authPaths {
		if !got[p] {
			t.Errorf("%s is not served with auth enabled", p)
		}
	}
}

// Registering the same pattern twice panics ServeMux at startup. That is a
// crash on boot, which is the worst moment to discover a merge conflict
// resolved badly.
func TestRoutesRegisterWithoutCollision(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()

	for _, authEnabled := range []bool{false, true} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("registering routes with authEnabled=%v panicked: %v", authEnabled, r)
				}
			}()
			srv.registerRoutes(http.NewServeMux(), authEnabled)
		}()
	}
}

// A read-only server must not accept writes. The guard is applied route by
// route, so a new endpoint added without it is invisible until someone writes
// to a replica.
func TestWriteRoutesAreGuardedInReadOnlyMode(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()
	srv.Mode = ModeRead

	mux := http.NewServeMux()
	srv.registerRoutes(mux, false)

	// Endpoints that change data. Each must refuse in read-only mode.
	writePaths := []string{
		"/v1/add",
		"/v1/update",
		"/v1/delete",
		"/v1/upload",
		"/v1/ingest",
		"/v1/set-ttl",
		"/v1/import-url",
	}

	for _, path := range writePaths {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s returned %d in read-only mode, want 403: %s",
				path, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// Health must answer without auth, without a database write, and on both of
// its paths — it is what a load balancer asks before sending traffic.
func TestHealthIsServedOnBothPaths(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()

	// Health answers 503 "warming_up" until startup finishes — that is what
	// it is for. A started server is what a load balancer asks about.
	srv.Ready = true

	mux := http.NewServeMux()
	srv.registerRoutes(mux, true) // even with auth on

	for _, path := range []string{"/health", "/v1/health"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("%s returned %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "healthy") {
			t.Errorf("%s did not report health: %s", path, rec.Body.String())
		}
	}
}

// An unregistered path must 404 rather than being swallowed by a catch-all,
// which would turn every typo into a silent success.
func TestUnknownPathIsNotFound(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()

	mux := http.NewServeMux()
	srv.registerRoutes(mux, false)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/no-such-endpoint", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown path returned %d, want 404", rec.Code)
	}
}

// pprof exposes process internals and must stay off unless asked for.
func TestPprofIsOffByDefault(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()

	t.Setenv("MDDB_PPROF_ENABLED", "false")
	off := http.NewServeMux()
	srv.registerRoutes(off, false)
	if got := registeredRoutes(t, off, []string{"/debug/pprof/"}); len(got) != 0 {
		t.Error("pprof is served without being enabled")
	}

	t.Setenv("MDDB_PPROF_ENABLED", "true")
	on := http.NewServeMux()
	srv.registerRoutes(on, false)
	if got := registeredRoutes(t, on, []string{"/debug/pprof/"}); len(got) == 0 {
		t.Error("pprof is not served when enabled")
	}
}
