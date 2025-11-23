package mddb

import (
	"context"
	"fmt"
	"io"
	"log"
)

// TransportMode specifies transport mode.
type TransportMode string

const (
	TransportGRPCOnly             TransportMode = "grpc_only"
	TransportRESTOnly             TransportMode = "rest_only"
	TransportGRPCWithRESTFallback TransportMode = "grpc_with_rest_fallback"
	TransportRESTWithGRPCFallback TransportMode = "rest_with_grpc_fallback"
)

// FallbackClient implements Client with fallback logic between gRPC and REST.
type FallbackClient struct {
	mode      TransportMode
	primary   Client
	secondary Client
}

// NewFallbackClient creates client with fallback.
func NewFallbackClient(mode TransportMode, grpcClient, restClient Client) *FallbackClient {
	var primary, secondary Client

	switch mode {
	case TransportGRPCOnly:
		primary = grpcClient
		secondary = nil
	case TransportRESTOnly:
		primary = restClient
		secondary = nil
	case TransportGRPCWithRESTFallback:
		primary = grpcClient
		secondary = restClient
	case TransportRESTWithGRPCFallback:
		primary = restClient
		secondary = grpcClient
	default:
		log.Printf("unknown transport mode %s, using grpc_with_rest_fallback", mode)
		primary = grpcClient
		secondary = restClient
	}

	return &FallbackClient{
		mode:      mode,
		primary:   primary,
		secondary: secondary,
	}
}

func (c *FallbackClient) Health(ctx context.Context) (*Health, error) {
	h, err := c.primary.Health(ctx)
	if err != nil && c.secondary != nil {
		log.Printf("primary health failed: %v, trying secondary", err)
		return c.secondary.Health(ctx)
	}
	return h, err
}

func (c *FallbackClient) Stats(ctx context.Context) (*Stats, error) {
	s, err := c.primary.Stats(ctx)
	if err != nil && c.secondary != nil {
		log.Printf("primary stats failed: %v, trying secondary", err)
		return c.secondary.Stats(ctx)
	}
	return s, err
}

func (c *FallbackClient) Add(ctx context.Context, req *AddRequest) (*Document, error) {
	doc, err := c.primary.Add(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary add failed: %v, trying secondary", err)
		return c.secondary.Add(ctx, req)
	}
	return doc, err
}

func (c *FallbackClient) AddBatch(ctx context.Context, req *AddBatchRequest) (*AddBatchResponse, error) {
	resp, err := c.primary.AddBatch(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary add batch failed: %v, trying secondary", err)
		return c.secondary.AddBatch(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) UpdateBatch(ctx context.Context, req *UpdateBatchRequest) (*UpdateBatchResponse, error) {
	resp, err := c.primary.UpdateBatch(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary update batch failed: %v, trying secondary", err)
		return c.secondary.UpdateBatch(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) DeleteBatch(ctx context.Context, req *DeleteBatchRequest) (*DeleteBatchResponse, error) {
	resp, err := c.primary.DeleteBatch(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary delete batch failed: %v, trying secondary", err)
		return c.secondary.DeleteBatch(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) Get(ctx context.Context, req *GetRequest) (*Document, error) {
	doc, err := c.primary.Get(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary get failed: %v, trying secondary", err)
		return c.secondary.Get(ctx, req)
	}
	return doc, err
}

func (c *FallbackClient) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	resp, err := c.primary.Search(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary search failed: %v, trying secondary", err)
		return c.secondary.Search(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) Delete(ctx context.Context, req *DeleteRequest) error {
	err := c.primary.Delete(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary delete failed: %v, trying secondary", err)
		return c.secondary.Delete(ctx, req)
	}
	return err
}

func (c *FallbackClient) DeleteCollection(ctx context.Context, req *DeleteCollectionRequest) (*DeleteCollectionResponse, error) {
	resp, err := c.primary.DeleteCollection(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary delete collection failed: %v, trying secondary", err)
		return c.secondary.DeleteCollection(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) Export(ctx context.Context, req *ExportRequest) (io.ReadCloser, error) {
	r, err := c.primary.Export(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary export failed: %v, trying secondary", err)
		return c.secondary.Export(ctx, req)
	}
	return r, err
}

func (c *FallbackClient) Backup(ctx context.Context, req *BackupRequest) (*BackupResponse, error) {
	resp, err := c.primary.Backup(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary backup failed: %v, trying secondary", err)
		return c.secondary.Backup(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) Restore(ctx context.Context, req *RestoreRequest) (*RestoreResponse, error) {
	resp, err := c.primary.Restore(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary restore failed: %v, trying secondary", err)
		return c.secondary.Restore(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) Truncate(ctx context.Context, req *TruncateRequest) (*TruncateResponse, error) {
	resp, err := c.primary.Truncate(ctx, req)
	if err != nil && c.secondary != nil {
		log.Printf("primary truncate failed: %v, trying secondary", err)
		return c.secondary.Truncate(ctx, req)
	}
	return resp, err
}

func (c *FallbackClient) Close() error {
	var errs []error
	if c.primary != nil {
		if err := c.primary.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close primary: %w", err))
		}
	}
	if c.secondary != nil {
		if err := c.secondary.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close secondary: %w", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}
