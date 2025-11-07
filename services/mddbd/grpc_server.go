package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	proto "mddb/proto"
)

// GRPCServer implements the MDDB gRPC service
type GRPCServer struct {
	proto.UnimplementedMDDBServer
	server                  *Server
	batchProcessor          *BatchProcessor
	optimizedBatchProcessor *OptimizedBatchProcessor
	batchDeleter            *BatchDeleter
	batchUpdater            *BatchUpdater
	workerPool              *WorkerPool
}

// NewGRPCServer creates a new gRPC server wrapper
func NewGRPCServer(s *Server) *GRPCServer {
	gs := &GRPCServer{
		server:                  s,
		batchProcessor:          NewBatchProcessor(s, 8),          // 8 parallel workers (legacy)
		optimizedBatchProcessor: NewOptimizedBatchProcessor(s, 8), // 8 parallel workers (optimized)
		batchDeleter:            NewBatchDeleter(s, 8),            // 8 parallel workers
		batchUpdater:            NewBatchUpdater(s, 8),            // 8 parallel workers
	}
	// Worker pool will be initialized when needed
	return gs
}

// startGRPCServer starts the gRPC server on the specified address
func startGRPCServer(s *Server, addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(10 * 1024 * 1024), // 10MB
		grpc.MaxSendMsgSize(10 * 1024 * 1024), // 10MB
	)

	proto.RegisterMDDBServer(grpcServer, NewGRPCServer(s))
	
	// Register reflection service for grpcurl
	reflection.Register(grpcServer)

	return grpcServer.Serve(lis)
}

// Add implements the Add RPC
func (g *GRPCServer) Add(ctx context.Context, req *proto.AddRequest) (*proto.Document, error) {
	if g.server.Mode == ModeRead {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields")
	}

	// Convert proto meta to internal format
	meta := make(map[string][]string)
	for k, v := range req.Meta {
		meta[k] = v.Values
	}

	now := time.Now().Unix()
	docID := genID(req.Collection, req.Key, req.Lang)

	// Use KeyBuilder for efficient key construction
	var kb KeyBuilder

	var saved Doc
	var cachedBuf []byte
	err := g.server.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(g.server.BucketNames.Docs)
		bRev := tx.Bucket(g.server.BucketNames.Rev)
		bByK := tx.Bucket(g.server.BucketNames.ByKey)

		// Load existing
		existing := Doc{}
		docKey := kb.BuildDocKey(req.Collection, docID)
		if v := bDocs.Get(docKey); v != nil {
			existingDoc, err := unmarshalDoc(v)
			if err != nil {
				return err
			}
			existing = *existingDoc
		}
		added := existing.AddedAt
		if added == 0 {
			added = now
		}

		doc := Doc{
			ID: docID, Key: req.Key, Lang: req.Lang, Meta: meta,
			ContentMD: req.ContentMd, AddedAt: added, UpdatedAt: now,
		}
		buf, err := marshalDoc(&doc)
		if err != nil {
			return err
		}
		cachedBuf = buf // Save for cache
		
		// Use KeyBuilder for all keys
		docKey = kb.BuildDocKey(req.Collection, docID)
		if err := bDocs.Put(docKey, buf); err != nil {
			return err
		}
		
		byKeyKey := kb.BuildByKey(req.Collection, req.Key, req.Lang)
		if err := bByK.Put(byKeyKey, []byte(docID)); err != nil {
			return err
		}

		// Lazy metadata indexing - queue for async processing
		if metadataChanged(existing.Meta, doc.Meta) {
			g.server.IndexQueue.Enqueue(&IndexJob{
				Collection: req.Collection,
				DocID:      docID,
				OldMeta:    existing.Meta,
				NewMeta:    doc.Meta,
			})
		}

		// Revision (optional - only if requested)
		if req.SaveRevision {
			revKey := kb.BuildRevKey(req.Collection, doc.ID, now)
			if err := bRev.Put(revKey, buf); err != nil {
				return err
			}
		}

		saved = doc
		return nil
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Update cache (use lock-free cache if extreme mode)
	cacheKey := BuildCacheKey(req.Collection, req.Key, req.Lang)
	if g.server.UseExtreme {
		g.server.LockFreeCache.Set(cacheKey, cachedBuf)
	} else {
		g.server.Cache.Set(cacheKey, cachedBuf)
	}

	return docToProto(&saved), nil
}

// AddBatch implements the AddBatch RPC - adds multiple documents in a single transaction
// Uses parallel processing for preparation, then single transaction for commit
func (g *GRPCServer) AddBatch(ctx context.Context, req *proto.AddBatchRequest) (*proto.AddBatchResponse, error) {
	if g.server.Mode == ModeRead {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	if len(req.Documents) == 0 {
		return &proto.AddBatchResponse{}, nil
	}

	// Use optimized batch processor if extreme mode, otherwise legacy
	var resp *proto.AddBatchResponse
	var err error
	
	if g.server.UseExtreme {
		resp, err = g.optimizedBatchProcessor.ProcessBatch(ctx, req.Collection, req.Documents)
	} else {
		resp, err = g.batchProcessor.ProcessBatch(ctx, req.Collection, req.Documents)
	}
	
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return resp, nil
}

// Get implements the Get RPC
func (g *GRPCServer) Get(ctx context.Context, req *proto.GetRequest) (*proto.Document, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields")
	}

	// Check cache first (use lock-free cache if extreme mode)
	cacheKey := BuildCacheKey(req.Collection, req.Key, req.Lang)
	
	var cachedData []byte
	var found bool
	
	if g.server.UseExtreme {
		cachedData, found = g.server.LockFreeCache.Get(cacheKey)
	} else {
		cachedData, found = g.server.Cache.Get(cacheKey)
	}
	
	if found {
		docPtr, err := unmarshalDoc(cachedData)
		if err == nil {
			// Apply template variables if needed
			if len(req.Env) > 0 {
				docPtr.ContentMD = applyEnv(docPtr.ContentMD, req.Env)
			}
			return docToProto(docPtr), nil
		}
	}

	var doc Doc
	var docData []byte
	err := g.server.DB.View(func(tx *bolt.Tx) error {
		bByK := tx.Bucket(g.server.BucketNames.ByKey)
		bDocs := tx.Bucket(g.server.BucketNames.Docs)

		docID := bByK.Get(kByKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errors.New("not found")
		}

		v := bDocs.Get(kDoc(req.Collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}

		docData = make([]byte, len(v))
		copy(docData, v)

		docPtr, err := unmarshalDoc(v)
		if err != nil {
			return err
		}
		doc = *docPtr
		return nil
	})
	
	// Update cache (use lock-free cache if extreme mode)
	if err == nil && docData != nil {
		if g.server.UseExtreme {
			g.server.LockFreeCache.Set(cacheKey, docData)
		} else {
			g.server.Cache.Set(cacheKey, docData)
		}
	}

	if err != nil {
		if err.Error() == "not found" {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Apply template variables
	if len(req.Env) > 0 {
		doc.ContentMD = applyEnv(doc.ContentMD, req.Env)
	}

	return docToProto(&doc), nil
}

// Search implements the Search RPC
func (g *GRPCServer) Search(ctx context.Context, req *proto.SearchRequest) (*proto.SearchResponse, error) {
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	// Convert proto filter to internal format
	filterMeta := make(map[string][]string)
	for k, v := range req.FilterMeta {
		filterMeta[k] = v.Values
	}

	// Single transaction for both ID collection and document loading
	var docs []Doc
	err := g.server.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(g.server.BucketNames.IdxMeta)
		bDocs := tx.Bucket(g.server.BucketNames.Docs)

		var docIDs []string
		
		if len(filterMeta) == 0 {
			// No filter: scan all docs
			c := bDocs.Cursor()
			prefix := []byte("doc|" + req.Collection + "|")
			for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
				parts := strings.Split(string(k), "|")
				if len(parts) >= 3 {
					docIDs = append(docIDs, parts[2])
				}
			}
		} else {
			// Filter by meta
			sets := [][]string{}
			for mk, mvs := range filterMeta {
				union := []string{}
				for _, mv := range mvs {
					c := bIdx.Cursor()
					prefix := kMetaKeyPrefix(req.Collection, mk, mv)
					for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
						parts := strings.Split(string(k), "|")
						if len(parts) >= 5 {
							union = append(union, parts[4])
						}
					}
				}
				sets = append(sets, unique(union))
			}
			docIDs = intersect(sets...)
		}
		
		// Load documents in the same transaction
		for _, id := range docIDs {
			v := bDocs.Get(kDoc(req.Collection, id))
			if v != nil {
				d, err := unmarshalDoc(v)
				if err == nil {
					docs = append(docs, *d)
				}
			}
		}
		
		return nil
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Sort
	sortField := req.Sort
	if sortField == "" {
		sortField = "updatedAt"
	}
	sortDocs(docs, sortField, req.Asc)

	// Pagination
	total := len(docs)
	offset := int(req.Offset)
	limit := int(req.Limit)
	if limit == 0 {
		limit = 50
	}

	if offset > len(docs) {
		offset = len(docs)
	}
	end := offset + limit
	if end > len(docs) {
		end = len(docs)
	}
	docs = docs[offset:end]

	// Convert to proto
	protoDocs := make([]*proto.Document, len(docs))
	for i, doc := range docs {
		protoDocs[i] = docToProto(&doc)
	}

	return &proto.SearchResponse{
		Documents: protoDocs,
		Total:     int32(total),
	}, nil
}

// Export implements the Export RPC (streaming)
func (g *GRPCServer) Export(req *proto.ExportRequest, stream proto.MDDB_ExportServer) error {
	// Similar to HTTP export but streaming chunks
	return status.Error(codes.Unimplemented, "export streaming not yet implemented")
}

// Backup implements the Backup RPC
func (g *GRPCServer) Backup(ctx context.Context, req *proto.BackupRequest) (*proto.BackupResponse, error) {
	filename := req.To
	if filename == "" {
		filename = fmt.Sprintf("backup-%d.db", time.Now().Unix())
	}

	err := g.server.DB.View(func(tx *bolt.Tx) error {
		return tx.CopyFile(filename, 0600)
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &proto.BackupResponse{Backup: filename}, nil
}

// Restore implements the Restore RPC
func (g *GRPCServer) Restore(ctx context.Context, req *proto.RestoreRequest) (*proto.RestoreResponse, error) {
	if g.server.Mode == ModeRead {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.From == "" {
		return nil, status.Error(codes.InvalidArgument, "missing backup filename")
	}

	if err := copyFile(req.From, g.server.Path); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &proto.RestoreResponse{Restored: req.From}, nil
}

// Truncate implements the Truncate RPC
func (g *GRPCServer) Truncate(ctx context.Context, req *proto.TruncateRequest) (*proto.TruncateResponse, error) {
	if g.server.Mode == ModeRead {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	err := g.server.DB.Update(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(g.server.BucketNames.Rev)
		bDocs := tx.Bucket(g.server.BucketNames.Docs)

		// Get all doc IDs in collection
		var docIDs []string
		c := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
			parts := strings.Split(string(k), "|")
			if len(parts) >= 3 {
				docIDs = append(docIDs, parts[2])
			}
		}

		// For each doc, keep only last N revisions
		for _, docID := range docIDs {
			var revKeys []string
			rc := bRev.Cursor()
			rprefix := kRevPrefix(req.Collection, docID)
			for k, _ := rc.Seek(rprefix); k != nil && strings.HasPrefix(string(k), string(rprefix)); k, _ = rc.Next() {
				revKeys = append(revKeys, string(k))
			}

			// Delete old revisions
			if len(revKeys) > int(req.KeepRevs) {
				toDelete := revKeys[:len(revKeys)-int(req.KeepRevs)]
				for _, k := range toDelete {
					_ = bRev.Delete([]byte(k))
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &proto.TruncateResponse{Status: "truncated"}, nil
}

// Stats implements the Stats RPC
func (g *GRPCServer) Stats(ctx context.Context, req *proto.StatsRequest) (*proto.StatsResponse, error) {
	resp := &proto.StatsResponse{
		DatabasePath: g.server.Path,
		Mode:         string(g.server.Mode),
		Collections:  []*proto.CollectionStats{},
	}

	// Get database file size
	if info, err := os.Stat(g.server.Path); err == nil {
		resp.DatabaseSize = info.Size()
	}

	// Collect statistics
	collectionMap := make(map[string]*proto.CollectionStats)

	err := g.server.DB.View(func(tx *bolt.Tx) error {
		// Count documents
		bDocs := tx.Bucket(g.server.BucketNames.Docs)
		if bDocs != nil {
			c := bDocs.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &proto.CollectionStats{Name: coll}
					}
					collectionMap[coll].DocumentCount++
					resp.TotalDocuments++
				}
			}
		}

		// Count revisions
		bRev := tx.Bucket(g.server.BucketNames.Rev)
		if bRev != nil {
			c := bRev.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &proto.CollectionStats{Name: coll}
					}
					collectionMap[coll].RevisionCount++
					resp.TotalRevisions++
				}
			}
		}

		// Count meta indices
		bIdx := tx.Bucket(g.server.BucketNames.IdxMeta)
		if bIdx != nil {
			c := bIdx.Cursor()
			for k, _ := c.First(); k != nil; k, _ = c.Next() {
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &proto.CollectionStats{Name: coll}
					}
					collectionMap[coll].MetaIndexCount++
					resp.TotalMetaIndices++
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Convert map to slice
	for _, cs := range collectionMap {
		resp.Collections = append(resp.Collections, cs)
	}

	return resp, nil
}

// Helper: convert internal Doc to proto Document
func docToProto(doc *Doc) *proto.Document {
	protoMeta := make(map[string]*proto.MetaValues)
	for k, v := range doc.Meta {
		protoMeta[k] = &proto.MetaValues{Values: v}
	}

	return &proto.Document{
		Id:        doc.ID,
		Key:       doc.Key,
		Lang:      doc.Lang,
		Meta:      protoMeta,
		ContentMd: doc.ContentMD,
		AddedAt:   doc.AddedAt,
		UpdatedAt: doc.UpdatedAt,
	}
}

// DeleteBatch implements the DeleteBatch RPC - deletes multiple documents in a single transaction
func (g *GRPCServer) DeleteBatch(ctx context.Context, req *proto.DeleteBatchRequest) (*proto.DeleteBatchResponse, error) {
	if g.server.Mode == ModeRead {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	resp, err := g.batchDeleter.ProcessBatchDelete(ctx, req.Collection, req.Documents)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return resp, nil
}

// UpdateBatch implements the UpdateBatch RPC - updates multiple documents in a single transaction
func (g *GRPCServer) UpdateBatch(ctx context.Context, req *proto.UpdateBatchRequest) (*proto.UpdateBatchResponse, error) {
	if g.server.Mode == ModeRead {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	resp, err := g.batchUpdater.ProcessBatchUpdate(ctx, req.Collection, req.Documents)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return resp, nil
}
