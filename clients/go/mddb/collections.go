package mddb

import (
	"context"
	"encoding/json"
	"net/url"
)

// GetCollectionConfig returns a collection's stored settings, or the defaults
// with Configured=false when none are stored.
func (c *Client) GetCollectionConfig(ctx context.Context, collection string) (*CollectionConfigResponse, error) {
	var out CollectionConfigResponse
	path := "/v1/collection-config?collection=" + url.QueryEscape(collection)
	if err := c.doJSON(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetCollectionConfig replaces a collection's settings. The server stores the
// request as a whole, so fields left zero are cleared: read the current
// config with GetCollectionConfig, change it, and send all of it back.
func (c *Client) SetCollectionConfig(ctx context.Context, req SetCollectionConfigRequest) error {
	_, err := c.Do(ctx, "PUT", "/v1/collection-config", req)
	return err
}

// Storage reports where the database's bytes are, per bucket and collection.
// With collection == "" it reads the whole database, which takes minutes on
// a large file; naming a collection reads only that collection's keys.
func (c *Client) Storage(ctx context.Context, collection string) (*StorageBreakdown, error) {
	path := "/v1/storage"
	if collection != "" {
		path += "?collection=" + url.QueryEscape(collection)
	}
	var out StorageBreakdown
	if err := c.doJSON(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CodeLanguages is the server's extension → language table (AGT-001).
type CodeLanguages struct {
	Extensions map[string]string `json:"extensions"`
	Languages  []string          `json:"languages"`
}

// CodeLanguages returns the table the server classifies source files by, so
// a client walking a directory marks files the way the server would.
func (c *Client) CodeLanguages(ctx context.Context) (*CodeLanguages, error) {
	var out CodeLanguages
	if err := c.doJSON(ctx, "GET", "/v1/code-languages", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchAdvisor measures a collection and recommends how to search it; with
// apply it also stores the recommendation as the collection's retrieval
// profile. The recommendation is returned as the server sends it.
func (c *Client) SearchAdvisor(ctx context.Context, collection string, apply bool) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/search-advisor", map[string]any{"collection": collection, "apply": apply})
}

// Eval runs a collection's evaluation set (SRCH-019) under the given search
// configurations and returns recall@k, nDCG@k, MRR, latency and tokens per
// configuration, as the server sends them. req is the POST /v1/eval body.
func (c *Client) Eval(ctx context.Context, req any) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/eval", req)
}

// Gain reports what retrieval saved over a window such as "30d" (AGT-004):
// searches by type and algorithm, tokens returned and avoided. daily adds a
// per-day breakdown. The report is returned as the server sends it.
func (c *Client) Gain(ctx context.Context, window string, daily bool) (json.RawMessage, error) {
	q := url.Values{}
	if window != "" {
		q.Set("window", window)
	}
	if daily {
		q.Set("daily", "true")
	}
	path := "/v1/gain"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.Do(ctx, "GET", path, nil)
}
