package mddb

// Types for /v1/collection-config, after the server's CollectionConfig and
// SetCollectionConfigRequest.

// CollectionConfig is a collection's stored settings, as GET returns them.
// Credentials come back blanked, with SecretKeySet/APIKeySet set instead.
type CollectionConfig struct {
	Type            string                 `json:"type"` // default|website|images|audio|documents
	Description     string                 `json:"description,omitempty"`
	Icon            string                 `json:"icon,omitempty"`
	Color           string                 `json:"color,omitempty"`
	CustomMeta      map[string]string      `json:"customMeta,omitempty"`
	StorageBackend  string                 `json:"storageBackend,omitempty"` // boltdb|memory|s3
	StorageConfig   *StorageConfig         `json:"storageConfig,omitempty"`
	Quantization    string                 `json:"quantization,omitempty"` // float32|int8|int4
	DiskOnlyVectors bool                   `json:"diskOnlyVectors,omitempty"`
	TrackAccess     bool                   `json:"trackAccess,omitempty"`
	TrackHot        bool                   `json:"trackHot,omitempty"`
	SpellCorrect    bool                   `json:"spellCorrect,omitempty"`
	SpellLang       string                 `json:"spellLang,omitempty"`
	MaxRevisions    int                    `json:"maxRevisions,omitempty"` // 0 = keep all
	Encrypted       bool                   `json:"encrypted,omitempty"`
	WordPress       *WordPressTarget       `json:"wordpress,omitempty"`
	Retrieval       *RetrievalProfile      `json:"retrieval,omitempty"`
	ResponsePrompt  string                 `json:"responsePrompt,omitempty"`
	Code            *CodeConfig            `json:"code,omitempty"`
	Embedding       *EmbeddingChunking     `json:"embedding,omitempty"`
	FTS             *CollectionFTSSettings `json:"fts,omitempty"`
}

// SetCollectionConfigRequest replaces a collection's settings (PUT). Fields
// left zero are stored as zero, so read, change and send back the whole set.
type SetCollectionConfigRequest struct {
	Collection      string                 `json:"collection"`
	Type            string                 `json:"type,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Icon            string                 `json:"icon,omitempty"`
	Color           string                 `json:"color,omitempty"`
	CustomMeta      map[string]string      `json:"customMeta,omitempty"`
	StorageBackend  string                 `json:"storageBackend,omitempty"`
	StorageConfig   *StorageConfig         `json:"storageConfig,omitempty"`
	Quantization    string                 `json:"quantization,omitempty"`
	DiskOnlyVectors bool                   `json:"diskOnlyVectors,omitempty"`
	MaxRevisions    int                    `json:"maxRevisions,omitempty"`
	Encrypted       bool                   `json:"encrypted,omitempty"`
	WordPress       *WordPressTarget       `json:"wordpress,omitempty"`
	Retrieval       *RetrievalProfile      `json:"retrieval,omitempty"`
	ResponsePrompt  string                 `json:"responsePrompt,omitempty"`
	Code            *CodeConfig            `json:"code,omitempty"`
	Embedding       *EmbeddingChunking     `json:"embedding,omitempty"`
	FTS             *CollectionFTSSettings `json:"fts,omitempty"`
	TrackAccess     bool                   `json:"trackAccess,omitempty"`
	TrackHot        bool                   `json:"trackHot,omitempty"`
	SpellCorrect    bool                   `json:"spellCorrect,omitempty"`
	SpellLang       string                 `json:"spellLang,omitempty"`
}

// CollectionConfigResponse is the GET /v1/collection-config response.
// Configured is false when the collection has no stored settings and Config
// holds the defaults.
type CollectionConfigResponse struct {
	Collection string           `json:"collection"`
	Config     CollectionConfig `json:"config"`
	Configured bool             `json:"configured"`
}

// StorageConfig holds settings for a non-default storage backend (s3).
type StorageConfig struct {
	Endpoint     string `json:"endpoint,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	Region       string `json:"region,omitempty"`
	AccessKey    string `json:"accessKey,omitempty"`
	SecretKey    string `json:"secretKey,omitempty"`
	Prefix       string `json:"prefix,omitempty"`
	UseTLS       bool   `json:"useTLS,omitempty"`
	SecretKeySet bool   `json:"secretKeySet,omitempty"` // output only
}

// WordPressTarget is the remote-publishing endpoint of a collection.
type WordPressTarget struct {
	URL       string `json:"url"`
	APIKey    string `json:"apiKey,omitempty"`
	APIKeySet bool   `json:"apiKeySet,omitempty"` // output only
}

// RetrievalProfile holds a collection's search defaults.
type RetrievalProfile struct {
	DefaultSearchType     string  `json:"defaultSearchType,omitempty"` // fts|vector|hybrid
	TopK                  int     `json:"topK,omitempty"`
	RetrievalMode         string  `json:"retrievalMode,omitempty"`  // parent|chunk|window
	HybridStrategy        string  `json:"hybridStrategy,omitempty"` // alpha|rrf
	HybridAlpha           float64 `json:"hybridAlpha,omitempty"`
	HybridAlphaSet        bool    `json:"hybridAlphaSet,omitempty"` // marks an explicit 0
	ContextTokenBudget    int     `json:"contextTokenBudget,omitempty"`
	Oversample            float64 `json:"oversample,omitempty"`
	Diversity             float64 `json:"diversity,omitempty"`
	DiversityThreshold    float64 `json:"diversityThreshold,omitempty"`
	Proximity             float64 `json:"proximity,omitempty"`
	Freshness             float64 `json:"freshness,omitempty"`
	FreshnessHalfLifeDays float64 `json:"freshnessHalfLifeDays,omitempty"`
}

// CodeConfig configures collections of source code. IncludePaths are the
// collection-relative directories angle-bracket includes resolve against.
type CodeConfig struct {
	IncludePaths []string `json:"includePaths,omitempty"`
}

// EmbeddingChunking overrides the server's embedding chunk size (characters)
// and mode ("code" or "prose").
type EmbeddingChunking struct {
	ChunkSize int    `json:"chunkSize,omitempty"`
	ChunkMode string `json:"chunkMode,omitempty"`
}

// CollectionFTSSettings turns full-text indexing of a collection on or off.
// A nil Enabled means on.
type CollectionFTSSettings struct {
	Enabled *bool `json:"enabled,omitempty"`
}
