package embedding

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// CohereProvider generates embeddings using Cohere API
type CohereProvider struct {
	apiKey     string
	apiURL     string
	model      string
	dimensions int
	client     *http.Client
}

// NewCohereProvider creates a new Cohere embedding provider
func NewCohereProvider(apiKey, apiURL, model string, dimensions int) *CohereProvider {
	if apiURL == "" {
		apiURL = "https://api.cohere.ai/v1"
	}
	return &CohereProvider{
		apiKey:     apiKey,
		apiURL:     apiURL,
		model:      model,
		dimensions: dimensions,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Model returns the model name used by this provider.
func (p *CohereProvider) Model() string { return p.model }

// Dimensions returns the embedding dimensionality.
func (p *CohereProvider) Dimensions() int { return p.dimensions }

// Embed generates an embedding for a single text
func (p *CohereProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("empty response from Cohere")
	}
	return vectors[0], nil
}

// EmbedBatch generates embeddings for multiple texts in one API call
func (p *CohereProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := cohereEmbedRequest{
		Texts:     texts,
		Model:     p.model,
		InputType: "search_document",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiURL+"/embed", bytes.NewReader(body)) // #nosec G704 -- URL from server config
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req) // #nosec G704 -- URL from server config
	if err != nil {
		return nil, fmt.Errorf("cohere API request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// SEC-013: bounded; see upstream_error.go.
		return nil, upstreamError("cohere", resp)
	}

	var result cohereEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("unexpected number of embeddings: got %d, expected %d", len(result.Embeddings), len(texts))
	}

	// Copy embeddings to result
	vectors := make([][]float32, len(result.Embeddings))
	copy(vectors, result.Embeddings)

	// Update dimensions from actual response
	if len(vectors) > 0 && len(vectors[0]) > 0 {
		p.dimensions = len(vectors[0])
	}

	return vectors, nil
}

type cohereEmbedRequest struct {
	Texts     []string `json:"texts"`
	Model     string   `json:"model"`
	InputType string   `json:"input_type"`
}

type cohereEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}
