package mddb

import "context"

// Add creates or updates a document and returns the stored result.
func (c *Client) Add(ctx context.Context, req AddRequest) (*Document, error) {
	var doc Document
	if err := c.doJSON(ctx, "POST", "/v1/add", req, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Get fetches a single document.
func (c *Client) Get(ctx context.Context, req GetRequest) (*Document, error) {
	var doc Document
	if err := c.doJSON(ctx, "POST", "/v1/get", req, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Search lists documents in a collection, filtered and sorted by req.
func (c *Client) Search(ctx context.Context, req SearchRequest) ([]Document, error) {
	var docs []Document
	if err := c.doJSON(ctx, "POST", "/v1/search", req, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// Delete removes a single document. A missing document is an *APIError with
// status 404; a lost conditional delete one with status 409.
func (c *Client) Delete(ctx context.Context, req DeleteRequest) error {
	return c.doJSON(ctx, "POST", "/v1/delete", req, nil)
}

// SetTTL sets or clears a document's expiry and returns the updated document.
func (c *Client) SetTTL(ctx context.Context, req SetTTLRequest) (*Document, error) {
	var doc Document
	if err := c.doJSON(ctx, "POST", "/v1/set-ttl", req, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ImportURL fetches a remote URL and stores it as a document.
func (c *Client) ImportURL(ctx context.Context, req ImportURLRequest) (*Document, error) {
	var doc Document
	if err := c.doJSON(ctx, "POST", "/v1/import-url", req, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Stats returns server-wide and per-collection statistics.
func (c *Client) Stats(ctx context.Context) (*Stats, error) {
	var s Stats
	if err := c.doJSON(ctx, "GET", "/v1/stats", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
