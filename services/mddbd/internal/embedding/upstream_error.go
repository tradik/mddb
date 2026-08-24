package embedding

import (
	"fmt"
	"io"
	"net/http"
)

// SEC-013: an embedding provider dials a URL an administrator supplied, and
// that URL deliberately bypasses the SSRF guard so a local Ollama on
// 127.0.0.1:11434 keeps working. The bypass is the feature.
//
// What was not the feature: every provider read the failing response in full
// and put it in the returned error. A URL pointed at something that is not an
// embedding service — a metadata endpoint, an internal admin panel — answers
// with its own body, and that body travelled back to the caller. The primitive
// is meant to be blind; echoing the response is what makes it not.
//
// The body is still worth something for diagnosis: "model not found" from a
// real Ollama is the message an operator needs. So it is bounded rather than
// dropped — enough to carry a service's own error string, not enough to
// exfiltrate a page.

// maxUpstreamErrorBytes bounds what a failing upstream can say back through us.
//
// Chosen against the messages that matter: Ollama's "model \"x\" not found,
// try pulling it first" and the JSON error envelopes OpenAI, Cohere and Voyage
// return all fit comfortably. A configuration page or a credentials document
// does not.
const maxUpstreamErrorBytes = 512

// upstreamError builds the error for a non-2xx response from an embedding
// provider, reading at most maxUpstreamErrorBytes of the body.
func upstreamError(provider string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamErrorBytes+1))

	truncated := ""
	if len(body) > maxUpstreamErrorBytes {
		body = body[:maxUpstreamErrorBytes]
		truncated = " (truncated)"
	}

	if len(body) == 0 {
		return fmt.Errorf("%s API error (status %d)", provider, resp.StatusCode)
	}
	return fmt.Errorf("%s API error (status %d): %s%s", provider, resp.StatusCode, body, truncated)
}
