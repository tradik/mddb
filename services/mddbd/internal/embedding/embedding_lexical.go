package embedding

import (
	"context"
	"log/slog"
	"math"

	"mddb/internal/envconf"
)

// The lexical provider (SRCH-014): vectors from the words of a text, with no
// model, no network and no download.
//
// It is named for what it measures. Two texts land close together when they
// share words and word fragments — spelling, not meaning. "car" and
// "automobile" are as far apart here as any two unrelated words. Two earlier
// decisions rejected a hashing embedder offered as a stand-in for a model
// (SRCH-001); this one is offered as nothing of the kind, and IsSemantic
// reports false for it so /config and the search advisor say so too.
//
// What it is for is a working vector pipeline where no model can run: CI,
// air-gapped installs, demos, a laptop without Ollama. Vector, hybrid and
// cross-collection search, graph retrieval, MMR, quantization and HNSW all
// need vectors to exist before they do anything at all. The same text gives
// the same vector on every machine, so tests and replicas agree bit for bit.
//
// The method is feature hashing, as in scikit-learn's HashingVectorizer:
//   - every word, and every character n-gram of length 3 to 5 of the word
//     wrapped in boundary markers ("<deploy>" gives "<de", "dep", ...), is a
//     feature — the n-grams are what tolerate a typo or a plural;
//   - a feature's position is a hash of it modulo the dimension count, and
//     its sign is a second, independent hash, so collisions cancel out on
//     average instead of piling up;
//   - its weight is sublinear in its count (1 + ln tf), so a word repeated
//     forty times does not drown the rest;
//   - the vector is L2-normalised, so cosine similarity is a dot product.

// ProviderLexical is the provider name that selects the lexical embedder.
const ProviderLexical = "lexical"

// LexicalModel is the model name the lexical provider reports. The version
// suffix changes whenever the features or the hashing change, because vectors
// from two versions are not comparable and a model change is what tells the
// index to re-embed.
const LexicalModel = "lexical-ngram-v1"

// Dimension bounds for the lexical provider. Below 256 collisions blur
// unrelated words together; above 2048 the vectors grow with nothing gained
// for typical documents.
const (
	LexicalDefaultDimensions = 512
	LexicalMinDimensions     = 256
	LexicalMaxDimensions     = 2048
)

// LexicalProvider embeds text by hashing its words and character n-grams.
// It is safe for concurrent use: it holds no state beyond its dimension count.
type LexicalProvider struct {
	dims int
}

// NewLexicalProvider returns a lexical provider with dims dimensions, clamped
// to [LexicalMinDimensions, LexicalMaxDimensions]. A value of zero or less
// means the default.
func NewLexicalProvider(dims int) *LexicalProvider {
	return &LexicalProvider{dims: clampLexicalDimensions(dims)}
}

// LexicalDimensionsFromEnv reads MDDB_EMBEDDING_DIMENSIONS for the lexical
// provider — default 512, clamped to the allowed range — so the provider and
// /config report the same number.
func LexicalDimensionsFromEnv() int {
	return clampLexicalDimensions(envconf.Int("MDDB_EMBEDDING_DIMENSIONS", LexicalDefaultDimensions))
}

// clampLexicalDimensions keeps dims within bounds, saying so when it moves it.
func clampLexicalDimensions(dims int) int {
	switch {
	case dims <= 0:
		return LexicalDefaultDimensions
	case dims < LexicalMinDimensions:
		slog.Warn("lexical embedding dimensions below minimum, using minimum", "requested", dims, "used", LexicalMinDimensions)
		return LexicalMinDimensions
	case dims > LexicalMaxDimensions:
		slog.Warn("lexical embedding dimensions above maximum, using maximum", "requested", dims, "used", LexicalMaxDimensions)
		return LexicalMaxDimensions
	}
	return dims
}

// Model returns LexicalModel.
func (p *LexicalProvider) Model() string { return LexicalModel }

// Dimensions returns the vector length.
func (p *LexicalProvider) Dimensions() int { return p.dims }

// Semantic reports false: the vectors measure shared spelling, not meaning.
func (p *LexicalProvider) Semantic() bool { return false }

// Embed returns the hashed feature vector of text. role is ignored — a
// document and a query are hashed the same way. A text with no letters or
// digits gives a zero vector, which matches nothing.
func (p *LexicalProvider) Embed(ctx context.Context, text string, _ Role) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.vector(text), nil
}

// EmbedBatch embeds each text in turn.
func (p *LexicalProvider) EmbedBatch(ctx context.Context, texts []string, _ Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out[i] = p.vector(t)
	}
	return out, nil
}

// vector builds the normalised feature vector of text.
func (p *LexicalProvider) vector(text string) []float32 {
	vec := make([]float32, p.dims)
	for _, f := range lexicalFeatures(text) {
		vec[f.key%uint64(p.dims)] += f.sign * float32(1+math.Log(float64(f.count))) // #nosec G115 -- dims is clamped to 256..2048, never negative
	}
	normalizeL2(vec)
	return vec
}

// normalizeL2 scales vec to unit length in place; a zero vector stays zero.
func normalizeL2(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range vec {
		vec[i] *= inv
	}
}
