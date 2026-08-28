package embedding

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	json "mddb/internal/jsonx"
)

// OpenAIProvider generates embeddings using OpenAI API.
type OpenAIProvider struct {
	apiKey     string
	apiURL     string
	model      string
	dimensions int
	client     *http.Client
}

// NewOpenAIProvider creates a new OpenAI embedding provider.
func NewOpenAIProvider(apiKey, apiURL, model string, dimensions int) *OpenAIProvider {
	return &OpenAIProvider{
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
func (p *OpenAIProvider) Model() string { return p.model }

// Dimensions returns the embedding dimensionality.
func (p *OpenAIProvider) Dimensions() int { return p.dimensions }

// Embed generates an embedding for a single text.
// Embed generates an embedding for one text.
//
// The role is ignored: OpenAI's text-embedding-3 models are symmetric, so a
// query and a document holding the same words produce the same vector. The
// parameter is present because the interface carries it, not because this
// provider has anything to do with it.
func (p *OpenAIProvider) Embed(ctx context.Context, text string, role Role) ([]float32, error) {
	vectors, err := p.EmbedBatch(ctx, []string{text}, role)
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("empty response from OpenAI")
	}
	return vectors[0], nil
}

// EmbedBatch generates embeddings for multiple texts in one API call.
func (p *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string, _ Role) ([][]float32, error) {
	reqBody := openAIEmbeddingRequest{
		Input:      texts,
		Model:      p.model,
		Dimensions: p.dimensions,
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
		return nil, fmt.Errorf("API request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// SEC-013: bounded; see upstream_error.go.
		return nil, upstreamError("openai", resp)
	}

	var result openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	vectors := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		vectors[d.Index] = float64sToFloat32s(d.Embedding)
	}

	return vectors, nil
}

type openAIEmbeddingRequest struct {
	Input      []string `json:"input"`
	Model      string   `json:"model"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type openAIEmbeddingResponse struct {
	Data []openAIEmbeddingData `json:"data"`
}

type openAIEmbeddingData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

func float64sToFloat32s(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
