package embedding

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// SEC-013: a provider's apiUrl deliberately bypasses the SSRF guard, which
// makes it a blind request primitive. Echoing the target's response body back
// through the error is what removed the "blind".

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestUpstreamErrorKeepsAShortServiceMessage(t *testing.T) {
	// The message an operator actually needs.
	err := upstreamError("ollama", response(404, `{"error":"model \"nomic\" not found, try pulling it first"}`))

	if !strings.Contains(err.Error(), "not found, try pulling it first") {
		t.Errorf("the provider's own error was lost: %v", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("the status code was lost: %v", err)
	}
}

func TestUpstreamErrorBoundsALargeResponse(t *testing.T) {
	// What an apiUrl pointed at something that is not an embedding service
	// answers with.
	secret := strings.Repeat("A", 100_000)
	err := upstreamError("ollama", response(403, secret))

	if len(err.Error()) > maxUpstreamErrorBytes+200 {
		t.Fatalf("error carried %d bytes of the target's response", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("truncation was silent: %v", err)
	}
}

func TestUpstreamErrorOnAnEmptyBody(t *testing.T) {
	err := upstreamError("voyage", response(502, ""))

	if !strings.Contains(err.Error(), "502") {
		t.Errorf("the status code was lost: %v", err)
	}
	// No dangling separator for a body that is not there.
	if strings.HasSuffix(err.Error(), ": ") {
		t.Errorf("empty body left a trailing separator: %q", err.Error())
	}
}

func TestUpstreamErrorAtTheLimit(t *testing.T) {
	exact := strings.Repeat("B", maxUpstreamErrorBytes)
	err := upstreamError("cohere", response(400, exact))

	if strings.Contains(err.Error(), "truncated") {
		t.Errorf("a body at exactly the limit was reported as truncated")
	}
	if !strings.Contains(err.Error(), exact) {
		t.Error("a body at exactly the limit was cut")
	}
}
