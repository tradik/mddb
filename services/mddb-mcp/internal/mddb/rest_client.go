package mddb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// RESTClient implementuje Client przez HTTP/JSON API.
type RESTClient struct {
	baseURL string
	client  *http.Client
}

// NewRESTClient tworzy nowego klienta REST.
func NewRESTClient(baseURL string, timeout time.Duration) *RESTClient {
	return &RESTClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *RESTClient) Health(ctx context.Context) (*Health, error) {
	var h Health
	if err := c.get(ctx, "/health", &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func (c *RESTClient) Stats(ctx context.Context) (*Stats, error) {
	var s Stats
	if err := c.get(ctx, "/v1/stats", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *RESTClient) Add(ctx context.Context, req *AddRequest) (*Document, error) {
	var doc Document
	if err := c.post(ctx, "/v1/add", req, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (c *RESTClient) AddBatch(ctx context.Context, req *AddBatchRequest) (*AddBatchResponse, error) {
	var resp AddBatchResponse
	if err := c.post(ctx, "/v1/add-batch", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RESTClient) UpdateBatch(ctx context.Context, req *UpdateBatchRequest) (*UpdateBatchResponse, error) {
	var resp UpdateBatchResponse
	if err := c.post(ctx, "/v1/update-batch", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RESTClient) DeleteBatch(ctx context.Context, req *DeleteBatchRequest) (*DeleteBatchResponse, error) {
	var resp DeleteBatchResponse
	if err := c.post(ctx, "/v1/delete-batch", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RESTClient) Get(ctx context.Context, req *GetRequest) (*Document, error) {
	var doc Document
	if err := c.post(ctx, "/v1/get", req, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (c *RESTClient) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	var docs []Document
	if err := c.post(ctx, "/v1/search", req, &docs); err != nil {
		return nil, err
	}
	return &SearchResponse{Documents: docs, Total: len(docs)}, nil
}

func (c *RESTClient) Delete(ctx context.Context, req *DeleteRequest) error {
	var result map[string]interface{}
	return c.post(ctx, "/v1/delete", req, &result)
}

func (c *RESTClient) DeleteCollection(ctx context.Context, req *DeleteCollectionRequest) (*DeleteCollectionResponse, error) {
	var resp DeleteCollectionResponse
	if err := c.post(ctx, "/v1/delete-collection", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *RESTClient) Export(ctx context.Context, req *ExportRequest) (io.ReadCloser, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal export request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/export", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create export request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("export request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("export failed: status %d", resp.StatusCode)
	}

	return resp.Body, nil
}

func (c *RESTClient) Backup(ctx context.Context, req *BackupRequest) (*BackupResponse, error) {
	u := fmt.Sprintf("%s/v1/backup?to=%s", c.baseURL, url.QueryEscape(req.To))
	httpReq, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("create backup request: %w", err)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("backup request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backup failed: status %d", resp.StatusCode)
	}

	var br BackupResponse
	if err := json.NewDecoder(resp.Body).Decode(&br); err != nil {
		return nil, fmt.Errorf("decode backup response: %w", err)
	}

	return &br, nil
}

func (c *RESTClient) Restore(ctx context.Context, req *RestoreRequest) (*RestoreResponse, error) {
	var rr RestoreResponse
	if err := c.post(ctx, "/v1/restore", req, &rr); err != nil {
		return nil, err
	}
	return &rr, nil
}

func (c *RESTClient) Truncate(ctx context.Context, req *TruncateRequest) (*TruncateResponse, error) {
	var result map[string]interface{}
	if err := c.post(ctx, "/v1/truncate", req, &result); err != nil {
		return nil, err
	}
	deleted, _ := result["deleted"].(float64)
	return &TruncateResponse{Status: fmt.Sprintf("deleted %d revisions", int(deleted))}, nil
}

func (c *RESTClient) Close() error {
	return nil
}

// get wykonuje GET request.
func (c *RESTClient) get(ctx context.Context, path string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed: status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// post wykonuje POST request.
func (c *RESTClient) post(ctx context.Context, path string, body, result interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed: status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
