package main

import (
	"errors"
	"io"
	"mddb/internal/binlog"
	"mddb/internal/encryption"
	"net/http"
	"sync"

	json "mddb/internal/jsonx"
	bolt "go.etcd.io/bbolt"
)

var bucketColMeta = []byte("colmeta")

// CollectionConfig stores per-collection attributes: type, description, icon, color, custom metadata, and storage backend.
type CollectionConfig struct {
	Type           string            `json:"type"` // "default","website","images","audio","documents"
	Description    string            `json:"description,omitempty"`
	Icon           string            `json:"icon,omitempty"`
	Color          string            `json:"color,omitempty"`
	CustomMeta     map[string]string `json:"customMeta,omitempty"`
	StorageBackend string            `json:"storageBackend,omitempty"` // "boltdb" (default), "memory", "s3"
	StorageConfig  *StorageConfigDef `json:"storageConfig,omitempty"`  // backend-specific settings (required for s3)
	Quantization   string            `json:"quantization,omitempty"`   // "float32" (default), "int8", "int4"
	// DiskOnlyVectors keeps only quantized vectors in RAM: full-precision
	// vectors live exclusively in the BoltDB vectors bucket and are read back
	// for exact rescoring of the quantized candidate set. Requires
	// Quantization to be set; ignored otherwise.
	DiskOnlyVectors bool `json:"diskOnlyVectors,omitempty"`
	// Temporal tracking
	TrackAccess bool `json:"trackAccess,omitempty"` // record per-read access events
	TrackHot    bool `json:"trackHot,omitempty"`    // maintain hot-docs leaderboard
	// Spell correction
	SpellCorrect bool   `json:"spellCorrect,omitempty"` // auto-correct FTS queries using spell checker
	SpellLang    string `json:"spellLang,omitempty"`    // override language for spell correction
	// Revision retention. 0 (default) = keep all; N > 0 = keep last N per document; every add/update trims older revs in the same transaction.
	MaxRevisions int `json:"maxRevisions,omitempty"`
	// At-rest encryption (ISO 27001 A.8.24 / SOC 2 CC6.7). When true and
	// MDDB_ENCRYPTION_KEY is configured, documents and revisions in this
	// collection are AES-256-GCM encrypted before being written to the
	// BoltDB docs/rev buckets. FTS and vector indexes remain plaintext
	// because they are queryable structures — see docs/config.md for the
	// full threat model.
	Encrypted bool `json:"encrypted,omitempty"`
	// WordPress remote-publishing target used by the wordpress_publish /
	// wordpress_set_status MCP tools. The URL points at a site running the
	// mddb-sync plugin with "Remote publishing" enabled; APIKey is that
	// plugin's publish key (sent as Authorization: Bearer).
	WordPress *WordPressTargetConfig `json:"wordpress,omitempty"`
	// Retrieval settings for this collection (RAG-001). nil = today's
	// behaviour: every search path keeps its own historical default. See
	// retrieval_profile.go for the precedence rule.
	Retrieval *RetrievalProfileDef `json:"retrieval,omitempty"`
	// ResponsePrompt is how answers drawn from this collection should be
	// formatted (RAG-002) — numbered steps for runbooks, code blocks for API
	// docs. Applied automatically by mddb-chat and returned to MCP agents, so
	// the instruction travels with the data instead of living in every client.
	// Plain text for a model, never rendered as markup. Max 4 KiB.
	ResponsePrompt string `json:"responsePrompt,omitempty"`
}

// WordPressTargetConfig holds the outbound publishing endpoint for a collection.
type WordPressTargetConfig struct {
	URL    string `json:"url"`              // site base URL, e.g. https://blog.example.com
	APIKey string `json:"apiKey,omitempty"` // mddb-sync publish key
}

// StorageConfigDef holds backend-specific configuration for non-default storage backends.
type StorageConfigDef struct {
	// S3 / MinIO settings
	Endpoint  string `json:"endpoint,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	SecretKey string `json:"secretKey,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	UseTLS    bool   `json:"useTLS,omitempty"`
}

// CollectionManager manages per-collection configuration in a dedicated BoltDB bucket.
type CollectionManager struct {
	db        *bolt.DB
	mu        sync.RWMutex
	cache     map[string]*CollectionConfig
	binlog    *binlog.Binlog
	encryptor *encryption.Encryptor // optional; when set, cache changes are mirrored into it
}

// NewCollectionManager creates a new collection config manager.
func NewCollectionManager(db *bolt.DB) *CollectionManager {
	return &CollectionManager{
		db:    db,
		cache: make(map[string]*CollectionConfig),
	}
}

// SetBinlog sets the binlog for replication logging.
func (cm *CollectionManager) SetBinlog(bl *binlog.Binlog) {
	cm.binlog = bl
}

// SetEncryptor wires the at-rest encryptor so cache updates can keep
// the encryptor's per-collection flag in sync with CollectionConfig.
func (cm *CollectionManager) SetEncryptor(e *encryption.Encryptor) {
	cm.mu.Lock()
	cm.encryptor = e
	// Mirror existing cache into the encryptor on first wire-up.
	for name, cfg := range cm.cache {
		e.SetCollectionEnabled(name, cfg != nil && cfg.Encrypted)
	}
	cm.mu.Unlock()
}

// EnsureBucket creates the colmeta bucket if it doesn't exist.
func (cm *CollectionManager) EnsureBucket() error {
	return cm.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketColMeta)
		return err
	})
}

// LoadAll loads all collection configs from BoltDB into the in-memory cache.
func (cm *CollectionManager) LoadAll() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketColMeta)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var cfg CollectionConfig
			if err := json.Unmarshal(v, &cfg); err != nil {
				return nil // skip corrupt entries
			}
			cm.cache[string(k)] = &cfg
			return nil
		})
	})
}

// Get returns the config for a collection. ok is false if unconfigured.
func (cm *CollectionManager) Get(collection string) (*CollectionConfig, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cfg, ok := cm.cache[collection]
	return cfg, ok
}

// Set stores or updates the config for a collection.
func (cm *CollectionManager) Set(collection string, cfg *CollectionConfig) error {
	if cfg.Type == "" {
		cfg.Type = "default"
	}

	val, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	key := []byte(collection)

	if err := cm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketColMeta)
		return b.Put(key, val)
	}); err != nil {
		return err
	}

	if cm.binlog != nil {
		_ = cm.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "colmeta", Key: CopyBytes(key), Value: CopyBytes(val)})
	}

	cm.mu.Lock()
	cm.cache[collection] = cfg
	if cm.encryptor != nil {
		cm.encryptor.SetCollectionEnabled(collection, cfg.Encrypted)
	}
	cm.mu.Unlock()
	return nil
}

// Delete removes the config for a collection.
func (cm *CollectionManager) Delete(collection string) error {
	key := []byte(collection)
	if err := cm.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketColMeta)
		return b.Delete(key)
	}); err != nil {
		return err
	}

	if cm.binlog != nil {
		_ = cm.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "colmeta", Key: CopyBytes(key)})
	}

	cm.mu.Lock()
	delete(cm.cache, collection)
	if cm.encryptor != nil {
		cm.encryptor.SetCollectionEnabled(collection, false)
	}
	cm.mu.Unlock()
	return nil
}

// ListAll returns all configured collections.
func (cm *CollectionManager) ListAll() map[string]*CollectionConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	result := make(map[string]*CollectionConfig, len(cm.cache))
	for k, v := range cm.cache {
		result[k] = v
	}
	return result
}

// --- HTTP Handlers ---

// SetCollectionConfigRequest is the request body for PUT /v1/collection-config.
type SetCollectionConfigRequest struct {
	Collection      string                 `json:"collection"`
	Type            string                 `json:"type,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Icon            string                 `json:"icon,omitempty"`
	Color           string                 `json:"color,omitempty"`
	CustomMeta      map[string]string      `json:"customMeta,omitempty"`
	StorageBackend  string                 `json:"storageBackend,omitempty"` // "boltdb", "memory", "s3"
	StorageConfig   *StorageConfigDef      `json:"storageConfig,omitempty"`
	Quantization    string                 `json:"quantization,omitempty"`    // "float32" (default), "int8", "int4"
	DiskOnlyVectors bool                   `json:"diskOnlyVectors,omitempty"` // RAM holds only quantized vectors; full vectors stay on disk
	MaxRevisions    int                    `json:"maxRevisions,omitempty"`    // keep last N revisions per doc (0 = unlimited)
	Encrypted       bool                   `json:"encrypted,omitempty"`       // opt collection into AES-256-GCM at-rest encryption
	WordPress       *WordPressTargetConfig `json:"wordpress,omitempty"`       // outbound publishing target (mddb-sync plugin)
	Retrieval       *RetrievalProfileDef   `json:"retrieval,omitempty"`       // per-collection retrieval defaults (RAG-001)
	ResponsePrompt  string                 `json:"responsePrompt,omitempty"`  // how to format answers from this collection (RAG-002)
	// Temporal tracking and spell correction. These live in CollectionConfig
	// and are read on every request, but were missing here — so the panel's
	// toggles sent values the handler ignored, and every config save silently
	// cleared whatever had been set by other means.
	TrackAccess  bool   `json:"trackAccess,omitempty"`
	TrackHot     bool   `json:"trackHot,omitempty"`
	SpellCorrect bool   `json:"spellCorrect,omitempty"`
	SpellLang    string `json:"spellLang,omitempty"`
}

func (s *Server) handleCollectionConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleCollectionConfigGet(w, r)
	case http.MethodPut:
		s.handleCollectionConfigSet(w, r)
	case http.MethodDelete:
		s.handleCollectionConfigDelete(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCollectionConfigGet(w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Query().Get("collection")
	if collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}

	// Check read permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("collection_config_get", collection)
	}

	cfg, found := s.CollectionManager.Get(collection)
	if !found {
		cfg = &CollectionConfig{Type: "default"}
	}
	ok(w, map[string]interface{}{
		"collection": collection,
		"config":     cfg,
		"configured": found,
	})
}

func (s *Server) handleCollectionConfigSet(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}

	var req SetCollectionConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}

	// Check write permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("collection_config_set", req.Collection)
	}

	// Validate storage backend
	sb := req.StorageBackend
	if sb != "" && sb != "boltdb" && sb != "memory" && sb != "s3" {
		bad(w, errors.New("invalid storageBackend: must be boltdb, memory, or s3"))
		return
	}
	if sb == "s3" && (req.StorageConfig == nil || req.StorageConfig.Endpoint == "" || req.StorageConfig.Bucket == "") {
		bad(w, errors.New("s3 storageBackend requires storageConfig with endpoint and bucket"))
		return
	}

	// Validate quantization
	qt := req.Quantization
	if qt != "" && qt != "float32" && qt != "int8" && qt != "int4" {
		bad(w, errors.New("invalid quantization: must be float32, int8, or int4"))
		return
	}

	if req.MaxRevisions < 0 {
		bad(w, errors.New("invalid maxRevisions: must be >= 0 (0 = unlimited)"))
		return
	}

	if req.DiskOnlyVectors && (qt == "" || qt == "float32") {
		bad(w, errors.New("diskOnlyVectors requires quantization (int8 or int4)"))
		return
	}

	if err := validateWordPressTarget(req.WordPress); err != nil {
		bad(w, err)
		return
	}

	// RAG-001: a profile is stored data, so a value that cannot mean anything
	// is refused here rather than surfacing as a strange result months later.
	if err := req.Retrieval.Validate(); err != nil {
		bad(w, err)
		return
	}

	// RAG-002: this text is prepended to prompts automatically, so an
	// unbounded value would silently eat the context the answer needs.
	if err := ValidateResponsePrompt(req.ResponsePrompt); err != nil {
		bad(w, err)
		return
	}

	cfg := &CollectionConfig{
		Type:            req.Type,
		Description:     req.Description,
		Icon:            req.Icon,
		Color:           req.Color,
		CustomMeta:      req.CustomMeta,
		StorageBackend:  sb,
		StorageConfig:   req.StorageConfig,
		Quantization:    qt,
		DiskOnlyVectors: req.DiskOnlyVectors,
		MaxRevisions:    req.MaxRevisions,
		Encrypted:       req.Encrypted,
		WordPress:       req.WordPress,
		Retrieval:       req.Retrieval,
		TrackAccess:     req.TrackAccess,
		TrackHot:        req.TrackHot,
		SpellCorrect:    req.SpellCorrect,
		SpellLang:       req.SpellLang,
		ResponsePrompt:  req.ResponsePrompt,
	}
	// GO-021: create or drop the storage backend this config asks for, before
	// storing it — a config that names a backend which cannot be reached
	// should be refused rather than accepted and silently ignored, which is
	// what this whole change exists to stop.
	if err := s.ApplyStorageBackend(req.Collection, cfg); err != nil {
		bad(w, err)
		return
	}

	if err := s.CollectionManager.Set(req.Collection, cfg); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "ok", "collection": req.Collection})
}

func (s *Server) handleCollectionConfigDelete(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}

	collection := r.URL.Query().Get("collection")
	if collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}

	// Check write permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("collection_config_delete", collection)
	}

	if err := s.CollectionManager.Delete(collection); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "ok", "collection": collection})
}

func (s *Server) handleCollectionConfigList(w http.ResponseWriter, r *http.Request) {
	// Check read permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("collection_config_list", "")
	}

	_, _ = io.Copy(io.Discard, r.Body)
	configs := s.CollectionManager.ListAll()

	type configInfo struct {
		Collection string            `json:"collection"`
		Config     *CollectionConfig `json:"config"`
	}
	tenant := TenantFromContext(r.Context())
	var result []configInfo
	for col, cfg := range configs {
		if !CollectionInTenant(tenant, col) {
			continue
		}
		result = append(result, configInfo{Collection: col, Config: cfg})
	}
	if result == nil {
		result = []configInfo{}
	}
	ok(w, map[string]interface{}{
		"configs": result,
		"total":   len(result),
	})
}
