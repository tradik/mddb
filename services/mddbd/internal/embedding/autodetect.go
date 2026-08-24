// Package embedding — local provider discovery (SRCH-001).
//
// A fresh install with no API key and no configuration has no semantic search.
// SRCH-001 asks for an embedding model compiled into the binary; its spike
// concluded that means a full transformer inference stack in pure Go, which is
// a project rather than a task, and that a hash-based stand-in would pass the
// name "offline semantic search" while failing the ticket's own quality gate.
//
// This closes a different part of the same problem, and closes it completely.
// A great many of the people who have no semantic search have Ollama already
// running on the same machine: they installed it, and MDDB never looked. The
// gap was not the model. It was that nothing asked.
//
// So on startup, when nothing else is configured, MDDB asks localhost whether
// an embedding model is there. If one is, it is used; if not, nothing changes.
// This claims nothing it does not do — it is discovery of a provider that
// exists, not a model in the binary.

package embedding

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// DefaultOllamaURL is where Ollama listens unless told otherwise.
const DefaultOllamaURL = "http://localhost:11434"

// detectTimeout bounds the startup probe.
//
// Short on purpose: this runs before the server accepts traffic, and a machine
// with nothing on the port must not pay for asking. A refused connection
// answers in microseconds; this budget is for the case where something is
// listening but slow.
const detectTimeout = 2 * time.Second

// knownEmbeddingModels maps Ollama embedding models to their dimensionality.
//
// Ollama's /api/tags does not report embedding dimensions, and guessing wrong
// writes vectors that no later query can match. Only models whose dimensions
// are known are auto-selected; anything else is left for a human to configure,
// because a wrong number here is worse than no configuration at all.
//
// Order is preference order — better retrieval quality first.
var knownEmbeddingModels = []struct {
	prefix     string
	dimensions int
}{
	{"nomic-embed-text", 768},
	{"mxbai-embed-large", 1024},
	{"snowflake-arctic-embed", 1024},
	{"bge-m3", 1024},
	{"all-minilm", 384},
}

// DetectedProvider is a provider found on the local machine, with the details
// a log line and a stored configuration both need.
type DetectedProvider struct {
	Provider   Provider
	Name       string
	Model      string
	Dimensions int
	APIURL     string
}

// ollamaTagsResponse is the shape of Ollama's /api/tags.
type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// DetectLocalProvider looks for a usable embedding provider on this machine.
//
// Returns nil when there is nothing to find, which is the ordinary case and
// not an error. Set MDDB_EMBEDDING_AUTODETECT=0 to skip the probe entirely.
func DetectLocalProvider(ctx context.Context) *DetectedProvider {
	if os.Getenv("MDDB_EMBEDDING_AUTODETECT") == "0" {
		return nil
	}

	url := strings.TrimSuffix(envOr("OLLAMA_HOST", DefaultOllamaURL), "/")
	// OLLAMA_HOST is conventionally written without a scheme.
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}

	models, err := listOllamaModels(ctx, url)
	if err != nil || len(models) == 0 {
		return nil
	}

	model, dimensions, ok := pickEmbeddingModel(models)
	if !ok {
		return nil
	}

	return &DetectedProvider{
		Provider:   NewOllamaProvider(url, model, dimensions),
		Name:       "ollama",
		Model:      model,
		Dimensions: dimensions,
		APIURL:     url,
	}
}

// listOllamaModels asks a local Ollama what it has pulled.
func listOllamaModels(ctx context.Context, url string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, detectTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	// A dedicated client rather than the shared pooled one: that transport
	// carries the SSRF dialer, which refuses loopback — correct there, and the
	// exact opposite of what this probe is for.
	client := &http.Client{Timeout: detectTimeout}
	resp, err := client.Do(req) // #nosec G107 -- loopback or OLLAMA_HOST, never a request value
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/tags returned %d", resp.StatusCode)
	}

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// pickEmbeddingModel chooses the best known embedding model among those pulled.
//
// Preference order beats installation order: a machine with both all-minilm
// and nomic-embed-text should get the better one regardless of which was
// pulled first.
func pickEmbeddingModel(available []string) (string, int, bool) {
	for _, known := range knownEmbeddingModels {
		for _, name := range available {
			// Ollama reports "nomic-embed-text:latest"; the tag is part of the
			// name the API expects back, so the full name is kept.
			if strings.HasPrefix(name, known.prefix) {
				return name, known.dimensions, true
			}
		}
	}
	return "", 0, false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
