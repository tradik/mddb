package embedding

import (
	"context"
	"log/slog"
	"mddb/internal/envconf"
	"os"
)

// Provider generates embedding vectors from text.
type Provider interface {
	// Embed generates an embedding vector for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embedding vectors for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Model returns the model name used for embeddings.
	Model() string

	// Dimensions returns the dimensionality of the embedding vectors.
	Dimensions() int
}

// NewProvider creates an embedding provider based on configuration.
// Returns nil if embedding is disabled (provider = "none" or empty).
func NewProvider() Provider {
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
