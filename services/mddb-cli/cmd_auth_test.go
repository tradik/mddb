package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TEST-001. Authentication commands. The credentials the CLI sends and the
// header it attaches are the parts that fail silently: a token that never
// reaches the server looks exactly like a permission problem.

func TestLoginPrintsTheTokenAndHowToUseIt(t *testing.T) {
	expires := time.Now().Add(time.Hour).Unix()
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/auth/login": map[string]interface{}{
			"token": "jwt.value.here", "expiresAt": expires,
		},
	})

	out, err := runCLI(t, fs.URL, "login", "admin", "hunter2")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	body := fs.lastCall(t).Body
	assertBodyField(t, body, "username", "admin")
	assertBodyField(t, body, "password", "hunter2")

	mustContain(t, out, "jwt.value.here")
	// The token is useless without the flag that carries it.
	mustContain(t, out, "--token")
}

func TestLoginWithBadCredentialsExitsNonZero(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/auth/login": failure{http.StatusUnauthorized, `{"error":"invalid credentials"}`},
	})

	out, err := runCLI(t, fs.URL, "login", "admin", "wrong")
	if err == nil {
		t.Fatal("a rejected login exited zero")
	}
	if strings.Contains(out, "✓") {
		t.Errorf("a failed login printed a success mark:\n%s", out)
	}
}

// A token given on the command line must actually travel with the request.
func TestTokenFlagReachesTheAuthorizationHeader(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/stats": map[string]interface{}{"mode": "wr"},
	})

	if _, err := runCLI(t, fs.URL, "--token", "jwt.value.here", "stats"); err != nil {
		t.Fatalf("stats failed: %v", err)
	}

	got := fs.lastCall(t).Header.Get("Authorization")
	if got != "Bearer jwt.value.here" {
		t.Errorf("Authorization = %q, want a bearer token", got)
	}
}

func TestAPIKeyFlagReachesItsHeader(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/stats": map[string]interface{}{"mode": "wr"},
	})

	if _, err := runCLI(t, fs.URL, "--api-key", "mddb_secret", "stats"); err != nil {
		t.Fatal(err)
	}

	call := fs.lastCall(t)
	if call.Header.Get("X-API-Key") == "" && call.Header.Get("Authorization") == "" {
		t.Errorf("neither an API-key nor an Authorization header was sent: %v", call.Header)
	}
}

// Creating a key needs a JWT; refusing locally beats a 401 the user has to
// interpret.
func TestAPIKeyCreateRefusesWithoutAToken(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/auth/api-key": map[string]interface{}{"key": "should not be reached"},
	})

	_, err := runCLI(t, fs.URL, "api-key", "create", "--description", "ci")
	if err == nil {
		t.Fatal("api-key create ran without a token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
	if calls := fs.calls(); len(calls) != 0 {
		t.Errorf("a token-less request reached the server: %v", calls)
	}
}

func TestAPIKeyCreateSendsItsDescription(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/auth/api-key": map[string]interface{}{
			"key": "mddb_generated", "description": "ci", "createdAt": 1700000000,
		},
	})

	out, err := runCLI(t, fs.URL, "--token", "jwt", "api-key", "create",
		"--description", "ci", "--expires-at", "1800000000")
	if err != nil {
		t.Fatalf("api-key create failed: %v", err)
	}

	body := fs.lastCall(t).Body
	assertBodyField(t, body, "description", "ci")
	assertBodyField(t, body, "expiresAt", 1800000000)

	// The key is shown once and never again; it must be in the output.
	mustContain(t, out, "mddb_generated")
}

// expires-at is omitted rather than sent as zero: a zero would read as "expired
// at the epoch" to a server that takes the field literally.
func TestAPIKeyCreateOmitsAnUnsetExpiry(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/auth/api-key": map[string]interface{}{"key": "k", "createdAt": 1700000000},
	})

	if _, err := runCLI(t, fs.URL, "--token", "jwt", "api-key", "create",
		"--description", "no expiry"); err != nil {
		t.Fatal(err)
	}
	if _, present := fs.lastCall(t).Body["expiresAt"]; present {
		t.Error("an unset --expires-at was sent as a value")
	}
}

func TestAPIKeyListRendersTheKeys(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/auth/api-keys": map[string]interface{}{
			"keys": []map[string]interface{}{
				{"keyHash": "abc123", "description": "ci", "createdAt": 1700000000},
			},
		},
	})

	out, err := runCLI(t, fs.URL, "--token", "jwt", "api-key", "list")
	if err != nil {
		t.Fatalf("api-key list failed: %v", err)
	}
	mustContain(t, out, "abc123")
}

func TestAPIKeyDeleteAddressesTheKey(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/auth/api-keys/abc123": map[string]interface{}{"deleted": true},
	})

	if _, err := runCLI(t, fs.URL, "--token", "jwt", "api-key", "delete", "abc123"); err != nil {
		t.Fatalf("api-key delete failed: %v", err)
	}

	call := fs.lastCall(t)
	if call.Method != http.MethodDelete {
		t.Errorf("delete used %s, want DELETE", call.Method)
	}
	if !strings.HasSuffix(call.Path, "/abc123") {
		t.Errorf("path = %q, want it to end with the key hash", call.Path)
	}
}

// Without a flag no credential must be attached: an accidental header would
// authenticate a command the user meant to run anonymously.
func TestNoCredentialFlagsMeansNoCredentialHeaders(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/stats": map[string]interface{}{"mode": "wr"},
	})

	if _, err := runCLI(t, fs.URL, "stats"); err != nil {
		t.Fatal(err)
	}

	call := fs.lastCall(t)
	for _, h := range []string{"Authorization", "X-API-Key"} {
		if v := call.Header.Get(h); v != "" {
			t.Errorf("%s was sent without being asked for: %q", h, v)
		}
	}
}

// GO-005, found by TEST-001: the listing sliced keyHash[:16] without checking
// its length, so a short or missing hash crashed the CLI with a stack trace.
func TestAPIKeyListSurvivesAShortHash(t *testing.T) {
	for name, hash := range map[string]interface{}{
		"short":           "abc",
		"empty":           "",
		"missing":         nil,
		"exactly sixteen": "0123456789abcdef",
	} {
		t.Run(name, func(t *testing.T) {
			fs := newFakeServer(t, map[string]interface{}{
				"/v1/auth/api-keys": map[string]interface{}{
					"keys": []map[string]interface{}{
						{"keyHash": hash, "description": "d", "createdAt": 1700000000},
					},
				},
			})

			if _, err := runCLI(t, fs.URL, "--token", "jwt", "api-key", "list"); err != nil {
				t.Errorf("api-key list failed on a %s hash: %v", name, err)
			}
		})
	}
}

func TestShorten(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"shorter than the limit": {"abc", "abc"},
		"exactly the limit":      {"0123456789", "0123456789"},
		"longer":                 {"0123456789x", "0123456789..."},
		"empty":                  {"", ""},
	}
	for name, c := range cases {
		if got := shorten(c.in, 10); got != c.want {
			t.Errorf("%s: shorten(%q, 10) = %q, want %q", name, c.in, got, c.want)
		}
	}
	// A non-positive limit means "do not shorten", not "return nothing".
	if got := shorten("abcdef", 0); got != "abcdef" {
		t.Errorf("shorten with a zero limit returned %q", got)
	}
}
