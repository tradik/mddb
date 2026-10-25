package mddb

import (
	"context"
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
