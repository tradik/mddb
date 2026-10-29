package main

import (
	"os"

	"mddb/internal/embedding"
	"mddb/internal/envconf"
)

// VectorConfig holds the vector/embedding configuration settings.
type VectorConfig struct {
	Enabled    bool   `json:"enabled"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	APIURL     string `json:"apiUrl"`
	// Semantic says whether the vectors encode meaning (SRCH-014). It is
	// false only for the lexical provider, whose vectors encode shared
	// spelling: vector search then finds documents that use the query's
	// words and word fragments, never ones that merely mean the same.
	Semantic bool `json:"semantic"`
	// Source says where this configuration came from: "environment",
	// "stored" or "autodetected" (SRCH-001). A provider MDDB found on the
	// machine rather than one anybody chose should say so — otherwise the
	// panel shows a configuration nobody recognises writing.
	Source string `json:"source,omitempty"`
}

// vectorConfig describes the embedding provider for GET /config, or returns
// nil when there is none.
func (s *Server) vectorConfig() *VectorConfig {
	if provider := os.Getenv("MDDB_EMBEDDING_PROVIDER"); provider != "" {
		var apiURL, model string
		var dimensions int

		switch provider {
		case "openai":
			apiURL = envconf.String("MDDB_EMBEDDING_API_URL", "https://api.openai.com/v1")
			model = envconf.String("MDDB_EMBEDDING_MODEL", "text-embedding-3-small")
			dimensions = envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 1536)
		case "ollama":
			apiURL = envconf.String("MDDB_EMBEDDING_API_URL", "http://localhost:11434")
			model = envconf.String("MDDB_EMBEDDING_MODEL", "nomic-embed-text")
			dimensions = envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 768)
		case "voyage":
			apiURL = envconf.String("MDDB_EMBEDDING_API_URL", "https://api.voyageai.com/v1")
			model = envconf.String("MDDB_EMBEDDING_MODEL", "voyage-3")
			dimensions = envconf.Int("MDDB_EMBEDDING_DIMENSIONS", 1024)
		case embedding.ProviderLexical:
			// No URL: nothing is called. The model name is fixed, because it
			// names the hashing scheme rather than something downloaded.
			model = embedding.LexicalModel
			dimensions = embedding.LexicalDimensionsFromEnv()
		}

		return &VectorConfig{
			Enabled:    true,
			Provider:   provider,
			Model:      model,
			Dimensions: dimensions,
			APIURL:     apiURL,
			Semantic:   embedding.IsSemanticProvider(provider),
			Source:     "environment",
		}
	}
	if detected := s.DetectedEmbedding; detected != nil {
		// SRCH-001: nothing was configured and startup found a provider on
		// this machine. Reporting nothing here would leave the panel saying
		// vector search is off while the server is embedding documents.
		// Autodetection only ever finds a model, so the vectors encode
		// meaning.
		return &VectorConfig{
			Enabled:    true,
			Provider:   detected.Name,
			Model:      detected.Model,
			Dimensions: detected.Dimensions,
			APIURL:     detected.APIURL,
			Semantic:   true,
			Source:     "autodetected",
		}
	}
	return nil
}
