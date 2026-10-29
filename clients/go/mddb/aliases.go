package mddb

import (
	"context"
	"net/url"
)

// Alias is a stable name for a collection (GO-044).
type Alias struct {
	Alias      string `json:"alias"`
	Collection string `json:"collection"`
}

// Aliases lists the aliases whose collection the caller may read.
func (c *Client) Aliases(ctx context.Context) ([]Alias, error) {
	var out struct {
		Aliases []Alias `json:"aliases"`
	}
	if err := c.doJSON(ctx, "GET", "/v1/aliases", nil, &out); err != nil {
		return nil, err
	}
	return out.Aliases, nil
}

// SetAlias points alias at collection, creating it or moving it in one step.
func (c *Client) SetAlias(ctx context.Context, alias, collection string) error {
	return c.doJSON(ctx, "PUT", "/v1/aliases/"+url.PathEscape(alias), map[string]string{"collection": collection}, nil)
}

// DeleteAlias removes an alias; the collection stays.
func (c *Client) DeleteAlias(ctx context.Context, alias string) error {
	return c.doJSON(ctx, "DELETE", "/v1/aliases/"+url.PathEscape(alias), nil, nil)
}

// IndexingStatus reports queued indexing work for a collection (GO-042),
// as the server sends it.
func (c *Client) IndexingStatus(ctx context.Context, collection string) (*IndexingStatus, error) {
	var out IndexingStatus
	if err := c.doJSON(ctx, "GET", "/v1/indexing-status?collection="+url.QueryEscape(collection), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// IndexingStatus is the answer of GET /v1/indexing-status.
type IndexingStatus struct {
	Collection        string  `json:"collection"`
	PendingEmbeddings int     `json:"pendingEmbeddings"`
	FailedEmbeddings  int     `json:"failedEmbeddings"`
	PendingMetadata   int     `json:"pendingMetadata"`
	Progress          float64 `json:"progress"`
	Indexed           bool    `json:"indexed"`
}
