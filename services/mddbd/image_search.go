package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/storage"
	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// Searching images by their picture (RAG-010).
//
// With a provider that embeds images, an image document has a vector of its
// picture in the image index (image_index.go). Two searches reach it:
//   - a text query with includeImages: the query is embedded into the image
//     space and the nearest images come back as imageHits, beside the
//     results — never mixed into them, since the two spaces' scores do not
//     compare;
//   - queryImage on vector search: an image, as base64 or an http(s) URL
//     fetched through the SSRF guard, embedded and compared with the image
//     vectors; the results are the image documents, each naming its parent.
//
// Without such a provider, queryImage is refused with a 4xx that says so,
// and includeImages still attaches images to hits.

var (
	// errNoImageProvider refuses an image query no provider can embed.
	errNoImageProvider = errors.New("queryImage needs an embedding provider that embeds images (voyage, or cohere embed v3 and newer)")
	// errQueryImage wraps what is wrong with a query image.
	errQueryImage = errors.New("invalid queryImage")
)

// imageHit is an image document found by its picture.
type imageHit struct {
	doc   storage.Doc
	score float32
}

// loadImageHits loads the documents of image index results.
func (s *Server) loadImageHits(collection string, results []vec.VectorResult) []imageHit {
	hits := make([]imageHit, 0, len(results))
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		for _, r := range results {
			if doc, err := loadDoc(bDocs.Get(storage.DocKey(collection, r.DocID))); err == nil && doc != nil {
				hits = append(hits, imageHit{doc: *doc, score: r.Score})
			}
		}
		return nil
	})
	return hits
}

// imageHitsFor finds the images whose pictures match a text query, for a
// search with includeImages, and says in the plan what it did.
func (s *Server) imageHitsFor(ctx context.Context, collection, query string, topK int, threshold float64,
	allowed map[string]bool, filtered bool, plan *SearchPlan) []ImageRef {
	if plan == nil {
		plan = &SearchPlan{} // a path without a plan: the reasons go nowhere
	}
	n := s.imageIndex().size(collection)
	if query == "" || n == 0 {
		plan.reason("includeImages: images attached to hits by meta.parent; no image vectors to search")
		return nil
	}
	ie, ok := embedding.ImageEmbedderOf(s.Embedding)
	if !ok {
		plan.reason("includeImages: %d image vectors not searched: the embedding provider does not embed images", n)
		return nil
	}
	ectx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	q, err := ie.EmbedImageText(ectx, query, embedding.RoleQuery)
	if err != nil {
		plan.reason("includeImages: image search skipped: %s", err)
		return nil
	}
	hits := s.loadImageHits(collection, s.searchImages(collection, q, topK, threshold, allowed, filtered))
	plan.reason("includeImages: %d image vectors searched with the query embedded by %s; %d matched", n, ie.ImageModel(), len(hits))
	refs := make([]ImageRef, len(hits))
	for i, h := range hits {
		refs[i] = imageRefOf(collection, &h.doc)
		refs[i].Score = h.score
	}
	return refs
}

// searchByImage runs a queryImage search: the image documents nearest the
// query image, by their pictures. It also returns the model and the
// dimensions the query was embedded with.
func (s *Server) searchByImage(ctx context.Context, collection, queryImage string, topK int, threshold float64,
	allowed map[string]bool, filtered bool) ([]imageHit, string, int, error) {
	ie, ok := embedding.ImageEmbedderOf(s.Embedding)
	if !ok {
		return nil, "", 0, errNoImageProvider
	}
	data, err := queryImageBytes(ctx, queryImage)
	if err != nil {
		return nil, "", 0, fmt.Errorf("%w: %w", errQueryImage, err)
	}
	mime, _, _, err := validateImage(data)
	if err != nil {
		return nil, "", 0, fmt.Errorf("%w: %w", errQueryImage, err)
	}
	ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	q, err := ie.EmbedImage(ectx, embedding.Image{Data: data, MimeType: mime}, embedding.RoleQuery)
	if err != nil {
		return nil, "", 0, fmt.Errorf("%w: %w", errEmbedQuery, err)
	}
	return s.loadImageHits(collection, s.searchImages(collection, q, topK, threshold, allowed, filtered)), ie.ImageModel(), len(q), nil
}

// queryImageBytes reads a query image: an http(s) URL is fetched through the
// SSRF guard, anything else is a data URI or plain base64.
func queryImageBytes(ctx context.Context, q string) ([]byte, error) {
	lower := strings.ToLower(strings.TrimSpace(q))
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return fetchImageURL(ctx, strings.TrimSpace(q))
	case strings.HasPrefix(lower, "data:"):
		return decodeDataURI(strings.TrimSpace(q))
	}
	return decodeBase64Any(q)
}
