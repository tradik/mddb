package embedding

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// VoyageProvider generates embeddings using Voyage AI API (Anthropic).
type VoyageProvider struct {
	apiKey     string
	apiURL     string
	model      string
	dimensions int
	client     *http.Client
}

// NewVoyageProvider creates a new Voyage AI embedding provider.
func NewVoyageProvider(apiKey, apiURL, model string, dimensions int) *VoyageProvider {
	return &VoyageProvider{
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
func (p *VoyageProvider) Model() string { return p.model }

// Dimensions returns the embedding dimensionality.
func (p *VoyageProvider) Dimensions() int { return p.dimensions }

// Embed generates an embedding for a single text.
func (p *VoyageProvider) Embed(ctx context.Context, text string, role Role) ([]float32, error) {
	vectors, err := p.EmbedBatch(ctx, []string{text}, role)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("empty response from Voyage AI")
	}
	return vectors[0], nil
}

// EmbedBatch generates embeddings for multiple texts in one API call.
func (p *VoyageProvider) EmbedBatch(ctx context.Context, texts []string, role Role) ([][]float32, error) {
	reqBody := voyageEmbeddingRequest{
		Input:     texts,
		InputType: voyageInputType(role),
		Model:     p.model,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiURL+"/embeddings", bytes.NewReader(body)) // #nosec G704 -- URL from server config
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req) // #nosec G704 -- URL from server config
	if err != nil {
		return nil, fmt.Errorf("voyage AI API request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// SEC-013: bounded; see upstream_error.go.
		return nil, upstreamError("voyage", resp)
	}

	var result voyageEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	vectors := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		vectors[d.Index] = float64sToFloat32s(d.Embedding)
	}

	// Update dimensions from actual response.
	if len(vectors) > 0 && len(vectors[0]) > 0 {
		p.dimensions = len(vectors[0])
	}

	return vectors, nil
}

type voyageEmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`

	// Voyage's models are trained asymmetrically and the API takes the role as
	// a parameter. It was never sent, so queries and documents were embedded
	// identically (RAG-006).
	InputType string `json:"input_type,omitempty"`
}

// voyageInputType maps a role onto Voyage's input_type parameter.
func voyageInputType(role Role) string {
	if role == RoleQuery {
		return "query"
	}
	return "document"
}

type voyageEmbeddingResponse struct {
	Data  []voyageEmbeddingData `json:"data"`
	Model string                `json:"model"`
}

type voyageEmbeddingData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}
