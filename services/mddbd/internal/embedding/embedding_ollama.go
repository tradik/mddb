package embedding

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// OllamaProvider generates embeddings using local Ollama server.
type OllamaProvider struct {
	apiURL     string
	model      string
	dimensions int
	client     *http.Client
}

// NewOllamaProvider creates a new Ollama embedding provider.
func NewOllamaProvider(apiURL, model string, dimensions int) *OllamaProvider {
	return &OllamaProvider{
		apiURL:     apiURL,
		model:      model,
		dimensions: dimensions,
		client: &http.Client{
			Timeout: 60 * time.Second, // Ollama may be slower on first call
		},
	}
}

// Model returns the model name used by this provider.
func (p *OllamaProvider) Model() string { return p.model }

// Dimensions returns the embedding dimensionality.
func (p *OllamaProvider) Dimensions() int { return p.dimensions }

// Embed generates an embedding for a single text.
// taskPrefixes maps an Ollama model to the prefixes it was trained to expect
// for each role (RAG-006).
//
// Only models whose prefixes have been measured are listed. A model that is not
// here is embedded as plain text, exactly as before: an invented prefix is
// worse than none, because it moves every vector without improving the ranking.
//
// Measured for nomic-embed-text on a six-document corpus, query "my API key
// stopped working", where the correct answer sat third without prefixes and
// first with them. The others in autodetect.go's preference list document
// prefixes too, and belong here once someone has run the same measurement
// rather than trusted the model card.
var taskPrefixes = map[string]struct{ document, query string }{
	"nomic-embed-text": {document: "search_document: ", query: "search_query: "},
}

// applyTaskPrefix returns text with the prefix this model expects for role.
//
// Matching is on the name before any tag, so nomic-embed-text:latest and
// nomic-embed-text:v1.5 both resolve.
func applyTaskPrefix(model, text string, role Role) string {
	name := model
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	p, ok := taskPrefixes[name]
	if !ok {
		return text
	}
	if role == RoleQuery {
		return p.query + text
	}
	return p.document + text
}

func (p *OllamaProvider) Embed(ctx context.Context, text string, role Role) ([]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model: p.model,
		Input: applyTaskPrefix(p.model, text, role),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiURL+"/api/embed", bytes.NewReader(body)) // #nosec G704 -- URL from server config
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req) // #nosec G704 -- URL from server config
	if err != nil {
		return nil, fmt.Errorf("ollama API request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// SEC-013: bounded; see upstream_error.go.
		return nil, upstreamError("ollama", resp)
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("empty embedding response from Ollama")
	}

	// Update dimensions from actual response
	if len(result.Embeddings[0]) > 0 {
		p.dimensions = len(result.Embeddings[0])
	}

	return float64sToFloat32s(result.Embeddings[0]), nil
}

// EmbedBatch generates embeddings for multiple texts.
func (p *OllamaProvider) EmbedBatch(ctx context.Context, texts []string, role Role) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		v, err := p.Embed(ctx, text, role)
		if err != nil {
			return nil, fmt.Errorf("embed text %d: %w", i, err)
		}
		vectors[i] = v
	}
	return vectors, nil
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}
