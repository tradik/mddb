package mddb

import (
	"context"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "mddb/proto"
)

// GRPCClient implements Client via gRPC/Protobuf API.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client pb.MDDBClient
}

// NewGRPCClient creates new gRPC client.
func NewGRPCClient(address string, timeout time.Duration) (*GRPCClient, error) {
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &GRPCClient{
		conn:   conn,
		client: pb.NewMDDBClient(conn),
	}, nil
}

func (c *GRPCClient) Health(ctx context.Context) (*Health, error) {
	// gRPC doesn't have dedicated health check in proto, use Stats as proxy
	stats, err := c.Stats(ctx)
	if err != nil {
		return nil, err
	}
	return &Health{Status: "healthy", Mode: stats.Mode}, nil
}

func (c *GRPCClient) Stats(ctx context.Context) (*Stats, error) {
	resp, err := c.client.Stats(ctx, &pb.StatsRequest{})
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}

	collections := make([]CollectionStats, len(resp.Collections))
	for i, col := range resp.Collections {
		collections[i] = CollectionStats{
			Name:           col.Name,
			DocumentCount:  int(col.DocumentCount),
			RevisionCount:  int(col.RevisionCount),
			MetaIndexCount: int(col.MetaIndexCount),
		}
	}

	return &Stats{
		DatabasePath:     resp.DatabasePath,
		DatabaseSize:     resp.DatabaseSize,
		Mode:             resp.Mode,
		Collections:      collections,
		TotalDocuments:   int(resp.TotalDocuments),
		TotalRevisions:   int(resp.TotalRevisions),
		TotalMetaIndices: int(resp.TotalMetaIndices),
	}, nil
}

func (c *GRPCClient) Add(ctx context.Context, req *AddRequest) (*Document, error) {
	pbReq := &pb.AddRequest{
		Collection:   req.Collection,
		Key:          req.Key,
		Lang:         req.Lang,
		Meta:         convertMetaToProto(req.Meta),
		ContentMd:    req.ContentMD,
		SaveRevision: req.SaveRevision,
	}

	doc, err := c.client.Add(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("add: %w", err)
	}

	return convertDocumentFromProto(doc), nil
}

func (c *GRPCClient) AddBatch(ctx context.Context, req *AddBatchRequest) (*AddBatchResponse, error) {
	docs := make([]*pb.BatchDocument, len(req.Documents))
	for i, d := range req.Documents {
		docs[i] = &pb.BatchDocument{
			Key:          d.Key,
			Lang:         d.Lang,
			Meta:         convertMetaToProto(d.Meta),
			ContentMd:    d.ContentMD,
			SaveRevision: d.SaveRevision,
		}
	}

	pbReq := &pb.AddBatchRequest{
		Collection: req.Collection,
		Documents:  docs,
	}

	resp, err := c.client.AddBatch(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("add batch: %w", err)
	}

	return &AddBatchResponse{
		Added:   int(resp.Added),
		Updated: int(resp.Updated),
		Failed:  int(resp.Failed),
		Errors:  resp.Errors,
	}, nil
}

func (c *GRPCClient) UpdateBatch(ctx context.Context, req *UpdateBatchRequest) (*UpdateBatchResponse, error) {
	docs := make([]*pb.UpdateDocument, len(req.Documents))
	for i, d := range req.Documents {
		docs[i] = &pb.UpdateDocument{
			Key:          d.Key,
			Lang:         d.Lang,
			Meta:         convertMetaToProto(d.Meta),
			ContentMd:    d.ContentMD,
			SaveRevision: d.SaveRevision,
		}
	}

	pbReq := &pb.UpdateBatchRequest{
		Collection: req.Collection,
		Documents:  docs,
	}

	resp, err := c.client.UpdateBatch(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("update batch: %w", err)
	}

	return &UpdateBatchResponse{
		Updated:  int(resp.Updated),
		NotFound: int(resp.NotFound),
		Failed:   int(resp.Failed),
		Errors:   resp.Errors,
	}, nil
}

func (c *GRPCClient) DeleteBatch(ctx context.Context, req *DeleteBatchRequest) (*DeleteBatchResponse, error) {
	docs := make([]*pb.DeleteDocument, len(req.Documents))
	for i, d := range req.Documents {
		docs[i] = &pb.DeleteDocument{
			Key:  d.Key,
			Lang: d.Lang,
		}
	}

	pbReq := &pb.DeleteBatchRequest{
		Collection: req.Collection,
		Documents:  docs,
	}

	resp, err := c.client.DeleteBatch(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("delete batch: %w", err)
	}

	return &DeleteBatchResponse{
		Deleted:  int(resp.Deleted),
		NotFound: int(resp.NotFound),
		Failed:   int(resp.Failed),
		Errors:   resp.Errors,
	}, nil
}

func (c *GRPCClient) Get(ctx context.Context, req *GetRequest) (*Document, error) {
	pbReq := &pb.GetRequest{
		Collection: req.Collection,
		Key:        req.Key,
		Lang:       req.Lang,
		Env:        req.Env,
	}

	doc, err := c.client.Get(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("get: %w", err)
	}

	return convertDocumentFromProto(doc), nil
}

func (c *GRPCClient) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	pbReq := &pb.SearchRequest{
		Collection: req.Collection,
		FilterMeta: convertMetaToProto(req.FilterMeta),
		Sort:       req.Sort,
		Asc:        req.Asc,
		Limit:      int32(req.Limit),
		Offset:     int32(req.Offset),
	}

	resp, err := c.client.Search(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	docs := make([]Document, len(resp.Documents))
	for i, d := range resp.Documents {
		docs[i] = *convertDocumentFromProto(d)
	}

	return &SearchResponse{
		Documents: docs,
		Total:     int(resp.Total),
	}, nil
}

func (c *GRPCClient) Delete(ctx context.Context, req *DeleteRequest) error {
	// gRPC uses DeleteBatch for single deletions
	_, err := c.DeleteBatch(ctx, &DeleteBatchRequest{
		Collection: req.Collection,
		Documents: []DeleteDocument{
			{Key: req.Key, Lang: req.Lang},
		},
	})
	return err
}

func (c *GRPCClient) DeleteCollection(ctx context.Context, req *DeleteCollectionRequest) (*DeleteCollectionResponse, error) {
	// gRPC doesn't have dedicated DeleteCollection, use DeleteBatch after Search
	searchResp, err := c.Search(ctx, &SearchRequest{
		Collection: req.Collection,
		Limit:      10000,
	})
	if err != nil {
		return nil, fmt.Errorf("search for delete collection: %w", err)
	}

	if len(searchResp.Documents) == 0 {
		return &DeleteCollectionResponse{Deleted: 0}, nil
	}

	docs := make([]DeleteDocument, len(searchResp.Documents))
	for i, d := range searchResp.Documents {
		docs[i] = DeleteDocument{Key: d.Key, Lang: d.Lang}
	}

	delResp, err := c.DeleteBatch(ctx, &DeleteBatchRequest{
		Collection: req.Collection,
		Documents:  docs,
	})
	if err != nil {
		return nil, fmt.Errorf("delete batch: %w", err)
	}

	return &DeleteCollectionResponse{Deleted: delResp.Deleted}, nil
}

func (c *GRPCClient) Export(ctx context.Context, req *ExportRequest) (io.ReadCloser, error) {
	pbReq := &pb.ExportRequest{
		Collection: req.Collection,
		FilterMeta: convertMetaToProto(req.FilterMeta),
		Format:     req.Format,
	}

	stream, err := c.client.Export(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("export: %w", err)
	}

	pr, pw := io.Pipe()

	go func() {
		defer func() {
			_ = pw.Close()
		}()
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				_ = pw.CloseWithError(fmt.Errorf("recv chunk: %w", err))
				return
			}
			if _, err := pw.Write(chunk.Data); err != nil {
				_ = pw.CloseWithError(fmt.Errorf("write chunk: %w", err))
				return
			}
		}
	}()

	return pr, nil
}

func (c *GRPCClient) Backup(ctx context.Context, req *BackupRequest) (*BackupResponse, error) {
	pbReq := &pb.BackupRequest{To: req.To}
	resp, err := c.client.Backup(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	return &BackupResponse{Backup: resp.Backup}, nil
}

func (c *GRPCClient) Restore(ctx context.Context, req *RestoreRequest) (*RestoreResponse, error) {
	pbReq := &pb.RestoreRequest{From: req.From}
	resp, err := c.client.Restore(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	return &RestoreResponse{Restored: resp.Restored}, nil
}

func (c *GRPCClient) Truncate(ctx context.Context, req *TruncateRequest) (*TruncateResponse, error) {
	pbReq := &pb.TruncateRequest{
		Collection: req.Collection,
		KeepRevs:   int32(req.KeepRevs),
		DropCache:  req.DropCache,
	}
	resp, err := c.client.Truncate(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("truncate: %w", err)
	}
	return &TruncateResponse{Status: resp.Status}, nil
}

func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

// convertMetaToProto konwertuje meta z map[string][]string na proto format.
func convertMetaToProto(meta map[string][]string) map[string]*pb.MetaValues {
	if meta == nil {
		return nil
	}
	result := make(map[string]*pb.MetaValues, len(meta))
	for k, v := range meta {
		result[k] = &pb.MetaValues{Values: v}
	}
	return result
}

// convertMetaFromProto konwertuje meta z proto na map[string][]string.
func convertMetaFromProto(meta map[string]*pb.MetaValues) map[string][]string {
	if meta == nil {
		return nil
	}
	result := make(map[string][]string, len(meta))
	for k, v := range meta {
		result[k] = v.Values
	}
	return result
}

// convertDocumentFromProto converts Document from proto to internal type.
func convertDocumentFromProto(doc *pb.Document) *Document {
	return &Document{
		ID:        doc.Id,
		Key:       doc.Key,
		Lang:      doc.Lang,
		Meta:      convertMetaFromProto(doc.Meta),
		ContentMD: doc.ContentMd,
		AddedAt:   time.Unix(doc.AddedAt, 0),
		UpdatedAt: time.Unix(doc.UpdatedAt, 0),
	}
}
