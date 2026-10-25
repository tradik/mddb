package mddb

import "context"

// Ingest stores many documents in one collection in a single request. Use
// IngestOptions to skip embeddings, full-text indexing or webhooks during a
// bulk load. A large batch can outlast DefaultTimeout; pass a context with a
// longer deadline.
func (c *Client) Ingest(ctx context.Context, req IngestRequest) (*IngestResponse, error) {
	var out IngestResponse
	if err := c.doJSON(ctx, "POST", "/v1/ingest", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBatch deletes many documents of one collection. Documents that do not
// exist are counted in NotFound, not reported as an error.
func (c *Client) DeleteBatch(ctx context.Context, req DeleteBatchRequest) (*DeleteBatchResponse, error) {
	var out DeleteBatchResponse
	if err := c.doJSON(ctx, "POST", "/v1/delete-batch", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Aggregate returns facet counts and date histograms over a collection.
func (c *Client) Aggregate(ctx context.Context, req AggregateRequest) (*AggregateResponse, error) {
	var out AggregateResponse
	if err := c.doJSON(ctx, "POST", "/v1/aggregate", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
