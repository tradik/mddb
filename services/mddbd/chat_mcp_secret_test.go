package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/encryption"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// MCP-005: the write-only secrets of external MCP servers.

func TestValidChatMcpSecret(t *testing.T) {
	for _, c := range []struct{ name, header, value, want string }{
		{"wiki", "", "tok", ""},
		{"wiki", "X-Api-Key", "tok", ""},
		{"", "", "tok", "secret name"},
		{"wiki", "", "", "1 to 8192 bytes"},
		{"wiki", "", strings.Repeat("x", maxChatMcpSecretBytes+1), "1 to 8192 bytes"},
		{"wiki", "", "a\nb", "line break"},
		{"wiki", "X Bad", "tok", "not a header"},
		{"wiki", "mcp-session-id", "tok", "not a header"},
	} {
		err := validChatMcpSecret(c.name, c.header, c.value)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

// chatMcpKey loads an at-rest key for the test: an MCP secret is stored only
// sealed. The key goes away with the test.
func chatMcpKey(t *testing.T) {
	t.Helper()
	t.Setenv("MDDB_ENCRYPTION_KEY", genKey(t))
	e, err := encryption.NewEncryptor()
	if err != nil {
		t.Fatal(err)
	}
	SetGlobalEncryptor(e)
	t.Cleanup(func() { SetGlobalEncryptor(nil) })
}

// Without an at-rest key a secret is refused and nothing reaches the disk.
func TestChatMcpSecretNeedsKey(t *testing.T) {
	SetGlobalEncryptor(nil)
	db := openChatDB(t, filepath.Join(t.TempDir(), "chat.db"))
	m := NewChatScenarioManager(db)
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if chatMcpSecretsKeyReady() {
		t.Error("ready without a key")
	}
	if _, err := m.PutMcpSecret("wiki", "", []byte("plain-tok")); !errors.Is(err, errChatMcpSecretsNeedKey) ||
		!strings.Contains(err.Error(), "set MDDB_ENCRYPTION_KEY and restart") || strings.Contains(err.Error(), "plain-tok") {
		t.Fatalf("put without a key: %v", err)
	}
	if m.HasMcpSecret("wiki") || m.Version() != 0 {
		t.Error("a refused put was kept")
	}
	_ = db.View(func(tx *bolt.Tx) error {
		if n := tx.Bucket(bucketChatMcpSecrets).Stats().KeyN; n != 0 {
			t.Errorf("%d secrets on disk", n)
		}
		return nil
	})
}

// A value stored unsealed (by a build before the key was required) is
// refused when read, with or without a key, never used; the listing says it
// is not set.
func TestChatMcpSecretUnsealedRefused(t *testing.T) {
	db := openChatDB(t, filepath.Join(t.TempDir(), "chat.db"))
	m := NewChatScenarioManager(db)
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	_ = db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketChatMcpSecrets).Put([]byte("old"), []byte(`{"name":"old","value":"cGxhaW4tdG9r"}`))
	})
	if err := m.LoadAll(); err != nil || !m.HasMcpSecret("old") {
		t.Fatalf("reload: %v", err)
	}
	for _, withKey := range []bool{false, true} {
		if withKey {
			chatMcpKey(t)
		}
		h, value, err := m.McpSecretHeader("old")
		if !errors.Is(err, errChatMcpSecretUnsealed) || h != nil || value != "" || strings.Contains(err.Error(), "plain-tok") ||
			!strings.Contains(err.Error(), "MDDB_ENCRYPTION_KEY") {
			t.Errorf("key %v: %v %q %v", withKey, h, value, err)
		}
	}
	if list := m.ListMcpSecrets(); len(list) != 1 || list[0].Set {
		t.Errorf("list: %+v", list[0])
	}
}

func TestChatMcpSecretStore(t *testing.T) {
	chatMcpKey(t)
	db := openChatDB(t, filepath.Join(t.TempDir(), "chat.db"))
	m := NewChatScenarioManager(db)
	if (*ChatScenarioManager)(nil).HasMcpSecret("x") {
		t.Error("a nil manager has a secret")
	}
	if _, err := m.PutMcpSecret("early", "", []byte("v")); err == nil {
		t.Error("a write before LoadAll was accepted")
	}
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	created, err := m.PutMcpSecret("wiki", "", []byte("tok-1"))
	if err != nil || !created || !m.HasMcpSecret("wiki") || m.Version() != 1 {
		t.Fatalf("put: %v %v version %d", created, err, m.Version())
	}
	if created, _ := m.PutMcpSecret("wiki", "", []byte("tok-2")); created {
		t.Error("a replace reported a new secret")
	}
	if _, err := m.PutMcpSecret("api", "X-Api-Key", []byte("k")); err != nil {
		t.Fatal(err)
	}
	h, value, err := m.McpSecretHeader("wiki")
	if err != nil || h.Get("Authorization") != "Bearer tok-2" || value != "tok-2" {
		t.Errorf("bearer: %v %q %v", h, value, err)
	}
	if h, _, _ := m.McpSecretHeader("api"); h.Get("X-Api-Key") != "k" {
		t.Errorf("header: %v", h)
	}
	if _, _, err := m.McpSecretHeader("ghost"); !errors.Is(err, errChatMcpSecretNotFound) {
		t.Errorf("missing: %v", err)
	}

	// A scenario naming a secret keeps it; the listing says so and never
	// carries a value.
	if _, err := m.Put(&ChatScenario{Name: "support", McpServers: []ChatMcpServer{{Name: "w", SecretRef: "wiki"}}}); err != nil {
		t.Fatal(err)
	}
	list := m.ListMcpSecrets()
	if len(list) != 2 || list[1].Name != "wiki" || !list[1].Set || list[1].UsedBy[0] != "support" || len(list[0].UsedBy) != 0 {
		t.Errorf("list: %+v", list)
	}
	if usedBy, err := m.DeleteMcpSecret("wiki"); !errors.Is(err, errChatMcpSecretInUse) || usedBy[0] != "support" {
		t.Errorf("delete in use: %v %v", usedBy, err)
	}
	if _, err := m.DeleteMcpSecret("ghost"); !errors.Is(err, errChatMcpSecretNotFound) {
		t.Errorf("delete missing: %v", err)
	}
	if _, err := m.DeleteMcpSecret("api"); err != nil || m.HasMcpSecret("api") {
		t.Errorf("delete: %v", err)
	}

	// The secrets survive a reload.
	again := NewChatScenarioManager(db)
	if err := again.LoadAll(); err != nil || !again.HasMcpSecret("wiki") || again.HasMcpSecret("api") {
		t.Errorf("reload: %v", err)
	}

	// A store that cannot write keeps nothing.
	_ = db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(bucketChatMcpSecrets) })
	if _, err := m.PutMcpSecret("x", "", []byte("v")); err == nil || m.HasMcpSecret("x") {
		t.Error("a put without the bucket succeeded")
	}
	m.m = map[string]*ChatScenario{}
	if _, err := m.DeleteMcpSecret("wiki"); err == nil {
		t.Error("a delete without the bucket succeeded")
	}
}

// With an at-rest key the value is sealed on disk; without the key it
// cannot be opened, and the error says so without the value.
func TestChatMcpSecretSealedAtRest(t *testing.T) {
	chatMcpKey(t)
	db := openChatDB(t, filepath.Join(t.TempDir(), "chat.db"))
	m := NewChatScenarioManager(db)
	if err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PutMcpSecret("wiki", "", []byte("very-secret-token")); err != nil {
		t.Fatal(err)
	}
	_ = db.View(func(tx *bolt.Tx) error {
		if raw := tx.Bucket(bucketChatMcpSecrets).Get([]byte("wiki")); bytes.Contains(raw, []byte("very-secret-token")) ||
			strings.Contains(string(raw), "dmVyeS1zZWNyZXQtdG9rZW4") {
			t.Errorf("the value is on disk in the clear: %s", raw)
		}
		return nil
	})
	if _, value, err := m.McpSecretHeader("wiki"); err != nil || value != "very-secret-token" {
		t.Errorf("open: %q %v", value, err)
	}
	SetGlobalEncryptor(nil)
	if _, _, err := m.McpSecretHeader("wiki"); err == nil || strings.Contains(err.Error(), "very-secret") {
		t.Errorf("open without the key: %v", err)
	}
}

func TestChatMcpSecretsReplicate(t *testing.T) {
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	s.ChatScenarios = NewChatScenarioManager(s.DB)
	if err := s.ChatScenarios.LoadAll(); err != nil {
		t.Fatal(err)
	}
	chatMcpKey(t)
	sealed, err := globalEncryptor.EncryptAlways([]byte("tok"))
	if err != nil {
		t.Fatal(err)
	}
	val, _ := json.Marshal(&chatMcpSecret{Name: "wiki", Value: sealed})
	ra := NewReplicationApplier(s)
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogPut, BucketName: string(bucketChatMcpSecrets), Key: []byte("wiki"), Value: val})
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 2, Type: binlog.BinlogPut, BucketName: string(bucketChatMcpSecrets), Key: []byte("broken"), Value: []byte(`{`)})
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 3, Type: binlog.BinlogPut, BucketName: string(bucketChatMcpSecrets), Key: []byte("plain"),
		Value: []byte(`{"name":"plain","value":"dG9r"}`)})
	if _, value, err := s.ChatScenarios.McpSecretHeader("wiki"); err != nil || value != "tok" || s.ChatScenarios.HasMcpSecret("broken") {
		t.Errorf("after replication: %q %v", value, err)
	}
	if _, _, err := s.ChatScenarios.McpSecretHeader("plain"); !errors.Is(err, errChatMcpSecretUnsealed) {
		t.Errorf("a replicated plain value: %v", err)
	}
}
