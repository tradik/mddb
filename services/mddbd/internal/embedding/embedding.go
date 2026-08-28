package embedding

import (
	"context"
	"log/slog"
	"os"
	"time"

	"mddb/internal/envconf"
)

// Role says whether a text is being stored or being searched with.
//
// Retrieval embedding models are trained asymmetrically: the same sentence
// produces a different vector depending on whether it is a document in the
// corpus or the question being asked of it. Handing both to the model as the
// same kind of input flattens the space and costs ranking accuracy, which is
// what this type exists to prevent (RAG-006).
//
// It is a required argument rather than an option with a default, because a
// default is exactly how a call site stays silently wrong.
type Role int

const (
	// RoleDocument is text entering the corpus.
	RoleDocument Role = iota

	// RoleQuery is text being searched with.
	RoleQuery
)

func (r Role) String() string {
	if r == RoleQuery {
		return "query"
	}
	return "document"
}

// Provider generates embedding vectors from text.
type Provider interface {
	// Embed generates an embedding vector for a single text.
	//
	// role tells the provider whether this text is a document or a query;
	// providers whose models distinguish the two use it, and the rest ignore it.
	Embed(ctx context.Context, text string, role Role) ([]float32, error)

	// EmbedBatch generates embedding vectors for multiple texts, all in the
	// same role.
	EmbedBatch(ctx context.Context, texts []string, role Role) ([][]float32, error)

	// Model returns the model name used for embeddings.
	Model() string

	// Dimensions returns the dimensionality of the embedding vectors.
	Dimensions() int
}

// NewProvider creates an embedding provider based on configuration.
// Returns nil if embedding is disabled (provider = "none" or empty).
//
// The result is wrapped in an embedding cache unless
// MDDB_EMBEDDING_CACHE_SIZE=0 (RAG-003) — one place, so no call site has to
// know whether caching is on.
func NewProvider() Provider {
	return NewCachingProvider(
		newBareProvider(),
		envconf.Int("MDDB_EMBEDDING_CACHE_SIZE", DefaultCacheSize),
		time.Duration(envconf.Int("MDDB_EMBEDDING_CACHE_TTL", int(DefaultCacheTTL.Seconds())))*time.Second,
	)
}

// newBareProvider builds the configured provider without any decoration.
func newBareProvider() Provider {
	provider := os.Getenv("MDDB_EMBEDDING_PROVIDER")
	if provider == "" || provider == "none" {
		return nil
	}

	switch provider {
	case "openai":
		apiKey := os.Getenv("MDDB_EMBEDDING_API_KEY")
		if apiKey == "" {
			slog.Warn("MDDB_EMBEDDING_PROVIDER=openai but MDDB_EMBEDDING_API_KEY not set")
			return nil
		}
		apiURL := envconf.String("MDDB_EMBEDDING_API_URL", "https://api.openai.com/v1")
		model := envconf.String("MDDB_EMBEDDING_MODEL", "text-embedding-3-small")
		dims := envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 1536)
		return NewOpenAIProvider(apiKey, apiURL, model, dims)

	case "ollama":
		apiURL := envconf.String("MDDB_EMBEDDING_API_URL", "http://localhost:11434")
		model := envconf.String("MDDB_EMBEDDING_MODEL", "nomic-embed-text")
		dims := envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 768)
		return NewOllamaProvider(apiURL, model, dims)

	case "voyage":
		apiKey := os.Getenv("MDDB_EMBEDDING_API_KEY")
		if apiKey == "" {
			slog.Warn("MDDB_EMBEDDING_PROVIDER=voyage but MDDB_EMBEDDING_API_KEY not set")
			return nil
		}
		apiURL := envconf.String("MDDB_EMBEDDING_API_URL", "https://api.voyageai.com/v1")
		model := envconf.String("MDDB_EMBEDDING_MODEL", "voyage-3")
		dims := envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 1024)
		return NewVoyageProvider(apiKey, apiURL, model, dims)

	case "cohere":
		apiKey := os.Getenv("MDDB_EMBEDDING_API_KEY")
		if apiKey == "" {
			slog.Warn("MDDB_EMBEDDING_PROVIDER=cohere but MDDB_EMBEDDING_API_KEY not set")
			return nil
		}
		apiURL := envconf.String("MDDB_EMBEDDING_API_URL", "https://api.cohere.ai/v1")
		model := envconf.String("MDDB_EMBEDDING_MODEL", "embed-english-v3.0")
		dims := envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 1024)
		return NewCohereProvider(apiKey, apiURL, model, dims)

	default:
		slog.Warn("unknown MDDB_EMBEDDING_PROVIDER, embedding disabled", "provider", provider) // #nosec G706 -- internal log
		return nil
	}
}
