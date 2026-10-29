package embedding

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// Voyage multimodal embeddings (RAG-010).
//
// Voyage embeds images with its voyage-multimodal models, at POST
// /v1/multimodalembeddings: each input is a list of content items, text or a
// base64 data URI (PNG, JPEG, WebP or GIF, at most 16 million pixels and
// 20 MB), and the answer is one vector per input. Its text models are a
// different space, so a text query is embedded by the multimodal model too
// before it is compared with images.
// https://docs.voyageai.com/reference/multimodal-embeddings-api

// DefaultVoyageImageModel is the multimodal model images are embedded with
// when nothing else is configured.
const DefaultVoyageImageModel = "voyage-multimodal-3.5"

// SetImageModel picks the model images are embedded with: "" for the default
// (the text model itself when it is multimodal), "none" to embed no images.
func (p *VoyageProvider) SetImageModel(model string) {
	switch {
	case model == "none":
		p.imageModel = ""
	case model != "":
		p.imageModel = model
	case strings.HasPrefix(p.model, "voyage-multimodal"):
		p.imageModel = p.model
	default:
		p.imageModel = DefaultVoyageImageModel
	}
}

// ImageModel names the multimodal model, "" when images are off.
func (p *VoyageProvider) ImageModel() string { return p.imageModel }

// EmbedImage embeds one image with the multimodal model.
func (p *VoyageProvider) EmbedImage(ctx context.Context, img Image, role Role) ([]float32, error) {
	return p.multimodal(ctx, voyageContent{Type: "image_base64", ImageBase64: img.DataURI()}, role)
}

// EmbedImageText embeds text with the multimodal model, into the space its
// images are in.
func (p *VoyageProvider) EmbedImageText(ctx context.Context, text string, role Role) ([]float32, error) {
	return p.multimodal(ctx, voyageContent{Type: "text", Text: text}, role)
}

// multimodal sends one input of one content item and returns its vector.
func (p *VoyageProvider) multimodal(ctx context.Context, item voyageContent, role Role) ([]float32, error) {
	if p.imageModel == "" {
		return nil, fmt.Errorf("voyage: no multimodal model configured")
	}
	// A struct of strings: marshalling it cannot fail.
	body, _ := json.Marshal(voyageMultimodalRequest{
		Inputs:    []voyageMultimodalInput{{Content: []voyageContent{item}}},
		Model:     p.imageModel,
		InputType: voyageInputType(role),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/multimodalembeddings", bytes.NewReader(body)) // #nosec G704 -- URL from server config
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
		return nil, upstreamError("voyage", resp)
	}
	var result voyageEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	entries := make([]indexedEmbedding, 0, len(result.Data))
	for _, d := range result.Data {
		entries = append(entries, indexedEmbedding{index: d.Index, vector: float64sToFloat32s(d.Embedding)})
	}
	vectors, err := placeByIndex("voyage", 1, entries)
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

type voyageMultimodalRequest struct {
	Inputs    []voyageMultimodalInput `json:"inputs"`
	Model     string                  `json:"model"`
	InputType string                  `json:"input_type,omitempty"`
}

type voyageMultimodalInput struct {
	Content []voyageContent `json:"content"`
}

// voyageContent is one content item: text, or an image as a data URI.
type voyageContent struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	ImageBase64 string `json:"image_base64,omitempty"`
}
