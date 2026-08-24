package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// GO-035: a collection config holds two credentials — the S3 secret key and
// the mddb-sync publish key — and every read returned both in full. Reading a
// collection's config needs read permission on that collection, which is
// permission to read its documents; it is not permission to collect the
// credentials for the bucket underneath them, which reach every other
// collection sharing it.

func seedSecretConfig(t *testing.T, s *Server, collection string) {
	t.Helper()
	cfg := &CollectionConfig{
		Type:           "website",
		StorageBackend: "s3",
		StorageConfig: &StorageConfigDef{
			Endpoint: "s3.example.com", Bucket: "docs", AccessKey: "AKIA", SecretKey: "s3cret",
		},
		WordPress: &WordPressTargetConfig{URL: "https://blog.example.com", APIKey: "wp-key"},
	}
	if err := s.CollectionManager.Set(collection, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestRESTCollectionConfigGetMasksSecrets(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	seedSecretConfig(t, s, "blog")

	req := httptest.NewRequest(http.MethodGet, "/v1/collection-config?collection=blog", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigGet(w, req)

	body := w.Body.String()
	// Checked against the raw response rather than a decoded struct: a leak
	// through a field this test forgot to name is still a leak.
	for _, secret := range []string{"s3cret", "wp-key"} {
		if strings.Contains(body, secret) {
			t.Errorf("response contains the credential %q:\n%s", secret, body)
		}
	}

	var parsed struct {
		Config struct {
			StorageConfig struct {
				Bucket       string `json:"bucket"`
				SecretKeySet bool   `json:"secretKeySet"`
			} `json:"storageConfig"`
			WordPress struct {
				URL       string `json:"url"`
				APIKeySet bool   `json:"apiKeySet"`
			} `json:"wordpress"`
		} `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !parsed.Config.StorageConfig.SecretKeySet || !parsed.Config.WordPress.APIKeySet {
		t.Error("presence flags should say a credential is stored")
	}
	// Everything that is not a secret still has to come back, or masking would
	// have broken configuration reads rather than protected them.
	if parsed.Config.StorageConfig.Bucket != "docs" || parsed.Config.WordPress.URL == "" {
		t.Errorf("non-secret fields were dropped: %+v", parsed.Config)
	}
}

func TestRESTCollectionConfigListMasksSecrets(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	seedSecretConfig(t, s, "blog")

	req := httptest.NewRequest(http.MethodGet, "/v1/collection-configs", nil)
	w := httptest.NewRecorder()
	s.handleCollectionConfigList(w, req)

	for _, secret := range []string{"s3cret", "wp-key"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("listing contains the credential %q", secret)
		}
	}
}

func TestRESTCollectionConfigKeepsSecretsOnRoundTrip(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	seedSecretConfig(t, s, "blog")

	// What a UI does: read the config, edit a field, write it back. The read
	// returned a blank secret, so the write carries a blank secret.
	body := `{"collection":"blog","type":"website","storageBackend":"s3",
	          "storageConfig":{"endpoint":"s3.example.com","bucket":"renamed","accessKey":"AKIA"},
	          "wordpress":{"url":"https://blog.example.com"}}`
	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCollectionConfigSet(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Result().StatusCode, w.Body.String())
	}

	cfg, found := s.CollectionManager.Get("blog")
	if !found {
		t.Fatal("collection disappeared")
	}
	if cfg.StorageConfig.Bucket != "renamed" {
		t.Errorf("bucket should have changed, got %q", cfg.StorageConfig.Bucket)
	}
	if cfg.StorageConfig.SecretKey != "s3cret" {
		t.Errorf("secret key lost on round trip, got %q", cfg.StorageConfig.SecretKey)
	}
	if cfg.WordPress.APIKey != "wp-key" {
		t.Errorf("WordPress key lost on round trip, got %q", cfg.WordPress.APIKey)
	}
}

func TestRESTCollectionConfigStoresANewSecret(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	seedSecretConfig(t, s, "blog")

	body := `{"collection":"blog","type":"website","storageBackend":"s3",
	          "storageConfig":{"endpoint":"s3.example.com","bucket":"docs","secretKey":"rotated"}}`
	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCollectionConfigSet(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Result().StatusCode, w.Body.String())
	}
	cfg, _ := s.CollectionManager.Get("blog")
	if cfg.StorageConfig.SecretKey != "rotated" {
		t.Errorf("rotation did not take, got %q", cfg.StorageConfig.SecretKey)
	}
}

// A client cannot promote itself by claiming a secret exists: the presence
// flag is output-only and is cleared on the way in.
func TestRESTCollectionConfigIgnoresAClientSuppliedPresenceFlag(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()

	body := `{"collection":"fresh","type":"website",
	          "storageConfig":{"bucket":"docs","secretKeySet":true},
	          "wordpress":{"url":"https://x.example.com","apiKeySet":true}}`
	req := httptest.NewRequest(http.MethodPut, "/v1/collection-config", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCollectionConfigSet(w, req)

	cfg, found := s.CollectionManager.Get("fresh")
	if !found {
		t.Fatalf("not stored: %s", w.Body.String())
	}
	if cfg.StorageConfig.SecretKeySet || cfg.WordPress.APIKeySet {
		t.Error("a client-supplied presence flag reached storage, so the config would claim a credential it does not hold")
	}
}

// The MCP path answers an LLM, which may repeat what it is given into a
// transcript, a log or a reply.
func TestMCPCollectionConfigMasksSecrets(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	seedSecretConfig(t, s, "blog")

	client := &DirectClient{server: s}

	resp, err := client.GetCollectionConfig(context.Background(), "blog")
	if err != nil {
		t.Fatalf("GetCollectionConfig: %v", err)
	}
	if resp.Config.StorageConfig.SecretKey != "" || resp.Config.WordPress.APIKey != "" {
		t.Error("MCP handed an agent the stored credentials")
	}
	if !resp.Config.StorageConfig.SecretKeySet || !resp.Config.WordPress.APIKeySet {
		t.Error("presence flags should say a credential is stored")
	}

	list, err := client.ListCollectionConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListCollectionConfigs: %v", err)
	}
	listed := list.Configs["blog"]
	if listed == nil {
		t.Fatal("collection missing from the MCP listing")
	}
	if listed.StorageConfig.SecretKey != "" || listed.WordPress.APIKey != "" {
		t.Error("MCP listing leaked the stored credentials")
	}
}

// The stored config must keep its secrets: masking happens on the copy, not in
// the store. Redacting in place would delete the credential on first read.
func TestRedactDoesNotTouchTheStoredConfig(t *testing.T) {
	stored := &CollectionConfig{
		StorageConfig: &StorageConfigDef{SecretKey: "s3cret"},
		WordPress:     &WordPressTargetConfig{APIKey: "wp-key"},
	}

	redacted := redactCollectionConfig(stored)

	if stored.StorageConfig.SecretKey != "s3cret" || stored.WordPress.APIKey != "wp-key" {
		t.Fatal("redaction modified the stored config")
	}
	if redacted.StorageConfig.SecretKey != "" || redacted.WordPress.APIKey != "" {
		t.Fatal("redaction did not blank the copy")
	}
}

func TestRedactHandlesConfigsWithoutSecrets(t *testing.T) {
	if redactCollectionConfig(nil) != nil {
		t.Error("nil config should redact to nil")
	}
	plain := redactCollectionConfig(&CollectionConfig{Type: "default"})
	if plain.StorageConfig != nil || plain.WordPress != nil {
		t.Error("blocks that were absent should stay absent")
	}
	if plain.Type != "default" {
		t.Error("redaction dropped an unrelated field")
	}
}

// MCP writes go through the same carry-over as REST and gRPC — an agent that
// read a masked config and wrote it back would otherwise erase the credential.
func TestMCPSetCollectionConfigKeepsSecretsAndTarget(t *testing.T) {
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	seedSecretConfig(t, s, "blog")

	client := &DirectClient{server: s}
	if err := client.SetCollectionConfig(context.Background(), &MCPSetCollectionConfigRequest{
		Collection:  "blog",
		Type:        "website",
		Description: "an agent editing the description",
	}); err != nil {
		t.Fatalf("SetCollectionConfig: %v", err)
	}

	cfg, _ := s.CollectionManager.Get("blog")
	if cfg.Description != "an agent editing the description" {
		t.Errorf("description should have changed, got %q", cfg.Description)
	}
	if cfg.WordPress == nil {
		t.Fatal("the publishing target was deleted by an unrelated update")
	}
	if cfg.WordPress.APIKey != "wp-key" || cfg.StorageConfig.SecretKey != "s3cret" {
		t.Errorf("credentials lost: %+v %+v", cfg.StorageConfig, cfg.WordPress)
	}
}
