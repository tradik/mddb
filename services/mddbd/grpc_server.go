package main

import (
	"context"
	"errors"
	"fmt"
	"mddb/proto"
	"net"
	"os"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

// GRPCServer implements the MDDB gRPC service
type GRPCServer struct {
	proto.UnimplementedMDDBServer
	server *Server
}

// NewGRPCServer creates a new gRPC server wrapper
func NewGRPCServer(s *Server) *GRPCServer {
	return &GRPCServer{server: s}
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

	var saved Doc
	err := g.server.DB.Update(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		// Load existing
		existing := Doc{}
		if v := bDocs.Get(kDoc(req.Collection, docID)); v != nil {
			if err := json.Unmarshal(v, &existing); err != nil {
				return err
			}
		}
		added := existing.AddedAt
		if added == 0 {
			added = now
		}

		doc := Doc{
			ID: docID, Key: req.Key, Lang: req.Lang, Meta: meta,
			ContentMD: req.ContentMd, AddedAt: added, UpdatedAt: now,
		}
		buf, _ := json.Marshal(doc)
		if err := bDocs.Put(kDoc(req.Collection, docID), buf); err != nil {
			return err
		}
		if err := bByK.Put(kByKey(req.Collection, req.Key, req.Lang), []byte(docID)); err != nil {
			return err
		}

		// Delete old indices
		if existing.ID != "" && existing.Meta != nil {
			for mk, vals := range existing.Meta {
				for _, mv := range vals {
					prefix := append(kMetaKeyPrefix(req.Collection, mk, mv), []byte(existing.ID)...)
					_ = bIdx.Delete(prefix)
				}
			}
		}

		// Add new indices
		for mk, vals := range doc.Meta {
			for _, mv := range vals {
				key := append(kMetaKeyPrefix(req.Collection, mk, mv), []byte(doc.ID)...)
				if err := bIdx.Put(key, []byte("1")); err != nil {
					return err
				}
			}
		}

		// Revision
		rkey := append(kRevPrefix(req.Collection, doc.ID), []byte(fmt.Sprintf("%020d", now))...)
		if err := bRev.Put(rkey, buf); err != nil {
			return err
		}

		saved = doc
		return nil
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return docToProto(&saved), nil
}

// Get implements the Get RPC
func (g *GRPCServer) Get(ctx context.Context, req *proto.GetRequest) (*proto.Document, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields")
	}

	var doc Doc
	err := g.server.DB.View(func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		bDocs := tx.Bucket([]byte("docs"))

		docID := bByK.Get(kByKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errors.New("not found")
		}

		v := bDocs.Get(kDoc(req.Collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}

		return json.Unmarshal(v, &doc)
	})

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

	var docIDs []string
	err := g.server.DB.View(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		bDocs := tx.Bucket([]byte("docs"))

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
		return nil
	})

	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Load documents
	var docs []Doc
	err = g.server.DB.View(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		for _, id := range docIDs {
			v := bDocs.Get(kDoc(req.Collection, id))
			if v != nil {
				var d Doc
				if err := json.Unmarshal(v, &d); err == nil {
					docs = append(docs, d)
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
		bRev := tx.Bucket([]byte("rev"))
		bDocs := tx.Bucket([]byte("docs"))

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
		bDocs := tx.Bucket([]byte("docs"))
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
		bRev := tx.Bucket([]byte("rev"))
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
		bIdx := tx.Bucket([]byte("idxmeta"))
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
