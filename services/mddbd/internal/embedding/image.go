package embedding

import (
	"context"
	"encoding/base64"
	"errors"
)

// Image embedding (RAG-010).
//
// A provider whose API takes images embeds them into the same space as text
// it embeds with the same model, so a text query finds a picture with no
// words on it. Providers without image input do not implement ImageEmbedder;
// the server then stores images and finds them by their alt text and
// captions only.
//
// It is an optional interface, like DocumentVariant and SemanticReporter:
// callers ask ImageEmbedderOf, never assert the type themselves, because a
// wrapper (the embedding cache) implements every method whatever it wraps and
// says "no images" by an empty ImageModel.

// ErrNoImageEmbedding is returned by a wrapper asked to embed an image when
// the provider it wraps cannot.
var ErrNoImageEmbedding = errors.New("the embedding provider does not embed images")

// Image is an image to embed: its bytes and their MIME type (image/jpeg,
// image/png, image/gif or image/webp).
type Image struct {
	Data     []byte
	MimeType string
}

// DataURI is the image as a data URI, the form the multimodal APIs take.
func (im Image) DataURI() string {
	return "data:" + im.MimeType + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
}

// ImageEmbedder is implemented by a provider that embeds images.
type ImageEmbedder interface {
	// EmbedImage embeds one image. role is RoleDocument for an image being
	// stored and RoleQuery for an image being searched with.
	EmbedImage(ctx context.Context, img Image, role Role) ([]float32, error)

	// EmbedImageText embeds text into the image space: the vector a text
	// query is compared with image vectors by. For a model that embeds text
	// and images alike it is Embed; for a separate multimodal model it is
	// that model's text input.
	EmbedImageText(ctx context.Context, text string, role Role) ([]float32, error)

	// ImageModel names the model images are embedded with; "" when this
	// provider, as configured, cannot embed images.
	ImageModel() string
}

// ImageEmbedderOf returns p as an ImageEmbedder when it can embed images.
func ImageEmbedderOf(p Provider) (ImageEmbedder, bool) {
	ie, ok := p.(ImageEmbedder)
	if !ok || ie.ImageModel() == "" {
		return nil, false
	}
	return ie, true
}
