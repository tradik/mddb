package embedding

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	json "mddb/internal/jsonx"
)

// Cohere image embeddings (RAG-010).
//
// Cohere's Embed v3.0 and newer models embed images and text into one space:
// POST /v1/embed with input_type "image" and `images`, a list of data URIs
// (JPEG, PNG, WebP or GIF; v3 models take one image of at most 5 MB per
// call), answers one vector per image. A text query needs no second model:
// Embed already puts it in the images' space.
// https://docs.cohere.com/v1/reference/embed

// cohereVersionRe finds the model generation in a name: embed-english-v3.0,
// embed-v4.0.
var cohereVersionRe = regexp.MustCompile(`v(\d+)(?:\.\d+)?`)

// SetImageModel turns image embedding off with "none". Cohere embeds images
// with the text model itself, so no other value changes anything.
func (p *CohereProvider) SetImageModel(model string) {
	p.imagesOff = model == "none"
}

// ImageModel is the model itself when it embeds images (v3 and newer), else "".
func (p *CohereProvider) ImageModel() string {
	if p.imagesOff {
		return ""
	}
	m := cohereVersionRe.FindAllStringSubmatch(p.model, -1)
	if len(m) == 0 {
		return ""
	}
	// Digits only: Atoi fails on nothing but overflow, a version past any.
	if v, _ := strconv.Atoi(m[len(m)-1][1]); v < 3 {
		return ""
	}
	return p.model
}

// EmbedImage embeds one image. Cohere's input_type "image" has no query or
// document side, so role does not change the call.
func (p *CohereProvider) EmbedImage(ctx context.Context, img Image, _ Role) ([]float32, error) {
	// A struct of strings: marshalling it cannot fail.
	body, _ := json.Marshal(cohereImageRequest{Images: []string{img.DataURI()}, Model: p.model, InputType: "image"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/embed", bytes.NewReader(body)) // #nosec G704 -- URL from server config
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
		return nil, upstreamError("cohere", resp)
	}
	var result cohereEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if err := checkVectors("cohere", result.Embeddings, 1); err != nil {
		return nil, err
	}
	return result.Embeddings[0], nil
}

// EmbedImageText is Embed: Cohere's text and images share one space.
func (p *CohereProvider) EmbedImageText(ctx context.Context, text string, role Role) ([]float32, error) {
	return p.Embed(ctx, text, role)
}

type cohereImageRequest struct {
	Images    []string `json:"images"`
	Model     string   `json:"model"`
	InputType string   `json:"input_type"`
}
