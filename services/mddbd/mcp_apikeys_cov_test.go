package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// covDDropKeyBucket removes the key bucket, the state a store is in after a
// restore onto a database that never held MCP keys.
func covDDropKeyBucket(t *testing.T, db *bolt.DB) {
	t.Helper()
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.DeleteBucket([]byte(mcpAPIKeyBucket))
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCovDKeyStoreWithoutBucket(t *testing.T) {
	db := testDB(t)
	store := newMCPAPIKeyStore(db)
	covDDropKeyBucket(t, db)

	if _, err := store.Get("mcp_x"); err == nil || err.Error() != "key not found" {
		t.Errorf("Get without bucket = %v, want key not found", err)
	}
	if keys, err := store.List(); err != nil || len(keys) != 0 {
		t.Errorf("List without bucket = %v, %v; want empty", keys, err)
	}
	if err := store.Delete("mcp_x"); err == nil {
		t.Error("Delete without bucket must fail")
	}
	if err := store.Disable("mcp_x"); err == nil {
		t.Error("Disable without bucket must fail")
	}
	if n := store.Count(); n != 0 {
		t.Errorf("Count without bucket = %d", n)
	}

	// Create recreates the bucket instead of dereferencing a nil one.
	_, key, err := store.Create("after-restore", 0)
	if err != nil {
		t.Fatalf("Create without bucket: %v", err)
	}
	if got, err := store.Get(key); err != nil || got.Name != "after-restore" {
		t.Errorf("created key not readable: %v %v", got, err)
	}
}

func TestCovDKeyStoreErrors(t *testing.T) {
	db := testDB(t)
	store := newMCPAPIKeyStore(db)

	if _, _, err := store.Create("", 0); err == nil || err.Error() != "name is required" {
		t.Errorf("Create without name = %v", err)
	}
	if err := store.Delete("mcp_missing"); err == nil {
		t.Error("Delete of missing key must fail")
	}
	if err := store.Disable("mcp_missing"); err == nil {
		t.Error("Disable of missing key must fail")
	}

	// A corrupt record is skipped by List and refused by Disable.
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(mcpAPIKeyBucket)).Put([]byte("mcp_corrupt"), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create("good", 0); err != nil {
		t.Fatal(err)
	}
	keys, err := store.List()
	if err != nil || len(keys) != 1 || keys[0].Name != "good" {
		t.Errorf("List must skip the corrupt record: %v %v", keys, err)
	}
	if err := store.Disable("mcp_corrupt"); err == nil {
		t.Error("Disable of a corrupt record must fail")
	}

	// A closed database fails the write.
	_ = db.Close()
	if _, _, err := store.Create("closed", 0); err == nil {
		t.Error("Create on a closed database must fail")
	}
}

// covDKeyServer is a Server with only what the key handlers use.
func covDKeyServer(t *testing.T) (*Server, *MCPAPIKeyMiddleware) {
	t.Helper()
	store := newMCPAPIKeyStore(testDB(t))
	mw := &MCPAPIKeyMiddleware{enabled: true, keys: map[string]string{}, keyStore: store, cache: map[string]*apiKeyCacheEntry{}, cacheTTL: time.Minute}
	return &Server{mcpKeyStore: store, mcpAuth: mw}, mw
}

func covDKeyRequest(s *Server, h func(http.ResponseWriter, *http.Request), method, body string, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/mcp/keys", strings.NewReader(body)).WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestCovDKeyHandlersLifecycle(t *testing.T) {
	s, mw := covDKeyServer(t)
	ctx := context.Background()

	// A cached verdict from before the create must not outlive it.
	mw.cache["stale"] = &apiKeyCacheEntry{expires: time.Now().Add(time.Hour)}
	rec := covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodPost, `{"name":"ci","expiresAt":0}`, ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if _, ok := mw.cache["stale"]; ok {
		t.Error("create must invalidate the middleware cache")
	}
	var created struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || !strings.HasPrefix(created.Key, mcpKeySchemePrefix) || created.Name != "ci" {
		t.Fatalf("create body = %s (%v)", rec.Body, err)
	}

	rec = covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodGet, "", ctx)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) || strings.Contains(rec.Body.String(), created.Key) {
		t.Errorf("list = %d %s", rec.Code, rec.Body)
	}

	if name, _, ok := mw.validateKey(created.Key); !ok || name != "ci" {
		t.Errorf("stored key not accepted: %q %v", name, ok)
	}

	rec = covDKeyRequest(s, s.handleMCPAPIKeyDisable, http.MethodPost, `{"key":"`+created.Key+`"}`, ctx)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"disabled"}` {
		t.Errorf("disable = %d %s", rec.Code, rec.Body)
	}
	if len(mw.cache) != 0 {
		t.Error("disable must invalidate the middleware cache")
	}
	if _, _, ok := mw.validateKey(created.Key); ok {
		t.Error("disabled key still accepted")
	}

	rec = covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodDelete, `{"key":"`+created.Key+`"}`, ctx)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"deleted"}` {
		t.Errorf("delete = %d %s", rec.Code, rec.Body)
	}
	if s.mcpKeyStore.Count() != 0 {
		t.Error("key still stored after delete")
	}

	rec = covDKeyRequest(s, s.handleMCPAPIKeys, http.MethodPut, "", ctx)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT = %d", rec.Code)
	}
}

func TestCovDKeyHandlersBadInput(t *testing.T) {
	s, _ := covDKeyServer(t)
	s.mcpAuth = nil // no middleware to invalidate
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		h    func(http.ResponseWriter, *http.Request)
		body string
		want int
	}{
		{"create bad json", s.handleMCPAPIKeyCreate, `{`, http.StatusBadRequest},
		{"create no name", s.handleMCPAPIKeyCreate, `{"name":""}`, http.StatusBadRequest},
		{"delete bad json", s.handleMCPAPIKeyDelete, `{`, http.StatusBadRequest},
		{"delete missing", s.handleMCPAPIKeyDelete, `{"key":"mcp_none"}`, http.StatusNotFound},
		{"disable bad json", s.handleMCPAPIKeyDisable, `{`, http.StatusBadRequest},
		{"disable missing", s.handleMCPAPIKeyDisable, `{"key":"mcp_none"}`, http.StatusNotFound},
	} {
		if rec := covDKeyRequest(s, tc.h, http.MethodPost, tc.body, ctx); rec.Code != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body, tc.want)
		}
	}

	// Create, then delete and disable without a middleware attached.
	rec := covDKeyRequest(s, s.handleMCPAPIKeyCreate, http.MethodPost, `{"name":"x"}`, ctx)
	var created struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec := covDKeyRequest(s, s.handleMCPAPIKeyDisable, http.MethodPost, `{"key":"`+created.Key+`"}`, ctx); rec.Code != http.StatusOK {
		t.Errorf("disable = %d", rec.Code)
	}
	if rec := covDKeyRequest(s, s.handleMCPAPIKeyDelete, http.MethodDelete, `{"key":"`+created.Key+`"}`, ctx); rec.Code != http.StatusOK {
		t.Errorf("delete = %d", rec.Code)
	}

	// A closed database fails the list with 500.
	_ = s.mcpKeyStore.db.Close()
	if rec := covDKeyRequest(s, s.handleMCPAPIKeyList, http.MethodGet, "", ctx); rec.Code != http.StatusInternalServerError {
		t.Errorf("list on closed db = %d", rec.Code)
	}
}

func TestCovDKeyHandlersNoStore(t *testing.T) {
	s := &Server{}
	for name, h := range map[string]func(http.ResponseWriter, *http.Request){
		"create": s.handleMCPAPIKeyCreate, "list": s.handleMCPAPIKeyList,
		"delete": s.handleMCPAPIKeyDelete, "disable": s.handleMCPAPIKeyDisable,
	} {
		if rec := covDKeyRequest(s, h, http.MethodPost, `{}`, context.Background()); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without store = %d", name, rec.Code)
		}
	}
}

func TestCovDKeyHandlersRequireAdmin(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s, _ := covDKeyServer(t)
	s.AuthManager = am

	handlers := map[string]func(http.ResponseWriter, *http.Request){
		"create": s.handleMCPAPIKeyCreate, "list": s.handleMCPAPIKeyList,
		"delete": s.handleMCPAPIKeyDelete, "disable": s.handleMCPAPIKeyDisable,
	}
	anon := context.Background()
	for name, h := range handlers {
		if rec := covDKeyRequest(s, h, http.MethodPost, `{}`, anon); rec.Code != http.StatusForbidden {
			t.Errorf("anonymous %s = %d, want 403", name, rec.Code)
		}
	}

	admin := context.WithValue(anon, authContextKey, &JWTClaims{Username: "admin", Admin: true})
	if rec := covDKeyRequest(s, s.handleMCPAPIKeyCreate, http.MethodPost, `{"name":"adm"}`, admin); rec.Code != http.StatusOK {
		t.Errorf("admin create = %d %s", rec.Code, rec.Body)
	}
	if rec := covDKeyRequest(s, s.handleMCPAPIKeyList, http.MethodGet, "", admin); rec.Code != http.StatusOK {
		t.Errorf("admin list = %d", rec.Code)
	}
	if rec := covDKeyRequest(s, s.handleMCPAPIKeyDelete, http.MethodPost, `{"key":"mcp_none"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("admin delete missing = %d", rec.Code)
	}
	if rec := covDKeyRequest(s, s.handleMCPAPIKeyDisable, http.MethodPost, `{"key":"mcp_none"}`, admin); rec.Code != http.StatusNotFound {
		t.Errorf("admin disable missing = %d", rec.Code)
	}
}

func TestCovDValidateFromStoreCache(t *testing.T) {
	var nilMW *MCPAPIKeyMiddleware
	nilMW.InvalidateCache() // must not panic

	mw := &MCPAPIKeyMiddleware{cache: map[string]*apiKeyCacheEntry{}, cacheTTL: time.Hour}
	if _, _, ok := mw.validateFromStore("mcp_x"); ok {
		t.Error("no store must reject")
	}

	store := newMCPAPIKeyStore(testDB(t))
	mw.keyStore = store
	_, key, err := store.Create("cached", 0)
	if err != nil {
		t.Fatal(err)
	}
	if name, _, ok := mw.validateFromStore(key); !ok || name != "cached" {
		t.Fatalf("first lookup = %q %v", name, ok)
	}
	// Deleting the key without invalidating: the cache still answers.
	if err := store.Delete(key); err != nil {
		t.Fatal(err)
	}
	if name, _, ok := mw.validateFromStore(key); !ok || name != "cached" {
		t.Errorf("cached lookup = %q %v", name, ok)
	}
	mw.InvalidateCache()
	if _, _, ok := mw.validateFromStore(key); ok {
		t.Error("after invalidation the deleted key must be rejected")
	}
}

func TestCovDKeyToolsets(t *testing.T) {
	s, mw := covDKeyServer(t)

	if _, _, err := s.mcpKeyStore.Create("bad", 0, "nope"); err == nil || !strings.Contains(err.Error(), "unknown MCP toolset") {
		t.Errorf("Create with unknown toolset = %v", err)
	}
	rec := covDKeyRequest(s, s.handleMCPAPIKeyCreate, http.MethodPost, `{"name":"bad","toolsets":["nope"]}`, context.Background())
	var errBody map[string]string
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &errBody) != nil || !strings.Contains(errBody["error"], "nope") {
		t.Errorf("handler create with unknown toolset = %d %s", rec.Code, rec.Body)
	}

	rec = covDKeyRequest(s, s.handleMCPAPIKeyCreate, http.MethodPost, `{"name":"coder","toolsets":["search","code"]}`, context.Background())
	var created struct {
		Key      string   `json:"key"`
		Toolsets []string `json:"toolsets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || len(created.Toolsets) != 2 {
		t.Fatalf("create with toolsets = %d %s", rec.Code, rec.Body)
	}
	_, sets, ok := mw.validateKey(created.Key)
	if !ok || !sets[ToolsetSearch] || !sets[ToolsetCode] || len(sets) != 2 {
		t.Errorf("stored toolsets = %v %v", sets, ok)
	}
	keys, _ := s.mcpKeyStore.List()
	if len(keys) != 1 || len(keys[0].Toolsets) != 2 {
		t.Errorf("list toolsets = %+v", keys)
	}

	// A stored toolset this version no longer knows fails closed: no tools.
	raw, _ := json.Marshal(MCPAPIKey{Key: "mcp_legacy", Name: "legacy", CreatedAt: 1, Toolsets: []string{"retired"}})
	if err := s.mcpKeyStore.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(mcpAPIKeyBucket)).Put([]byte("mcp_legacy"), raw)
	}); err != nil {
		t.Fatal(err)
	}
	name, sets, ok := mw.validateFromStore("mcp_legacy")
	if !ok || name != "legacy" || sets == nil || len(sets) != 0 {
		t.Errorf("retired toolset = %q %v %v; want valid key limited to nothing", name, sets, ok)
	}
}
