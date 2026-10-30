package mddb

import (
	"context"
	"encoding/json"
	"net/url"
)

// --- Schema ---

// SetSchema installs or replaces a collection's metadata schema. schema is the
// raw JSON-schema body (server-defined shape).
func (c *Client) SetSchema(ctx context.Context, body any) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/schema/set", body)
}

// GetSchema returns a collection's metadata schema as raw JSON.
func (c *Client) GetSchema(ctx context.Context, collection string) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/schema/get", SchemaRequest{Collection: collection})
}

// ListSchemas returns all collection schemas as raw JSON.
func (c *Client) ListSchemas(ctx context.Context) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/schema/list", struct{}{})
}

// DeleteSchema removes a collection's metadata schema.
func (c *Client) DeleteSchema(ctx context.Context, collection string) error {
	_, err := c.Do(ctx, "POST", "/v1/schema/delete", SchemaRequest{Collection: collection})
	return err
}

// ValidateDocument checks a document body against its collection schema.
func (c *Client) ValidateDocument(ctx context.Context, body any) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/validate", body)
}

// --- Webhooks ---

// RegisterWebhook registers a webhook subscription and returns it.
func (c *Client) RegisterWebhook(ctx context.Context, req RegisterWebhookRequest) (*Webhook, error) {
	var wh Webhook
	if err := c.doJSON(ctx, "POST", "/v1/webhooks", req, &wh); err != nil {
		return nil, err
	}
	return &wh, nil
}

// ListWebhooks returns all registered webhooks.
func (c *Client) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	var hooks []Webhook
	if err := c.doJSON(ctx, "GET", "/v1/webhooks", nil, &hooks); err != nil {
		return nil, err
	}
	return hooks, nil
}

// DeleteWebhook removes a webhook by ID.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	_, err := c.Do(ctx, "POST", "/v1/webhooks/delete", map[string]string{"id": id})
	return err
}

// --- Backup / lifecycle ---

// Export streams a collection export and returns the raw body.
func (c *Client) Export(ctx context.Context, body any) ([]byte, error) {
	return c.Do(ctx, "POST", "/v1/export", body)
}

// Restore imports documents from an export body. Returns raw JSON.
func (c *Client) Restore(ctx context.Context, body any) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/restore", body)
}

// Truncate deletes a collection's revision history. Returns raw JSON.
func (c *Client) Truncate(ctx context.Context, body any) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/truncate", body)
}

// --- Diagnose ---

// DiagnoseOptions selects the diagnose bundle's opt-in parts (OPS-025).
type DiagnoseOptions struct {
	Profiles  bool // runtime.json: goroutine count, heap and GC counters
	Integrity bool // bbolt's consistency check of every database file; slow on a large one
	KeepNames bool // keep collection names instead of hashing them
}

// Diagnose downloads the diagnose bundle, GET /v1/admin/diagnose: a tar.gz
// describing the server — build, configuration through an allowlist, stats,
// metrics, query-shape counters, recent error classes — without its data.
// MANIFEST.json, its first file, lists every file and the redaction applied.
// Admin only. With Integrity, pass a context with a long enough deadline.
func (c *Client) Diagnose(ctx context.Context, opts DiagnoseOptions) ([]byte, error) {
	q := url.Values{}
	for name, on := range map[string]bool{"profiles": opts.Profiles, "integrity": opts.Integrity, "keepNames": opts.KeepNames} {
		if on {
			q.Set(name, "true")
		}
	}
	path := "/v1/admin/diagnose"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.Do(ctx, "GET", path, nil)
}

// --- Auth ---

// Login exchanges credentials for a JWT. Returns raw JSON (token + expiry).
func (c *Client) Login(ctx context.Context, username, password string) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/auth/login", map[string]string{"username": username, "password": password})
}

// CreateAPIKey provisions a new API key. body is the server-defined request.
func (c *Client) CreateAPIKey(ctx context.Context, body any) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/auth/api-key", body)
}

// ListAPIKeys returns all API keys as raw JSON.
func (c *Client) ListAPIKeys(ctx context.Context) (json.RawMessage, error) {
	return c.Do(ctx, "GET", "/v1/auth/api-keys", nil)
}

// DeleteAPIKey revokes an API key by ID.
func (c *Client) DeleteAPIKey(ctx context.Context, id string) error {
	_, err := c.Do(ctx, "DELETE", "/v1/auth/api-keys/"+id, nil)
	return err
}

// --- GraphQL ---

// GraphQL executes a GraphQL query/mutation. Returns the raw {data,errors} body.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/graphql", map[string]any{"query": query, "variables": variables})
}
