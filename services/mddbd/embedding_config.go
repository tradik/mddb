package main

import (
	"encoding/json"
	"fmt"
	"mddb/internal/binlog"
	"mddb/internal/embedding"
	"mddb/internal/httpclient"
	"net/http"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// EmbeddingConfig represents an embedding model configuration
type EmbeddingConfig struct {
	ID         string `json:"id"`         // unique identifier
	Name       string `json:"name"`       // display name
	Provider   string `json:"provider"`   // "openai" or "ollama"
	Model      string `json:"model"`      // model name
	Dimensions int    `json:"dimensions"` // embedding dimensions
	APIKey     string `json:"apiKey"`     // API key (for OpenAI) // #nosec G117
	APIURL     string `json:"apiUrl"`     // API URL (for Ollama)
	IsDefault  bool   `json:"isDefault"`  // is this the default config
	CreatedAt  int64  `json:"createdAt"`  // creation timestamp
}

// SaveEmbeddingConfig saves an embedding configuration to the database.
//
// SEC-013: apiUrl is validated here rather than in each handler, because every
// write goes through this function and a rule enforced in two of three places
// is not a rule. Loopback passes without opt-in — a local Ollama is the reason
// these providers bypass the SSRF guard at all — while any other private or
// reserved address needs MDDB_OUTBOUND_ALLOW_PRIVATE or the host allowlist.
func (s *Server) SaveEmbeddingConfig(config *EmbeddingConfig) error {
	if err := httpclient.ValidateServiceURL(config.APIURL); err != nil {
		return err
	}

	var bo binlog.BinlogOps
	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("embedding_configs"))
		if bucket == nil {
			return fmt.Errorf("embedding_configs bucket not found")
		}

		// If this is being set as default, unset all other defaults
		if config.IsDefault {
			cursor := bucket.Cursor()
			for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
				var existing EmbeddingConfig
				if err := json.Unmarshal(v, &existing); err != nil {
					continue
				}
				if existing.IsDefault && existing.ID != config.ID {
					existing.IsDefault = false
					data, _ := json.Marshal(existing) // #nosec G117
					_ = bucket.Put([]byte(existing.ID), data)
					bo.Put("embedding_configs", []byte(existing.ID), data)
				}
			}
		}

		data, err := json.Marshal(config) // #nosec G117
		if err != nil {
			return err
		}

		bo.Put("embedding_configs", []byte(config.ID), data)
		return bucket.Put([]byte(config.ID), data)
	})
	if err == nil {
		bo.FlushTo(s.Binlog)
	}
	return err
}

// GetEmbeddingConfig retrieves an embedding configuration by ID
func (s *Server) GetEmbeddingConfig(id string) (*EmbeddingConfig, error) {
	var config EmbeddingConfig
	err := s.DBView(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("embedding_configs"))
		if bucket == nil {
			return fmt.Errorf("embedding_configs bucket not found")
		}

		data := bucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("config not found")
		}

		return json.Unmarshal(data, &config)
	})

	return &config, err
}

// ListEmbeddingConfigs returns all embedding configurations
func (s *Server) ListEmbeddingConfigs() ([]*EmbeddingConfig, error) {
	var configs []*EmbeddingConfig

	err := s.DBView(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("embedding_configs"))
		if bucket == nil {
			return fmt.Errorf("embedding_configs bucket not found")
		}

		return bucket.ForEach(func(k, v []byte) error {
			var config EmbeddingConfig
			if err := json.Unmarshal(v, &config); err != nil {
				return err
			}
			configs = append(configs, &config)
			return nil
		})
	})

	return configs, err
}

// GetDefaultEmbeddingConfig returns the default embedding configuration
func (s *Server) GetDefaultEmbeddingConfig() (*EmbeddingConfig, error) {
	configs, err := s.ListEmbeddingConfigs()
	if err != nil {
		return nil, err
	}

	for _, config := range configs {
		if config.IsDefault {
			return config, nil
		}
	}

	return nil, fmt.Errorf("no default embedding config found")
}

// DeleteEmbeddingConfig deletes an embedding configuration
func (s *Server) DeleteEmbeddingConfig(id string) error {
	key := []byte(id)
	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("embedding_configs"))
		if bucket == nil {
			return fmt.Errorf("embedding_configs bucket not found")
		}

		return bucket.Delete(key)
	})
	if err == nil && s.Binlog != nil {
		_ = s.Binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: "embedding_configs", Key: CopyBytes(key)})
	}
	return err
}

// HTTP Handlers

func (s *Server) handleEmbeddingConfigs(w http.ResponseWriter, r *http.Request) {
	// Check admin permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		s.handleListEmbeddingConfigs(w, r)
	case http.MethodPost:
		s.handleCreateEmbeddingConfig(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEmbeddingConfigDetail(w http.ResponseWriter, r *http.Request) {
	// Check admin permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	// Extract ID from path: /v1/embedding-configs/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	id := parts[3]

	switch r.Method {
	case http.MethodGet:
		s.handleGetEmbeddingConfig(w, r, id)
	case http.MethodPut:
		s.handleUpdateEmbeddingConfig(w, r, id)
	case http.MethodDelete:
		s.handleDeleteEmbeddingConfig(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleListEmbeddingConfigs(w http.ResponseWriter, r *http.Request) {
	if s.Metrics != nil {
		s.Metrics.IncOp("embedding_config_list", "")
	}

	configs, err := s.ListEmbeddingConfigs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"configs": configs,
	}) // nolint:errcheck // HTTP response already committed
}

func (s *Server) handleCreateEmbeddingConfig(w http.ResponseWriter, r *http.Request) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("embedding_config_create", "")
	}

	var config EmbeddingConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if config.ID == "" || config.Name == "" || config.Provider == "" || config.Model == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	validProviders := map[string]bool{
		"openai": true,
		"ollama": true,
		"cohere": true,
		"voyage": true,
	}
	if !validProviders[config.Provider] {
		http.Error(w, "provider must be 'openai', 'ollama', 'cohere', or 'voyage'", http.StatusBadRequest)
		return
	}

	if config.Dimensions <= 0 {
		http.Error(w, "dimensions must be positive", http.StatusBadRequest)
		return
	}

	config.CreatedAt = time.Now().Unix()

	if err := s.SaveEmbeddingConfig(&config); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(config) // #nosec G117 -- API key is intentionally returned to authenticated admin
}

func (s *Server) handleGetEmbeddingConfig(w http.ResponseWriter, r *http.Request, id string) {
	if s.Metrics != nil {
		s.Metrics.IncOp("embedding_config_get", id)
	}

	config, err := s.GetEmbeddingConfig(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(config) // #nosec G117 -- API key is intentionally returned to authenticated admin
}

func (s *Server) handleUpdateEmbeddingConfig(w http.ResponseWriter, r *http.Request, id string) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("embedding_config_update", id)
	}

	var config EmbeddingConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	config.ID = id // Ensure ID matches path

	// Validate
	validProviders := map[string]bool{
		"openai": true,
		"ollama": true,
		"cohere": true,
		"voyage": true,
	}
	if !validProviders[config.Provider] {
		http.Error(w, "provider must be 'openai', 'ollama', 'cohere', or 'voyage'", http.StatusBadRequest)
		return
	}

	if config.Dimensions <= 0 {
		http.Error(w, "dimensions must be positive", http.StatusBadRequest)
		return
	}

	if err := s.SaveEmbeddingConfig(&config); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(config) // #nosec G117 -- API key is intentionally returned to authenticated admin
}

func (s *Server) handleDeleteEmbeddingConfig(w http.ResponseWriter, r *http.Request, id string) {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("embedding_config_delete", id)
	}

	// Check if it's the default config
	config, err := s.GetEmbeddingConfig(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if config.IsDefault {
		http.Error(w, "cannot delete default config", http.StatusBadRequest)
		return
	}

	if err := s.DeleteEmbeddingConfig(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetDefaultEmbeddingConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return
	}

	// Check admin permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("embedding_config_set_default", "")
	}

	var req struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	config, err := s.GetEmbeddingConfig(req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	config.IsDefault = true
	if err := s.SaveEmbeddingConfig(config); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reinitialize embedding with new default config
	s.InitializeEmbeddingFromConfig(config)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "default config updated",
	}) // nolint:errcheck // HTTP response already committed
}

// InitializeEmbeddingFromConfig initializes the embedding system from a config
func (s *Server) InitializeEmbeddingFromConfig(config *EmbeddingConfig) {
	if config == nil {
		return
	}

	var emb embedding.Provider
	switch config.Provider {
	case "openai":
		emb = embedding.NewOpenAIProvider(config.APIKey, "https://api.openai.com/v1", config.Model, config.Dimensions)
	case "ollama":
		emb = embedding.NewOllamaProvider(config.APIURL, config.Model, config.Dimensions)
	case "cohere":
		emb = embedding.NewCohereProvider(config.APIKey, config.APIURL, config.Model, config.Dimensions)
	case "voyage":
		emb = embedding.NewVoyageProvider(config.APIKey, config.APIURL, config.Model, config.Dimensions)
	default:
		return
	}

	s.Embedding = emb

	// Restart embedding worker with new config
	if s.EmbeddingWorker != nil {
		s.EmbeddingWorker.Stop()
	}
	if emb != nil {
		s.EmbeddingWorker = NewEmbeddingWorker(emb, s.VectorStore, s.VectorIndex, 1000)
		s.EmbeddingWorker.SetDiskOnly(s.QuantizedVecIndex, s.collectionDiskOnly)
		s.EmbeddingWorker.Start(2)
	}
}
