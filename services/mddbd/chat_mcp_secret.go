package main

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/encryption"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// Credentials of external MCP servers (MCP-005): name → a bearer token, or
// the value of a header the server reads. Write-only: no read returns a
// value — a listing shows the name, the header and set: true — and a
// scenario names one in secretRef. Stored next to the scenarios, so backups
// carry them and the binlog replicates them. A value is only ever stored
// sealed with the at-rest key (MDDB_ENCRYPTION_KEY): without the key a put is
// refused (errChatMcpSecretsNeedKey), and a stored value that is not sealed
// is refused when read (errChatMcpSecretUnsealed) rather than used.

// bucketChatMcpSecrets holds one JSON value per secret.
var bucketChatMcpSecrets = []byte("chat_mcp_secrets")

const maxChatMcpSecretBytes = 8192

var (
	errChatMcpSecretNotFound = errors.New("mcp secret not found")
	errChatMcpSecretInUse    = errors.New("mcp secret is in use")
	// errChatMcpSecretsNeedKey refuses a put on a server without an at-rest key.
	errChatMcpSecretsNeedKey = errors.New("mcp secrets are never stored in plain text and no at-rest encryption key is configured: " +
		"set MDDB_ENCRYPTION_KEY and restart mddbd")
	// errChatMcpSecretUnsealed refuses a stored value that is not sealed.
	errChatMcpSecretUnsealed = errors.New("mcp secret is stored unsealed and is not used: set it again with MDDB_ENCRYPTION_KEY configured")
	// chatMcpHeaderName is an HTTP header name a secret may set.
	chatMcpHeaderName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	// chatMcpReservedHeaders are the headers the client sets itself.
	chatMcpReservedHeaders = map[string]bool{"Host": true, "Content-Type": true, "Content-Length": true, "Accept": true,
		"Mcp-Session-Id": true, "Mcp-Protocol-Version": true, "User-Agent": true}
)

// chatMcpSecret is one secret as stored. Value is sealed (see above): it
// starts with the encryptor's version magic (MDDB_ENC_V2), which is how a
// sealed value is told from a plain one.
type chatMcpSecret struct {
	Name      string    `json:"name"`
	Header    string    `json:"header,omitempty"` // empty: Authorization: Bearer <value>
	Value     []byte    `json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// chatMcpSecretView is a secret as listed: never its value.
type chatMcpSecretView struct {
	Name      string    `json:"name"`
	Header    string    `json:"header"`
	Set       bool      `json:"set"` // a sealed value is stored; false for an unsealed one, which is refused
	UpdatedAt time.Time `json:"updatedAt"`
	UsedBy    []string  `json:"usedBy"`
}

// validChatMcpSecret checks a secret before it is sealed.
func validChatMcpSecret(name, header, value string) error {
	if err := validChatKeyName("secret", name); err != nil {
		return err
	}
	switch {
	case value == "" || len(value) > maxChatMcpSecretBytes:
		return fmt.Errorf("the secret's value must be 1 to %d bytes", maxChatMcpSecretBytes)
	case strings.ContainsAny(value, "\r\n\x00"):
		return errors.New("the secret's value may not hold a line break or NUL")
	case header != "" && (!chatMcpHeaderName.MatchString(header) || chatMcpReservedHeaders[http.CanonicalHeaderKey(header)]):
		return fmt.Errorf("header %q is not a header a secret may set", header)
	}
	return nil
}

// loadChatMcpSecrets reads every secret of the bucket, skipping a corrupt one.
func loadChatMcpSecrets(b *bolt.Bucket) map[string]*chatMcpSecret {
	m := map[string]*chatMcpSecret{}
	_ = b.ForEach(func(k, v []byte) error { // the callback never fails
		var sec chatMcpSecret
		if json.Unmarshal(v, &sec) == nil {
			m[string(k)] = &sec
		}
		return nil
	})
	return m
}

// HasMcpSecret reports whether a secret exists. Safe on a nil manager.
func (c *ChatScenarioManager) HasMcpSecret(name string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.mcpSecrets[name]
	return ok
}

// chatMcpSecretsKeyReady reports whether a secret can be sealed: an at-rest
// key is loaded.
func chatMcpSecretsKeyReady() bool { return globalEncryptor.Enabled() }

// McpSecretHeader returns the header a secret sets, opened. A value that is
// not sealed is refused, never used.
func (c *ChatScenarioManager) McpSecretHeader(name string) (http.Header, string, error) {
	c.mu.RLock()
	sec, ok := c.mcpSecrets[name]
	c.mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", errChatMcpSecretNotFound, name)
	}
	if !encryption.IsEncrypted(sec.Value) {
		return nil, "", fmt.Errorf("%w: %s", errChatMcpSecretUnsealed, name)
	}
	plain, err := maybeDecrypt(sec.Value)
	if err != nil {
		return nil, "", fmt.Errorf("secret %s cannot be opened: %w", name, err)
	}
	value := string(plain)
	if sec.Header == "" {
		return http.Header{"Authorization": {"Bearer " + value}}, value, nil
	}
	return http.Header{http.CanonicalHeaderKey(sec.Header): {value}}, value, nil
}

// ListMcpSecrets returns every secret without its value, by name.
func (c *ChatScenarioManager) ListMcpSecrets() []*chatMcpSecretView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*chatMcpSecretView, 0, len(c.mcpSecrets))
	for _, sec := range c.mcpSecrets {
		out = append(out, &chatMcpSecretView{Name: sec.Name, Header: sec.Header, Set: encryption.IsEncrypted(sec.Value),
			UpdatedAt: sec.UpdatedAt, UsedBy: c.mcpSecretUsedBy(sec.Name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// mcpSecretUsedBy names the scenarios whose servers use a secret. Under the lock.
func (c *ChatScenarioManager) mcpSecretUsedBy(name string) []string {
	out := []string{}
	for _, sc := range c.m {
		for _, m := range sc.McpServers {
			if m.SecretRef == name {
				out = append(out, sc.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// PutMcpSecret seals and stores a secret, validated by the caller; true when
// it is new. Bumps the version. Without an at-rest key it stores nothing and
// returns errChatMcpSecretsNeedKey.
func (c *ChatScenarioManager) PutMcpSecret(name, header string, value []byte) (bool, error) {
	// EncryptAlways fails only without a key (crypto/rand cannot fail since
	// Go 1.24).
	sealed, err := globalEncryptor.EncryptAlways(value)
	if err != nil {
		return false, errChatMcpSecretsNeedKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sec := &chatMcpSecret{Name: name, Header: header, Value: sealed, UpdatedAt: c.now().UTC()}
	val, _ := json.Marshal(sec) // strings, bytes and a time: it encodes
	if err := c.write(bucketChatMcpSecrets, func(b *bolt.Bucket) error { return b.Put([]byte(name), val) },
		&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketChatMcpSecrets), Key: []byte(name), Value: val}); err != nil {
		return false, err
	}
	_, existed := c.mcpSecrets[name]
	c.mcpSecrets[name] = sec
	return !existed, nil
}

// DeleteMcpSecret removes a secret no scenario uses; while one does, it
// returns errChatMcpSecretInUse with their names.
func (c *ChatScenarioManager) DeleteMcpSecret(name string) (usedBy []string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.mcpSecrets[name]; !ok {
		return nil, errChatMcpSecretNotFound
	}
	if usedBy = c.mcpSecretUsedBy(name); len(usedBy) > 0 {
		return usedBy, errChatMcpSecretInUse
	}
	if err := c.write(bucketChatMcpSecrets, func(b *bolt.Bucket) error { return b.Delete([]byte(name)) },
		&binlog.BinlogEntry{Type: binlog.BinlogDelete, BucketName: string(bucketChatMcpSecrets), Key: []byte(name)}); err != nil {
		return nil, err
	}
	delete(c.mcpSecrets, name)
	return nil, nil
}
