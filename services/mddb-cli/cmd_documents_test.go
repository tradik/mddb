package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TEST-001. Document commands: what goes out on the wire, what comes back to
// the terminal, and what happens when the server says no.

func TestAddSendsWhatTheUserTyped(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/add": map[string]interface{}{
			"id": "blog|post|en", "key": "post", "lang": "en",
			"addedAt": 1700000000, "updatedAt": 1700000000,
		},
	})

	out, err := runCLIWithStdin(t, fs.URL, "# Hello\n",
		"add", "blog", "post", "en", "--meta", "tag=go|rust,status=draft")
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	call := fs.lastCall(t)
	if call.Method != http.MethodPost || call.Path != "/v1/add" {
		t.Errorf("called %s %s, want POST /v1/add", call.Method, call.Path)
	}
	assertBodyField(t, call.Body, "collection", "blog")
	assertBodyField(t, call.Body, "key", "post")
	assertBodyField(t, call.Body, "lang", "en")
	assertBodyField(t, call.Body, "contentMd", "# Hello\n")
	assertBodyField(t, call.Body, "meta", map[string][]string{
		"tag": {"go", "rust"}, "status": {"draft"},
	})

	mustContain(t, out, "blog|post|en")
}

func TestAddReadsAFileWhenGivenOne(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/add": map[string]interface{}{"id": "x", "addedAt": 1700000000},
	})

	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("from a file"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLI(t, fs.URL, "add", "blog", "post", "en", "--file", path); err != nil {
		t.Fatalf("add --file failed: %v", err)
	}
	assertBodyField(t, fs.lastCall(t).Body, "contentMd", "from a file")
}

// A missing file must fail before anything is sent: a half-written document is
// worse than none.
func TestAddWithAMissingFileSendsNothing(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/add": map[string]interface{}{"id": "x"},
	})

	_, err := runCLI(t, fs.URL, "add", "blog", "post", "en",
		"--file", filepath.Join(t.TempDir(), "does-not-exist.md"))
	if err == nil {
		t.Fatal("a missing --file was accepted")
	}
	if calls := fs.calls(); len(calls) != 0 {
		t.Errorf("the command sent %d requests before failing on the file", len(calls))
	}
}

func TestAddSurfacesAServerRejection(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/add": failure{http.StatusForbidden, `{"error":"forbidden"}`},
	})

	_, err := runCLIWithStdin(t, fs.URL, "content", "add", "blog", "post", "en")
	if err == nil {
		t.Fatal("a 403 was reported as success — the shell would see exit 0")
	}
}

func TestGetPrintsTheDocument(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/get": map[string]interface{}{
			"id": "blog|post|en", "key": "post", "lang": "en",
			"contentMd": "# The content",
			"meta":      map[string][]string{"tag": {"go"}},
			"addedAt":   1700000000, "updatedAt": 1700000000,
		},
	})

	out, err := runCLI(t, fs.URL, "get", "blog", "post", "en")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}

	for _, want := range []string{"blog|post|en", "post", "# The content", "tag"} {
		mustContain(t, out, want)
	}
	// A real timestamp must be shown as a date, not as a raw number.
	if strings.Contains(out, "1700000000") {
		t.Errorf("a raw Unix timestamp reached the terminal:\n%s", out)
	}
}

// --content-only exists to be piped into a file; anything else in the output
// would corrupt it.
func TestGetContentOnlyPrintsNothingElse(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/get": map[string]interface{}{
			"id": "blog|post|en", "key": "post", "contentMd": "# Just this",
			"addedAt": 1700000000,
		},
	})

	out, err := runCLI(t, fs.URL, "get", "blog", "post", "en", "--content-only")
	if err != nil {
		t.Fatal(err)
	}
	if out != "# Just this" {
		t.Errorf("--content-only printed %q, want only the content", out)
	}
}

func TestGetPassesEnvForTemplating(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/get": map[string]interface{}{"id": "x", "contentMd": ""},
	})

	if _, err := runCLI(t, fs.URL, "get", "blog", "post", "en",
		"--env", "user=ada,role=admin"); err != nil {
		t.Fatal(err)
	}
	assertBodyField(t, fs.lastCall(t).Body, "env", map[string]string{
		"user": "ada", "role": "admin",
	})
}

// GO-005: the CLI used to crash on a response missing the fields it expected.
func TestGetSurvivesADegenerateResponse(t *testing.T) {
	for name, body := range map[string]string{
		"empty object":      `{}`,
		"null timestamps":   `{"addedAt":null,"updatedAt":null}`,
		"wrong-typed field": `{"addedAt":"yesterday","meta":"not a map"}`,
		"error object":      `{"error":"gone"}`,
	} {
		t.Run(name, func(t *testing.T) {
			fs := newFakeServer(t, map[string]interface{}{"/v1/get": body})
			out, err := runCLI(t, fs.URL, "get", "blog", "post", "en")
			if err != nil {
				t.Fatalf("get returned an error on %s: %v", name, err)
			}
			// A missing timestamp is shown as "-", not as the year 1970.
			if strings.Contains(out, "1970") {
				t.Errorf("a missing timestamp was printed as an epoch date:\n%s", out)
			}
		})
	}
}

// Malformed JSON is a different failure from a missing field, and must be
// reported rather than printed as gibberish.
func TestGetReportsUnparseableJSON(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/get": `{"key": bro`})

	if _, err := runCLI(t, fs.URL, "get", "blog", "post", "en"); err == nil {
		t.Fatal("a truncated JSON response was reported as success")
	}
}

func TestSetTTLSendsTheSeconds(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/set-ttl": map[string]interface{}{"ok": true},
	})

	if _, err := runCLI(t, fs.URL, "set-ttl", "blog", "post", "en", "--ttl", "3600"); err != nil {
		t.Fatalf("set-ttl failed: %v", err)
	}
	assertBodyField(t, fs.lastCall(t).Body, "ttl", 3600)
}

func TestImportURLSendsTheSourceAddress(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/import-url": map[string]interface{}{
			"id": "docs|readme|en", "key": "readme", "addedAt": 1700000000,
		},
	})

	if _, err := runCLI(t, fs.URL, "import-url", "docs",
		"https://example.test/readme.md", "en", "--meta", "source=web"); err != nil {
		t.Fatalf("import-url failed: %v", err)
	}

	call := fs.lastCall(t)
	assertBodyField(t, call.Body, "collection", "docs")
	assertBodyField(t, call.Body, "url", "https://example.test/readme.md")
	assertBodyField(t, call.Body, "meta", map[string][]string{"source": {"web"}})
}

// Cobra enforces the argument count; a wrong invocation must not reach the
// server as a request with empty fields.
func TestCommandsRejectTheWrongNumberOfArguments(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{})

	for name, args := range map[string][]string{
		"add without a language": {"add", "blog", "post"},
		"get with too many":      {"get", "blog", "post", "en", "extra"},
		"set-ttl without a key":  {"set-ttl", "blog"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runCLI(t, fs.URL, args...); err == nil {
				t.Error("the command was accepted with the wrong argument count")
			}
			if calls := fs.calls(); len(calls) != 0 {
				t.Errorf("a malformed invocation reached the server: %v", calls)
			}
		})
	}
}
