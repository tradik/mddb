package main

import (
	"crypto/cipher"
	"errors"
	"testing"
)

func TestCovDOAuthAEAD(t *testing.T) {
	if _, err := newOAuthAEAD(make([]byte, 5)); err == nil {
		t.Error("a 5-byte AES key must be refused")
	}
	aead, err := newOAuthAEAD(make([]byte, 32))
	if err != nil || aead.NonceSize() != 12 {
		t.Errorf("AES-256-GCM = %v, %v", aead, err)
	}
}

// TestCovDOAuthCipherFailure replaces the cipher constructor, so it must not
// run in parallel with other OAuth tests.
func TestCovDOAuthCipherFailure(t *testing.T) {
	orig := mcpOAuthNewAEAD
	t.Cleanup(func() { mcpOAuthNewAEAD = orig })
	mcpOAuthNewAEAD = func([]byte) (cipher.AEAD, error) { return nil, errors.New("no cipher") }

	keys := &MCPAPIKeyMiddleware{enabled: true, keys: map[string]string{}, cache: map[string]*apiKeyCacheEntry{}}
	if o, err := newMCPOAuth("https://mcp.example.com", "s", keys); o != nil || err == nil || err.Error() != "no cipher" {
		t.Errorf("newMCPOAuth = %v, %v", o, err)
	}

	t.Setenv("MDDB_MCP_OAUTH_ENABLED", "true")
	t.Setenv("MDDB_MCP_OAUTH_ISSUER", "https://mcp.example.com")
	t.Setenv("MDDB_MCP_OAUTH_SECRET", "0123456789abcdef0123456789abcdef")
	if o := newMCPOAuthFromEnv(keys); o != nil {
		t.Error("OAuth must stay off when its cipher cannot be built")
	}
}
