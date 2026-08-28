package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mddb/internal/cache"
	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	proto "mddb/proto"
	"os"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// GRPCServer implements the MDDB gRPC service
type GRPCServer struct {
	proto.UnimplementedMDDBServer
	server         *Server
	batchProcessor *BatchProcessor
	batchUpdater   *BatchUpdater
	batchDeleter   *BatchDeleter
}

// NewGRPCServer creates a new gRPC server wrapper
func NewGRPCServer(s *Server) *GRPCServer {
	// Use FinalBatchProcessor if extreme mode, otherwise standard
	var batchProc *BatchProcessor
	if s.UseExtreme {
		// In extreme mode, use wrapper that calls FinalBatchProcessor
		batchProc = &BatchProcessor{
			server:     s,
			maxWorkers: 8,
		}
		// Override with final processor
		finalProc := NewFinalBatchProcessor(s, 8)
		// Store final processor for use
		s.finalBatchProcessor = finalProc
	} else {
		batchProc = NewBatchProcessor(s, 8)
	}

	gs := &GRPCServer{
		server:         s,
		batchProcessor: batchProc,
		batchDeleter:   NewBatchDeleter(s, 8), // 8 parallel workers
		batchUpdater:   NewBatchUpdater(s, 8), // 8 parallel workers
	}
	// Worker pool will be initialized when needed
	return gs
}

// isReadOnly returns true if the gRPC server is in read-only mode,
// considering both the global server mode and the per-protocol gRPC mode override.
func (g *GRPCServer) isReadOnly() bool {
	mode := effectiveMode(g.server.Mode, g.server.Config.GRPC.Mode)
	return mode == ModeRead
}

// startGRPCServer starts the gRPC server on the specified address.
// addr may be a TCP host:port or a Unix Domain Socket (unix:/path/to/sock) —
// see openListener in listen_addr.go.
func startGRPCServer(s *Server, addr string, opts ...grpc.ServerOption) error {
	lis, err := openListener(addr)
	if err != nil {
		return err
	}
	defer func() { _ = closeListener(lis, addr) }()

	// Default options
	defaultOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(10 * 1024 * 1024), // 10MB
		grpc.MaxSendMsgSize(10 * 1024 * 1024), // 10MB
	}

	// Merge with provided options
	allOpts := append(defaultOpts, opts...)

	grpcServer := grpc.NewServer(allOpts...)

	proto.RegisterMDDBServer(grpcServer, NewGRPCServer(s))

	// Register replication service (available on all nodes for status queries)
	if s.ReplicationRole == "leader" || s.ReplicationRole == "follower" {
		rs := NewReplicationServer(s)
		s.replServer = rs
		proto.RegisterMDDBReplicationServer(grpcServer, rs)

		// SEC-001: snapshot/binlog streams expose the entire DB (incl. auth
		// hashes). Warn loudly if the leader exposes them with no auth, secret,
		// or mTLS — authorizeReplication will refuse such calls at runtime.
		hasSecret := os.Getenv("MDDB_REPLICATION_SECRET") != ""
		authOn := os.Getenv("MDDB_AUTH_ENABLED") == "true"
		hasMTLS := s.Config.TLS.ClientCAFile != ""
		if s.ReplicationRole == "leader" && !hasSecret && !authOn && !hasMTLS {
			slog.Warn("replication leader has no auth, MDDB_REPLICATION_SECRET, or mTLS. "+
				"Snapshot/binlog requests will be refused. Set MDDB_REPLICATION_SECRET, enable "+
				"MDDB_AUTH_ENABLED, or configure mTLS (TLS client CA) before followers can sync.",
				"finding", "SEC-001")
		}
	}

	// Register reflection service for grpcurl
	reflection.Register(grpcServer)

	return grpcServer.Serve(lis)
}

// Add implements the Add RPC.
//
// GO-001: this is a thin transport adapter over Server.addDocument — the SINGLE
// document write path shared with HTTP, MCP and GraphQL. Previously gRPC Add
// re-implemented the BoltDB insert and indexed metadata lazily via IndexQueue,
// silently skipping FTS, geo, webhooks, SSE, temporal tracking and revision
// trimming, so a doc added over gRPC was invisible to full-text/geo search and
// fired no live events. Routing through addDocument makes every transport
// behave identically.
func (g *GRPCServer) Add(ctx context.Context, req *proto.AddRequest) (*proto.Document, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	// Check write permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields")
	}

	// Convert proto meta to internal format
	meta := make(map[string][]string)
	for k, v := range req.Meta {
		meta[k] = v.Values
	}

	// Schema validation (opt-in)
	if err := g.server.SchemaManager.Validate(req.Collection, meta); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Single write path: full pipeline (meta index in-tx, revisions + trim,
	// then FTS/geo/webhooks/SSE/temporal/embedding via runPostWriteHooks).
	// addDocument also refreshes the read cache (GO-002), so the gRPC Get path
	// stays coherent without re-marshaling here.
	saved, _, err := g.server.addDocument(req.Collection, req.Key, req.Lang, meta, req.ContentMd, 0, req.SaveRevision)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return docToProto(&saved), nil
}

// AddBatch implements the AddBatch RPC - adds multiple documents in a single transaction
// Uses parallel processing for preparation, then single transaction for commit
func (g *GRPCServer) AddBatch(ctx context.Context, req *proto.AddBatchRequest) (*proto.AddBatchResponse, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	// Check write permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if len(req.Documents) == 0 {
		return &proto.AddBatchResponse{}, nil
	}

	// Use final batch processor if extreme mode, otherwise standard
	var resp *proto.AddBatchResponse
	var err error

	if g.server.UseExtreme && g.server.finalBatchProcessor != nil {
		resp, err = g.server.finalBatchProcessor.ProcessBatch(ctx, req.Collection, req.Documents)
	} else {
		resp, err = g.batchProcessor.ProcessBatch(ctx, req.Collection, req.Documents)
	}

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return resp, nil
}

// Ingest implements the Ingest RPC — bulk ingest with URL key derivation, dedup, and auto-metadata.
func (g *GRPCServer) Ingest(ctx context.Context, req *proto.IngestRequest) (*proto.IngestResponse, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if len(req.Documents) == 0 {
		return &proto.IngestResponse{Collection: req.Collection}, nil
	}

	resp, err := g.server.ingestDocumentsFromProto(ctx, req)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return protoFromIngestResponse(resp), nil
}

// Get implements the Get RPC
func (g *GRPCServer) Get(ctx context.Context, req *proto.GetRequest) (*proto.Document, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields")
	}

	// Check read permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	// Check cache first (use lock-free cache if extreme mode)
	cacheKey := cache.BuildCacheKey(req.Collection, req.Key, req.Lang)

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

	var doc storage.Doc
	var docData []byte
	err := g.server.DBView(func(tx *bolt.Tx) error {
		bByK := tx.Bucket(g.server.BucketNames.ByKey)
		bDocs := tx.Bucket(g.server.BucketNames.Docs)

		docID := bByK.Get(storage.ByKeyKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errors.New("not found")
		}

		v := bDocs.Get(storage.DocKey(req.Collection, string(docID)))
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

	// Check read permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	// Convert proto filter to internal format
	filterMeta := make(map[string][]string)
	for k, v := range req.FilterMeta {
		filterMeta[k] = v.Values
	}

	// Single transaction for both ID collection and document loading
	var docs []storage.Doc
	var docIDs []string

	err := g.server.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(g.server.BucketNames.IdxMeta)
		bDocs := tx.Bucket(g.server.BucketNames.Docs)

		if len(filterMeta) == 0 {
			// No filter: scan all docs
			c := bDocs.Cursor()
			prefix := []byte("doc|" + req.Collection + "|")
			for k, _ := c.Seek(prefix); k != nil && BytesHasPrefix(k, prefix); k, _ = c.Next() {
				// Extract docID (3rd part) without string allocations
				if docID := ExtractPart(k, 2); docID != nil {
					docIDs = append(docIDs, string(docID))
				}
			}
		} else {
			// Filter by meta
			sets := [][]string{}
			for mk, mvs := range filterMeta {
				union := []string{}
				for _, mv := range mvs {
					c := bIdx.Cursor()
					prefix := storage.MetaKeyPrefix(req.Collection, mk, mv)
					for k, _ := c.Seek(prefix); k != nil && BytesHasPrefix(k, prefix); k, _ = c.Next() {
						// Extract docID (5th part) without string allocations
						if docID := ExtractPart(k, 4); docID != nil {
							union = append(union, string(docID))
						}
					}
				}
				sets = append(sets, sliceutil.Unique(union))
			}
			docIDs = intersect(sets...)
		}

		// Load documents in the same transaction
		for _, id := range docIDs {
			v := bDocs.Get(storage.DocKey(req.Collection, id))
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
		Total:     safeInt32(total),
	}, nil
}

// Export implements the Export RPC (streaming)
func (g *GRPCServer) Export(req *proto.ExportRequest, stream proto.MDDB_ExportServer) error {
	// Check read permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(stream.Context(), req.Collection, PermRead); err != nil {
			return status.Error(codes.PermissionDenied, err.Error())
		}
	}

	// Similar to HTTP export but streaming chunks
	return status.Error(codes.Unimplemented, "export streaming not yet implemented")
}

// Backup implements the Backup RPC
func (g *GRPCServer) Backup(ctx context.Context, req *proto.BackupRequest) (*proto.BackupResponse, error) {
	// Check admin permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	filename := req.To
	if filename == "" {
		filename = fmt.Sprintf("backup-%d.db", time.Now().Unix())
	}
	safeName, err := safeBackupPath(filename, false)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	err = g.server.DBView(func(tx *bolt.Tx) error {
		return tx.CopyFile(safeName, 0600)
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &proto.BackupResponse{Backup: safeName}, nil
}

// Restore implements the Restore RPC
func (g *GRPCServer) Restore(ctx context.Context, req *proto.RestoreRequest) (*proto.RestoreResponse, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	// Check admin permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.From == "" {
		return nil, status.Error(codes.InvalidArgument, "missing backup filename")
	}
	safeFrom, err := safeBackupPath(req.From, true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// SEC-016: this used to copy the backup underneath the still-open handle —
	// the server kept serving the old data, reported success, and buried every
	// later write at the next restart. Now it shares the HTTP restore contract:
	// validated snapshot -> close -> copy -> reopen with rollback, under the
	// restore lock, followed by a binlog reset.
	if err := g.server.restoreFromBackup(safeFrom); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &proto.RestoreResponse{Restored: safeFrom}, nil
}

// Truncate implements the Truncate RPC
func (g *GRPCServer) Truncate(ctx context.Context, req *proto.TruncateRequest) (*proto.TruncateResponse, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}

	// Check write permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}

	err := g.server.DBUpdate(func(tx *bolt.Tx) error {
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
			rprefix := storage.RevPrefix(req.Collection, docID)
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
	// Check admin permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

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

	err := g.server.DBView(func(tx *bolt.Tx) error {
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
