package main

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"mddb/internal/binlog"
	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// Default forge tokens (INT-023): host → the stored secret a git or issues
// sync on that host reads with when neither its rule nor its collection's
// repository names one, so one token for github.com serves every repository
// there.
//
// The map is part of the stored secrets, not a store of its own: it lives in
// their bucket under forgeTokensKey — a key no secret can have, since a name
// may not hold '|' — so backups carry it, the binlog replicates it, and the
// reload after a restore or a snapshot reads it with the secrets. A secret a
// default names cannot be deleted ("default:<host>" in usedBy).
//
//	GET /v1/forge-tokens   {"defaults": {"github.com": "github-token"}} (admin)
//	PUT /v1/forge-tokens   the same body; replaces the whole map (admin)

// forgeTokensPath is the route of the defaults.
const forgeTokensPath = "/v1/forge-tokens" // #nosec G101 -- a route, not a credential

// forgeTokensKey is where the map is stored in bucketChatMcpSecrets.
var forgeTokensKey = []byte("|forgeTokens")

// forgeHost is a host a default may be registered for: a DNS name, lower
// case, with an optional port — what a repository URL's host is compared
// with, exactly.
var forgeHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)

// maxForgeTokenDefaults bounds the map.
const maxForgeTokenDefaults = 100

// forgeTokensBody is the body of both calls.
type forgeTokensBody struct {
	Defaults map[string]string `json:"defaults"`
}

// ForgeTokenDefault is the default secret of host; "" for none. Safe on a
// nil manager.
func (c *ChatScenarioManager) ForgeTokenDefault(host string) string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.forgeTokens[host]
}

// ForgeTokenDefaults is a copy of the map.
func (c *ChatScenarioManager) ForgeTokenDefaults() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.forgeTokens))
	for h, ref := range c.forgeTokens {
		out[h] = ref
	}
	return out
}

// SetForgeTokenDefaults validates and stores the whole map: hosts a
// repository URL can have, secrets that exist.
func (c *ChatScenarioManager) SetForgeTokenDefaults(defaults map[string]string) error {
	if len(defaults) > maxForgeTokenDefaults {
		return fmt.Errorf("at most %d default forge tokens", maxForgeTokenDefaults)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clean := make(map[string]string, len(defaults))
	for host, ref := range defaults {
		if !forgeHost.MatchString(host) {
			return fmt.Errorf("forge token host %q: a lower-case host name, with an optional port, and nothing else", host)
		}
		if _, ok := c.mcpSecrets[ref]; !ok {
			return fmt.Errorf("forge token for %s: secret %q not found: store it first (PUT /v1/chat/mcp-secrets/%s)", host, ref, ref)
		}
		clean[host] = ref
	}
	val, _ := json.Marshal(clean) // a map of strings: it encodes
	if err := c.write(bucketChatMcpSecrets, func(b *bolt.Bucket) error { return b.Put(forgeTokensKey, val) },
		&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: string(bucketChatMcpSecrets), Key: forgeTokensKey, Value: val}); err != nil {
		return err
	}
	c.forgeTokens = clean
	return nil
}

// loadForgeTokens reads the stored map; a corrupt one is none.
func loadForgeTokens(b *bolt.Bucket) map[string]string {
	m := map[string]string{}
	if v := b.Get(forgeTokensKey); v != nil && json.Unmarshal(v, &m) != nil {
		m = map[string]string{}
	}
	return m
}

// forgeTokensUsing names the hosts whose default is the secret, as
// "default:<host>". Under the lock.
func (c *ChatScenarioManager) forgeTokensUsing(name string) []string {
	var out []string
	for host, ref := range c.forgeTokens {
		if ref == name {
			out = append(out, "default:"+host)
		}
	}
	sort.Strings(out)
	return out
}

// handleForgeTokens serves GET and PUT /v1/forge-tokens.
func (s *Server) handleForgeTokens(w http.ResponseWriter, r *http.Request) {
	if s.ChatScenarios == nil {
		writeJSONError(w, http.StatusServiceUnavailable, map[string]any{"error": errChatScenariosUnavailable.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		ok(w, forgeTokensBody{Defaults: s.ChatScenarios.ForgeTokenDefaults()})
	case http.MethodPut:
		s.guardWrite(s.putForgeTokens)(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// putForgeTokens replaces the map.
func (s *Server) putForgeTokens(w http.ResponseWriter, r *http.Request) {
	var body forgeTokensBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		bad(w, errors.New(`the body must be {"defaults": {"<host>": "<secret>"}}`))
		return
	}
	defaults := make(map[string]string, len(body.Defaults))
	for host, ref := range body.Defaults {
		defaults[strings.ToLower(strings.TrimSpace(host))] = strings.TrimSpace(ref)
	}
	if err := s.ChatScenarios.SetForgeTokenDefaults(defaults); err != nil {
		bad(w, err)
		return
	}
	ok(w, forgeTokensBody{Defaults: s.ChatScenarios.ForgeTokenDefaults()})
}
