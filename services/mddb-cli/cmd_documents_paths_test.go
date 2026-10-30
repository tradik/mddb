package main

import (
	"strings"
	"testing"
)

// The document commands' other outputs: --json passes the server's answer
// through, and an answer that is not JSON is an error, not a printout.

func TestDocumentCommandsPrintJSONAsTheServerSentIt(t *testing.T) {
	answer := `{"id":"blog|post|en","marker":"raw"}`
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/add": answer, "/v1/get": answer, "/v1/import-url": answer, "/v1/set-ttl": answer,
	})
	for _, args := range [][]string{
		{"add", "blog", "post", "en"},
		{"get", "blog", "post", "en"},
		{"import-url", "blog", "https://example.test/post.md", "en"},
		{"set-ttl", "blog", "post", "en", "--ttl", "60"},
	} {
		out, err := runCLIWithStdin(t, fs.URL, "body", append([]string{"--json"}, args...)...)
		if err != nil {
			t.Fatalf("%s --json: %v", args[0], err)
		}
		mustContain(t, out, `"marker":"raw"`)
	}
}

func TestDocumentCommandsRejectAnAnswerThatIsNotJSON(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/add": `{"id": bro`, "/v1/get": `{"id": bro`, "/v1/import-url": `{"id": bro`,
	})
	for _, args := range [][]string{
		{"add", "blog", "post", "en"},
		{"get", "blog", "post", "en", "--content-only"},
		{"import-url", "blog", "https://example.test/post.md", "en"},
	} {
		if _, err := runCLIWithStdin(t, fs.URL, "body", args...); err == nil || !strings.Contains(err.Error(), "parse response") {
			t.Errorf("%s on a truncated answer: err = %v, want parse response", args[0], err)
		}
	}
}

func TestImportURLSendsTheKeyAndTTL(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/import-url": map[string]interface{}{"id": "x"}})

	if _, err := runCLI(t, fs.URL, "import-url", "docs", "https://example.test/a.md", "en", "--key", "a", "--ttl", "90"); err != nil {
		t.Fatal(err)
	}
	body := fs.lastCall(t).Body
	assertBodyField(t, body, "key", "a")
	assertBodyField(t, body, "ttl", 90)
}

func TestSetTTLZeroRemovesIt(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/set-ttl": map[string]interface{}{"ok": true}})

	out, err := runCLI(t, fs.URL, "set-ttl", "blog", "post", "en", "--ttl", "0")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "TTL removed for blog/post/en")
}

// Every request command surfaces a server refusal as an error.
func TestDocumentCommandsSurfaceAServerRefusal(t *testing.T) {
	refusal := failure{500, `{"error":"down"}`}
	fs := newFakeServer(t, map[string]interface{}{"/v1/get": refusal, "/v1/import-url": refusal, "/v1/set-ttl": refusal})
	for _, args := range [][]string{
		{"get", "blog", "post", "en"},
		{"import-url", "blog", "https://example.test/post.md", "en"},
		{"set-ttl", "blog", "post", "en"},
	} {
		if _, err := runCLI(t, fs.URL, args...); err == nil {
			t.Errorf("%s: a 500 was reported as success", args[0])
		}
	}
}
