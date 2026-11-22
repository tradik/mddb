package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mddb/internal/boltx"
	"net/http"
	"strings"
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
	// Toolsets limits the key to these MCP toolsets (MCP-003); empty = all
	// the server exposes. A key for a coding agent might carry ["search","code"].
	Toolsets []string `json:"toolsets,omitempty"`
	// Collections, ReadOnly and Scenario are the key's scope (#439,
	// mcp_key_scope.go). Collections may name aliases and "role:" targets,
	// resolved when a call is checked; empty = every collection. Scenario
	// binds the key to a chat scenario's allowedCollections, read-only. A
	// key stored before 2.19.9 has none of them and is not scoped.
	Collections []string `json:"collections,omitempty"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
	Scenario    string   `json:"scenario,omitempty"`
	// ReadOnlyCollections are names among Collections the key may only read
	// (AGT-007); MemoryNamespace names the memory a limited key shares with
	// other keys of that name (AGT-008), else it has one of its own.
	ReadOnlyCollections []string `json:"readOnlyCollections,omitempty"`
	MemoryNamespace     string   `json:"memoryNamespace,omitempty"`
}

// scope is the key's scope as the middleware carries it.
func (k *MCPAPIKey) scope() mcpKeyScope {
	return mcpKeyScope{collections: k.Collections, readOnly: k.ReadOnly, scenario: k.Scenario,
		readOnlyCollections: k.ReadOnlyCollections, memoryNamespace: keyMemoryNamespace(k.Key, k.MemoryNamespace)}
}

// mcpAPIKeyStore provides CRUD for MCP API keys in BoltDB.
type mcpAPIKeyStore struct {
	db     *bolt.DB
	follow func() *bolt.DB // set by FollowDB: the server's current handle after a restore
}

// FollowDB makes it use the database src returns — the server's current
// handle — so a restore or replication snapshot does not leave it on the
// closed old one.
func (s *mcpAPIKeyStore) FollowDB(src func() *bolt.DB) { s.follow = src }

func (s *mcpAPIKeyStore) handle() *bolt.DB { return boltx.Current(s.db, s.follow) }

func newMCPAPIKeyStore(db *bolt.DB) *mcpAPIKeyStore {
	_ = boltx.EnsureBuckets(db, []byte(mcpAPIKeyBucket))
	return &mcpAPIKeyStore{db: db}
}

// Create generates a new API key from spec — its name, expiry, toolsets
// (MCP-003) and scope (#439) — and stores it. spec.Key and spec.CreatedAt
// are set here.
func (s *mcpAPIKeyStore) Create(spec MCPAPIKey) (*MCPAPIKey, string, error) {
	if spec.Name == "" {
		return nil, "", fmt.Errorf("name is required")
	}
	if _, err := parseToolsets(spec.Toolsets); err != nil {
		return nil, "", err
	}
	for _, c := range spec.Collections {
		if strings.TrimSpace(c) == "" {
			return nil, "", fmt.Errorf("collections may not hold an empty name")
		}
	}

	// Generate key: mcp_ + 32 random hex chars
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error (Go 1.24+)
	key := mcpKeySchemePrefix + hex.EncodeToString(b)

	apiKey := spec
	apiKey.Key, apiKey.CreatedAt, apiKey.Disabled = key, time.Now().Unix(), false

	// Marshal cannot fail on a struct of strings, numbers, bools and string
	// slices.
	data, _ := json.Marshal(apiKey)

	err := s.handle().Update(func(tx *bolt.Tx) error {
		// The bucket is created at startup, but a restore or replication
		// snapshot can install a database that never had it; the other
		// methods treat that as "no keys", so Create recreates it rather than
		// writing into a nil bucket.
		b, err := tx.CreateBucketIfNotExists([]byte(mcpAPIKeyBucket))
		if err == nil {
			err = b.Put([]byte(key), data)
		}
		return err
	})
	if err != nil {
		return nil, "", err
	}

	slog.Info("MCP API key created", "name", apiKey.Name, "keyFingerprint", keyFingerprint(key))
	return &apiKey, key, nil
}

// Get retrieves an API key by its key string.
func (s *mcpAPIKeyStore) Get(key string) (*MCPAPIKey, error) {
	var apiKey MCPAPIKey
	err := s.handle().View(func(tx *bolt.Tx) error {
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
	err := s.handle().View(func(tx *bolt.Tx) error {
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
				Toolsets:    ak.Toolsets,
				Collections: ak.Collections,
				ReadOnly:    ak.ReadOnly,
				Scenario:    ak.Scenario,

				ReadOnlyCollections: ak.ReadOnlyCollections,
				MemoryNamespace:     ak.MemoryNamespace,
			})
			return nil
		})
	})
	return keys, err
}

// Delete removes an API key.
func (s *mcpAPIKeyStore) Delete(key string) error {
	return s.handle().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		if b == nil {
			return fmt.Errorf("key not found")
		}
		if b.Get([]byte(key)) == nil {
			return fmt.Errorf("key not found")
		}
		slog.Info("MCP API key deleted", "keyFingerprint", keyFingerprint(key))
		return b.Delete([]byte(key))
	})
}

// Disable marks a key as disabled without deleting it.
func (s *mcpAPIKeyStore) Disable(key string) error {
	return s.handle().Update(func(tx *bolt.Tx) error {
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
		updated, _ := json.Marshal(ak) // cannot fail: see Create
		slog.Info("MCP API key disabled", "name", ak.Name)
		return b.Put([]byte(key), updated)
	})
}

// Count returns the number of stored keys.
func (s *mcpAPIKeyStore) Count() int {
	count := 0
	_ = s.handle().View(func(tx *bolt.Tx) error {
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
	// Toolsets the key is limited to (MCP-003); absent = all.
	Toolsets []string `json:"toolsets,omitempty"`
	// The key's scope (#439), as stored; absent = not scoped.
	Collections []string `json:"collections,omitempty"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
	Scenario    string   `json:"scenario,omitempty"`
	// AGT-007 and AGT-008, as stored.
	ReadOnlyCollections []string `json:"readOnlyCollections,omitempty"`
	MemoryNamespace     string   `json:"memoryNamespace,omitempty"`
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

	// name, expiresAt, toolsets (MCP-003), collections, readOnly and
	// scenario (#439), readOnlyCollections (AGT-007) and memoryNamespace
	// (AGT-008); the key itself and the dates are the server's.
	var req MCPAPIKey
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	apiKey, fullKey, err := s.createMCPAPIKey(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Invalidate middleware cache
	if s.mcpAuth != nil {
		s.mcpAuth.InvalidateCache()
	}

	resp := map[string]interface{}{
		"key":         fullKey, // Full key shown only once
		"name":        apiKey.Name,
		"createdAt":   apiKey.CreatedAt,
		"expiresAt":   apiKey.ExpiresAt,
		"toolsets":    apiKey.Toolsets,
		"collections": apiKey.Collections,
		"readOnly":    apiKey.ReadOnly,
		"scenario":    apiKey.Scenario,

		"readOnlyCollections": apiKey.ReadOnlyCollections,
		"memoryNamespace":     apiKey.MemoryNamespace,
	}
	// The key is created either way, so a client that sets the variable
	// afterwards keeps it — but the caller learns now that no listener on
	// this server checks it yet (#445).
	if !mcpKeyAuthEnabled() {
		resp["warning"] = mcpKeyAuthOffMessage
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// createMCPAPIKey stores a key after checking the scenario it is bound to
// names a stored scenario (#439) and its readOnlyCollections and
// memoryNamespace fit its scope (validateKeyScopeSpec). Its collections are
// kept as given: an alias or a "role:" target is resolved when a call is
// checked.
func (s *Server) createMCPAPIKey(spec MCPAPIKey) (*MCPAPIKey, string, error) {
	if err := s.validateKeyScopeSpec(&spec); err != nil {
		return nil, "", err
	}
	if spec.Scenario != "" {
		if s.ChatScenarios == nil {
			return nil, "", errChatScenariosUnavailable
		}
		if _, ok := s.ChatScenarios.Get(spec.Scenario); !ok {
			return nil, "", fmt.Errorf("%w: %q", errChatScenarioNotFound, spec.Scenario)
		}
	}
	return s.mcpKeyStore.Create(spec)
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
	s.changeMCPAPIKey(w, r, (*mcpAPIKeyStore).Delete, `{"status":"deleted"}`)
}

func (s *Server) handleMCPAPIKeyDisable(w http.ResponseWriter, r *http.Request) {
	s.changeMCPAPIKey(w, r, (*mcpAPIKeyStore).Disable, `{"status":"disabled"}`)
}

// mcpAPIKeyRef names the key a delete or disable acts on: the full key, or —
// since 2.19.6, for a caller that only has the listing — its fingerprint.
type mcpAPIKeyRef struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

// errMCPKeyAmbiguous: two stored keys share the fingerprint given.
var errMCPKeyAmbiguous = errors.New("fingerprint matches more than one key: use the key")

// changeMCPAPIKey runs a delete or disable: admin only, the key named by its
// value or its fingerprint, the auth cache invalidated afterwards.
func (s *Server) changeMCPAPIKey(w http.ResponseWriter, r *http.Request, apply func(*mcpAPIKeyStore, string) error, done string) {
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

	var req mcpAPIKeyRef
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	key, err := s.mcpKeyStore.resolve(req)
	if err == nil {
		err = apply(s.mcpKeyStore, key)
	}
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, errMCPKeyAmbiguous) {
			status = http.StatusConflict
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), status)
		return
	}

	if s.mcpAuth != nil {
		s.mcpAuth.InvalidateCache()
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(done))
}

// resolve returns the key a reference names: the key itself when given, else
// the one stored key with that fingerprint. Four bytes of SHA-256 rarely
// collide, but when two keys share them neither is acted on.
func (s *mcpAPIKeyStore) resolve(ref mcpAPIKeyRef) (string, error) {
	if ref.Key != "" || ref.Fingerprint == "" {
		return ref.Key, nil
	}
	var matches []string
	err := s.handle().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(mcpAPIKeyBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, _ []byte) error {
			if keyFingerprint(string(k)) == ref.Fingerprint {
				matches = append(matches, string(k))
			}
			return nil
		})
	})
	switch {
	case err != nil:
		return "", err
	case len(matches) > 1:
		return "", errMCPKeyAmbiguous
	case len(matches) == 0:
		return "", fmt.Errorf("key not found")
	}
	return matches[0], nil
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
func (m *MCPAPIKeyMiddleware) validateFromStore(provided string) (mcpKeyGrant, bool) {
	if m.keyStore == nil {
		return mcpKeyGrant{}, false
	}

	// Check cache
	m.cacheMu.RLock()
	if entry, ok := m.cache[provided]; ok && time.Now().Before(entry.expires) {
		m.cacheMu.RUnlock()
		return entry.grant, entry.valid
	}
	m.cacheMu.RUnlock()

	ak, err := m.keyStore.Get(provided)
	valid := err == nil && ak != nil
	var grant mcpKeyGrant
	if valid {
		grant = mcpKeyGrant{name: ak.Name, scope: ak.scope()}
		// Stored toolsets were validated on create; a name a later version
		// removed would fail here, and fails closed: the key gets nothing.
		if grant.toolsets, err = parseToolsets(ak.Toolsets); err != nil {
			grant.toolsets = map[string]bool{}
		}
	}

	// Cache result
	m.cacheMu.Lock()
	m.cache[provided] = &apiKeyCacheEntry{grant: grant, valid: valid, expires: time.Now().Add(m.cacheTTL)}
	m.cacheMu.Unlock()

	return grant, valid
}
