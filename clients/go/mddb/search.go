package mddb

import (
	"context"
	"encoding/json"
)

// FTS runs a full-text search. The response shape depends on the configured
// ranking/highlight options, so it is returned as raw JSON for the caller to
// decode into the shape it needs.
func (c *Client) FTS(ctx context.Context, req FTSRequest) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/fts", req)
}

// VectorSearch runs a semantic (embedding) search. Returns raw JSON.
func (c *Client) VectorSearch(ctx context.Context, req VectorSearchRequest) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/vector-search", req)
}

// VectorStats returns embedding/index statistics as raw JSON.
func (c *Client) VectorStats(ctx context.Context) (json.RawMessage, error) {
	return c.Do(ctx, "GET", "/v1/vector-stats", nil)
}

// VectorReindex rebuilds the vector index for a collection (the server
// requires one). Returns raw JSON; ReindexVectors is the typed form with Force.
func (c *Client) VectorReindex(ctx context.Context, collection string) (json.RawMessage, error) {
	return c.Do(ctx, "POST", "/v1/vector-reindex", map[string]string{"collection": collection})
}

// SearchFTS runs a full-text search and decodes the response. It is the typed
// counterpart of FTS.
func (c *Client) SearchFTS(ctx context.Context, req FTSRequest) (*FTSResponse, error) {
	var out FTSResponse
	if err := c.doJSON(ctx, "POST", "/v1/fts", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchVectors runs a semantic search and decodes the response. It is the
// typed counterpart of VectorSearch.
func (c *Client) SearchVectors(ctx context.Context, req VectorSearchRequest) (*VectorSearchResponse, error) {
	var out VectorSearchResponse
	if err := c.doJSON(ctx, "POST", "/v1/vector-search", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchHybrid runs a combined full-text and vector search.
func (c *Client) SearchHybrid(ctx context.Context, req HybridSearchRequest) (*HybridSearchResponse, error) {
	var out HybridSearchResponse
	if err := c.doJSON(ctx, "POST", "/v1/hybrid-search", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetVectorStats returns embedding coverage per collection. It is the typed
// counterpart of VectorStats.
func (c *Client) GetVectorStats(ctx context.Context) (*VectorStats, error) {
	var out VectorStats
	if err := c.doJSON(ctx, "GET", "/v1/vector-stats", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReindexVectors re-embeds a collection and waits for the result. It is the
// typed counterpart of VectorReindex and adds Force. On a large collection
// this outlasts DefaultTimeout; pass a context with a longer deadline.
func (c *Client) ReindexVectors(ctx context.Context, req VectorReindexRequest) (*VectorReindexResponse, error) {
	var out VectorReindexResponse
	if err := c.doJSON(ctx, "POST", "/v1/vector-reindex", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CodeGraph walks the code connection graph (imports, calls, selectors)
// from one document. A missing document is an *APIError with status 404.
func (c *Client) CodeGraph(ctx context.Context, req CodeGraphRequest) (*CodeGraph, error) {
	var out CodeGraph
	if err := c.doJSON(ctx, "POST", "/v1/code-graph", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
