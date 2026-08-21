package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	bolt "go.etcd.io/bbolt"
)

// MCP API Key management — stores keys in internal BoltDB bucket "_mcp_api_keys".
// Keys persist across restarts. Managed via REST API (requires admin auth).

const mcpAPIKeyBucket = "_mcp_api_keys" // #nosec G101 -- bucket name, not a credential

// mcpKeySchemePrefix marks a string as an MCP API key. It is a fixed scheme
// marker, not key material — every key starts with it.
const mcpKeySchemePrefix = "mcp_"

// keyFingerprint derives a short, non-reversible identifier for an API key so
// operations can be correlated in logs without writing any byte of the key
// itself there (SEC-012). Four bytes of SHA-256 identify a key among any
// realistic number of them while revealing nothing usable to a log reader.
func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// MCPAPIKey represents a stored MCP API key.
type MCPAPIKey struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"` // 0 = never
	Disabled  bool   `json:"disabled,omitempty"`
}

// mcpAPIKeyStore provides CRUD for MCP API keys in BoltDB.
type mcpAPIKeyStore struct {
	db *bolt.DB
}

func newMCPAPIKeyStore(db *bolt.DB) *mcpAPIKeyStore {
	// Ensure bucket exists
	_ = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(mcpAPIKeyBucket))
		return err
	})
	return &mcpAPIKeyStore{db: db}
}

// Create generates a new API key and stores it.
func (s *mcpAPIKeyStore) Create(name string, expiresAt int64) (*MCPAPIKey, string, error) {
	if name == "" {
		return nil, "", fmt.Errorf("name is required")
	}

	// Generate key: mcp_ + 32 random hex chars
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, "", fmt.Errorf("failed to generate key: %w", err)
	}
	key := mcpKeySchemePrefix + hex.EncodeToString(b)

	apiKey := MCPAPIKey{
		Key:       key,
		Name:      name,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: expiresAt,
	}

	data, err := json.Marshal(apiKey)
	if err != nil {
		return nil, "", err
	}

	err = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		return b.Put([]byte(key), data)
	})
	if err != nil {
		return nil, "", err
	}

	log.Printf("MCP API key created: name=%s fingerprint=%s", name, keyFingerprint(key))
	return &apiKey, key, nil
}

// Get retrieves an API key by its key string.
func (s *mcpAPIKeyStore) Get(key string) (*MCPAPIKey, error) {
	var apiKey MCPAPIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		if b == nil {
			return fmt.Errorf("key not found")
		}
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("key not found")
		}
		return json.Unmarshal(data, &apiKey)
	})
	if err != nil {
		return nil, err
	}

	// Check expiry
	if apiKey.ExpiresAt > 0 && time.Now().Unix() > apiKey.ExpiresAt {
		return nil, fmt.Errorf("key expired")
	}
	if apiKey.Disabled {
		return nil, fmt.Errorf("key disabled")
	}

	return &apiKey, nil
}

// List returns all API keys (without the full key — only prefix shown).
func (s *mcpAPIKeyStore) List() ([]MCPAPIKeySummary, error) {
	var keys []MCPAPIKeySummary
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var ak MCPAPIKey
			if err := json.Unmarshal(v, &ak); err != nil {
				return nil // skip corrupt entries
			}
			// SEC-012: the summary identifies a key by fingerprint. KeyPrefix
			// keeps its shape for existing callers but now carries only the
			// scheme marker, never the random part of the key.
			keys = append(keys, MCPAPIKeySummary{
				KeyPrefix:   mcpKeySchemePrefix,
				Fingerprint: keyFingerprint(string(k)),
				Name:        ak.Name,
				CreatedAt:   ak.CreatedAt,
				ExpiresAt:   ak.ExpiresAt,
				Disabled:    ak.Disabled,
			})
			return nil
		})
	})
	return keys, err
}

// Delete removes an API key.
func (s *mcpAPIKeyStore) Delete(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		if b == nil {
			return fmt.Errorf("key not found")
		}
		if b.Get([]byte(key)) == nil {
			return fmt.Errorf("key not found")
		}
		log.Printf("MCP API key deleted: fingerprint=%s", keyFingerprint(key))
		return b.Delete([]byte(key))
	})
}

// Disable marks a key as disabled without deleting it.
func (s *mcpAPIKeyStore) Disable(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		if b == nil {
			return fmt.Errorf("key not found")
		}
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("key not found")
		}
		var ak MCPAPIKey
		if err := json.Unmarshal(data, &ak); err != nil {
			return err
		}
		ak.Disabled = true
		updated, err := json.Marshal(ak)
		if err != nil {
			return err
		}
		log.Printf("MCP API key disabled: name=%s", ak.Name)
		return b.Put([]byte(key), updated)
	})
}

// Count returns the number of stored keys.
func (s *mcpAPIKeyStore) Count() int {
	count := 0
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		if b == nil {
			return nil
		}
		count = b.Stats().KeyN
		return nil
	})
	return count
}

// MCPAPIKeySummary is the public representation of a key (no key material
// exposed). Fingerprint is the stable identifier to display or correlate with
// logs; KeyPrefix is retained for response-shape compatibility and carries only
// the scheme marker (SEC-012).
type MCPAPIKeySummary struct {
	KeyPrefix   string `json:"keyPrefix"`
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
	CreatedAt   int64  `json:"createdAt"`
	ExpiresAt   int64  `json:"expiresAt,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
}

// ---- HTTP Handlers (mounted on main HTTP port, requires admin auth) ----

// handleMCPAPIKeys multiplexes GET (list) / POST (create) / DELETE on /v1/mcp/keys.
func (s *Server) handleMCPAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleMCPAPIKeyList(w, r)
	case http.MethodPost:
		s.handleMCPAPIKeyCreate(w, r)
	case http.MethodDelete:
		s.handleMCPAPIKeyDelete(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMCPAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if s.mcpKeyStore == nil {
		http.Error(w, `{"error":"MCP API key store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"admin permission required"}`, http.StatusForbidden)
			return
		}
	}

	var req struct {
		Name      string `json:"name"`
		ExpiresAt int64  `json:"expiresAt,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	apiKey, fullKey, err := s.mcpKeyStore.Create(req.Name, req.ExpiresAt)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Invalidate middleware cache
	if s.mcpAuth != nil {
		s.mcpAuth.InvalidateCache()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key":       fullKey, // Full key shown only once
		"name":      apiKey.Name,
		"createdAt": apiKey.CreatedAt,
		"expiresAt": apiKey.ExpiresAt,
	})
}

func (s *Server) handleMCPAPIKeyList(w http.ResponseWriter, r *http.Request) {
	if s.mcpKeyStore == nil {
		http.Error(w, `{"error":"MCP API key store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"admin permission required"}`, http.StatusForbidden)
			return
		}
	}

	keys, err := s.mcpKeyStore.List()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"keys":  keys,
		"total": len(keys),
	})
}

func (s *Server) handleMCPAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	if s.mcpKeyStore == nil {
		http.Error(w, `{"error":"MCP API key store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"admin permission required"}`, http.StatusForbidden)
			return
		}
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if err := s.mcpKeyStore.Delete(req.Key); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}

	if s.mcpAuth != nil {
		s.mcpAuth.InvalidateCache()
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"deleted"}`))
}

func (s *Server) handleMCPAPIKeyDisable(w http.ResponseWriter, r *http.Request) {
	if s.mcpKeyStore == nil {
		http.Error(w, `{"error":"MCP API key store not initialized"}`, http.StatusServiceUnavailable)
		return
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"admin permission required"}`, http.StatusForbidden)
			return
		}
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	if err := s.mcpKeyStore.Disable(req.Key); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}

	if s.mcpAuth != nil {
		s.mcpAuth.InvalidateCache()
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"disabled"}`))
}

// InvalidateCache clears the dynamic key cache after key changes.
func (m *MCPAPIKeyMiddleware) InvalidateCache() {
	if m == nil {
		return
	}
	m.cacheMu.Lock()
	m.cache = make(map[string]*apiKeyCacheEntry)
	m.cacheMu.Unlock()
}

// validateFromStore checks a key against the BoltDB store.
func (m *MCPAPIKeyMiddleware) validateFromStore(provided string) (string, bool) {
	if m.keyStore == nil {
		return "", false
	}

	// Check cache
	m.cacheMu.RLock()
	if entry, ok := m.cache[provided]; ok && time.Now().Before(entry.expires) {
		m.cacheMu.RUnlock()
		return entry.name, entry.valid
	}
	m.cacheMu.RUnlock()

	ak, err := m.keyStore.Get(provided)
	valid := err == nil && ak != nil
	name := ""
	if valid {
		name = ak.Name
	}

	// Cache result
	m.cacheMu.Lock()
	m.cache[provided] = &apiKeyCacheEntry{name: name, valid: valid, expires: time.Now().Add(m.cacheTTL)}
	m.cacheMu.Unlock()

	return name, valid
}
