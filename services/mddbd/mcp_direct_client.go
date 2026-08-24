package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// DirectClient implements MCPClient by calling Server methods directly.
// This eliminates the gRPC/REST network hop that the old mddb-mcp service required.
type DirectClient struct {
	server *Server
}

// NewDirectClient creates a new DirectClient wrapping the given Server.
func NewDirectClient(s *Server) *DirectClient {
	return &DirectClient{server: s}
}

// Health checks if the database is healthy via the direct client.
func (c *DirectClient) Health(ctx context.Context) (*MCPHealth, error) {
	err := c.server.DBView(func(tx *bolt.Tx) error { return nil })
	if err != nil {
		return &MCPHealth{Status: "unhealthy", Mode: string(c.server.Mode)}, err
	}
	return &MCPHealth{Status: "healthy", Mode: string(c.server.Mode)}, nil
}

// Stats returns database statistics via the direct client.
func (c *DirectClient) Stats(ctx context.Context) (*MCPStats, error) {
	stats := &MCPStats{
		DatabasePath: c.server.Path,
		Mode:         string(c.server.Mode),
	}

	if info, err := os.Stat(c.server.Path); err == nil { // #nosec G703 -- path from server config
		stats.DatabaseSize = info.Size()
	}

	collectionMap := make(map[string]*MCPCollectionStats)

	err := c.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs != nil {
			c2 := bDocs.Cursor()
			for k, _ := c2.First(); k != nil; k, _ = c2.Next() {
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &MCPCollectionStats{Name: coll}
					}
					collectionMap[coll].DocumentCount++
					stats.TotalDocuments++
				}
			}
		}

		bRev := tx.Bucket([]byte("rev"))
		if bRev != nil {
			c2 := bRev.Cursor()
			for k, _ := c2.First(); k != nil; k, _ = c2.Next() {
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &MCPCollectionStats{Name: coll}
					}
					collectionMap[coll].RevisionCount++
					stats.TotalRevisions++
				}
			}
		}

		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx != nil {
			c2 := bIdx.Cursor()
			for k, _ := c2.First(); k != nil; k, _ = c2.Next() {
				parts := strings.Split(string(k), "|")
				if len(parts) >= 2 {
					coll := parts[1]
					if _, ok := collectionMap[coll]; !ok {
						collectionMap[coll] = &MCPCollectionStats{Name: coll}
					}
					collectionMap[coll].MetaIndexCount++
					stats.TotalMetaIndices++
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, cs := range collectionMap {
		stats.Collections = append(stats.Collections, *cs)
	}
	sort.Slice(stats.Collections, func(i, j int) bool {
		return stats.Collections[i].Name < stats.Collections[j].Name
	})

	return stats, nil
}

// Add creates a new document via the direct client.
func (c *DirectClient) Add(ctx context.Context, req *MCPAddRequest) (*MCPDocument, error) {
	saved, _, err := c.server.addDocument(req.Collection, req.Key, req.Lang, req.Meta, req.ContentMD, 0, true)
	if err != nil {
		return nil, err
	}
	doc := docToMCPDocument(saved)
	return &doc, nil
}

// AddBatch creates multiple documents in a single operation via the direct client.
func (c *DirectClient) AddBatch(ctx context.Context, req *MCPAddBatchRequest) (*MCPAddBatchResponse, error) {
	protoDocs := make([]*proto.BatchDocument, len(req.Documents))
	for i, d := range req.Documents {
		protoDocs[i] = &proto.BatchDocument{
			Key:          d.Key,
			Lang:         d.Lang,
			Meta:         toProtoMeta(d.Meta),
			ContentMd:    d.ContentMD,
			SaveRevision: d.SaveRevision,
		}
	}

	var resp *proto.AddBatchResponse
	var err error

	if c.server.finalBatchProcessor != nil {
		resp, err = c.server.finalBatchProcessor.ProcessBatch(ctx, req.Collection, protoDocs)
	} else {
		bp := NewBatchProcessor(c.server, 8)
		resp, err = bp.ProcessBatch(ctx, req.Collection, protoDocs)
	}
	if err != nil {
		return nil, err
	}

	return &MCPAddBatchResponse{
		Added:   int(resp.Added),
		Updated: int(resp.Updated),
		Failed:  int(resp.Failed),
		Errors:  resp.Errors,
	}, nil
}

// UpdateBatch updates multiple documents in a single operation via the direct client.
func (c *DirectClient) UpdateBatch(ctx context.Context, req *MCPUpdateBatchRequest) (*MCPUpdateBatchResponse, error) {
	protoDocs := make([]*proto.UpdateDocument, len(req.Documents))
	for i, d := range req.Documents {
		protoDocs[i] = &proto.UpdateDocument{
			Key:          d.Key,
			Lang:         d.Lang,
			Meta:         toProtoMeta(d.Meta),
			ContentMd:    d.ContentMD,
			SaveRevision: d.SaveRevision,
		}
	}

	bu := NewBatchUpdater(c.server, 8)
	resp, err := bu.ProcessBatchUpdate(ctx, req.Collection, protoDocs)
	if err != nil {
		return nil, err
	}

	return &MCPUpdateBatchResponse{
		Updated:  int(resp.Updated),
		NotFound: int(resp.NotFound),
		Failed:   int(resp.Failed),
		Errors:   resp.Errors,
	}, nil
}

// DeleteBatch removes multiple documents in a single operation via the direct client.
func (c *DirectClient) DeleteBatch(ctx context.Context, req *MCPDeleteBatchRequest) (*MCPDeleteBatchResponse, error) {
	protoDocs := make([]*proto.DeleteDocument, len(req.Documents))
	for i, d := range req.Documents {
		protoDocs[i] = &proto.DeleteDocument{
			Key:  d.Key,
			Lang: d.Lang,
		}
	}

	bd := NewBatchDeleter(c.server, 8)
	resp, err := bd.ProcessBatchDelete(ctx, req.Collection, protoDocs)
	if err != nil {
		return nil, err
	}

	return &MCPDeleteBatchResponse{
		Deleted:  int(resp.Deleted),
		NotFound: int(resp.NotFound),
		Failed:   int(resp.Failed),
		Errors:   resp.Errors,
	}, nil
}

// Get retrieves a document via the direct client.
func (c *DirectClient) Get(ctx context.Context, req *MCPGetRequest) (*MCPDocument, error) {
	var doc storage.Doc
	err := c.server.DBView(func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		docID := bByK.Get(storage.ByKeyKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errors.New("not found")
		}
		bDocs := tx.Bucket([]byte("docs"))
		v := bDocs.Get(storage.DocKey(req.Collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}
		docPtr, unmErr := loadDoc(v)
		if unmErr != nil {
			return unmErr
		}
		doc = *docPtr
		return nil
	})
	if err != nil {
		return nil, err
	}

	if doc.ExpiresAt > 0 && doc.ExpiresAt < time.Now().Unix() {
		return nil, errors.New("not found")
	}

	if len(req.Env) > 0 && doc.ContentMD != "" {
		doc.ContentMD = applyEnv(doc.ContentMD, req.Env)
	}

	result := docToMCPDocument(doc)
	return &result, nil
}

// Search queries documents via the direct client.
func (c *DirectClient) Search(ctx context.Context, req *MCPSearchRequest) (*MCPSearchResponse, error) {
	if req.Limit <= 0 {
		req.Limit = 50
	}

	type row struct{ Doc storage.Doc }
	var rows []row

	err := c.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		seen := make(map[string]bool)

		if len(req.FilterMeta) == 0 {
			c2 := bDocs.Cursor()
			prefix := []byte("doc|" + req.Collection + "|")
			for k, v := c2.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c2.Next() {
				d, err := loadDoc(v)
				if err != nil {
					return err
				}
				if d.ExpiresAt > 0 && d.ExpiresAt < time.Now().Unix() {
					continue
				}
				if !req.IncludeContent {
					d.ContentMD = "" // GO-022: don't carry a body the caller discards
				}
				rows = append(rows, row{*d})
			}
		} else {
			var sets [][]string
			for mk, mvals := range req.FilterMeta {
				var ids []string
				for _, mv := range mvals {
					prefix := storage.MetaKeyPrefix(req.Collection, mk, mv)
					c2 := bIdx.Cursor()
					for k, _ := c2.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c2.Next() {
						id := string(k[len(prefix):])
						ids = append(ids, id)
					}
				}
				ids = sliceutil.Unique(ids)
				sets = append(sets, ids)
			}
			ids := intersect(sets...)
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				v := bDocs.Get(storage.DocKey(req.Collection, id))
				if v == nil {
					continue
				}
				d, err := loadDoc(v)
				if err != nil {
					return err
				}
				if d.ExpiresAt > 0 && d.ExpiresAt < time.Now().Unix() {
					continue
				}
				if !req.IncludeContent {
					d.ContentMD = "" // GO-022: don't carry a body the caller discards
				}
				rows = append(rows, row{*d})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort
	switch req.Sort {
	case "addedAt":
		sort.Slice(rows, func(i, j int) bool {
			if req.Asc {
				return rows[i].Doc.AddedAt < rows[j].Doc.AddedAt
			}
			return rows[i].Doc.AddedAt > rows[j].Doc.AddedAt
		})
	case "updatedAt":
		sort.Slice(rows, func(i, j int) bool {
			if req.Asc {
				return rows[i].Doc.UpdatedAt < rows[j].Doc.UpdatedAt
			}
			return rows[i].Doc.UpdatedAt > rows[j].Doc.UpdatedAt
		})
	case "key":
		sort.Slice(rows, func(i, j int) bool {
			if req.Asc {
				return rows[i].Doc.Key < rows[j].Doc.Key
			}
			return rows[i].Doc.Key > rows[j].Doc.Key
		})
	}

	// Paginate
	total := len(rows)
	start := req.Offset
	if start > total {
		start = total
	}
	end := start + req.Limit
	if end > total {
		end = total
	}

	docs := make([]MCPDocument, 0, end-start)
	for _, r := range rows[start:end] {
		docs = append(docs, docToMCPDocument(r.Doc))
	}

	return &MCPSearchResponse{
		Documents: docs,
		Total:     total,
	}, nil
}

// Delete removes a document via the direct client.
func (c *DirectClient) Delete(ctx context.Context, req *MCPDeleteRequest) error {
	return c.server.deleteDocumentInternal(req.Collection, req.Key, req.Lang)
}

// DeleteCollection removes an entire collection via the direct client.
func (c *DirectClient) DeleteCollection(ctx context.Context, req *MCPDeleteCollectionRequest) (*MCPDeleteCollectionResponse, error) {
	var deletedCount int

	err := c.server.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		bRev := tx.Bucket([]byte("rev"))
		bByK := tx.Bucket([]byte("bykey"))

		cur := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cur.Next() {
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			doc := *docPtr
			if err := bDocs.Delete(k); err != nil {
				return err
			}

			rc := bRev.Cursor()
			rp := storage.RevPrefix(req.Collection, doc.ID)
			for rk, _ := rc.Seek(rp); rk != nil && bytes.HasPrefix(rk, rp); rk, _ = rc.Next() {
				if err := bRev.Delete(rk); err != nil {
					return err
				}
			}

			for mk, vals := range doc.Meta {
				for _, mv := range vals {
					mkey := append(storage.MetaKeyPrefix(req.Collection, mk, mv), []byte(doc.ID)...)
					if err := bIdx.Delete(mkey); err != nil {
						return err
					}
				}
			}

			deletedCount++
		}

		// DOC-016: every key-index entry for this collection, cleared by
		// prefix rather than one spelling per document — see
		// document_key_case.go.
		return deleteCollectionByKeyEntries(bByK, req.Collection, nil)
	})
	if err != nil {
		return nil, err
	}

	return &MCPDeleteCollectionResponse{Deleted: deletedCount}, nil
}

// collectExportDocs reads every document an export request selects.
//
// Separate from Export because the HTTP handler's zip format needs the same
// selection and must not re-derive it: the two used to disagree about what
// "filterMeta" means because each wrote its own query.
func (c *DirectClient) collectExportDocs(req *MCPExportRequest) ([]storage.Doc, error) {
	var docs []storage.Doc

	err := c.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))

		if len(req.FilterMeta) == 0 {
			cur := bDocs.Cursor()
			prefix := []byte("doc|" + req.Collection + "|")
			for k, v := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cur.Next() {
				d, err := loadDoc(v)
				if err != nil {
					continue
				}
				docs = append(docs, *d)
			}
		} else {
			var sets [][]string
			for mk, mvals := range req.FilterMeta {
				var ids []string
				for _, mv := range mvals {
					prefix := storage.MetaKeyPrefix(req.Collection, mk, mv)
					cur := bIdx.Cursor()
					for k, _ := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cur.Next() {
						id := string(k[len(prefix):])
						ids = append(ids, id)
					}
				}
				ids = sliceutil.Unique(ids)
				sets = append(sets, ids)
			}
			for _, id := range intersect(sets...) {
				v := bDocs.Get(storage.DocKey(req.Collection, id))
				if v == nil {
					continue
				}
				d, err := loadDoc(v)
				if err != nil {
					continue
				}
				docs = append(docs, *d)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

// Export streams a collection as NDJSON.
func (c *DirectClient) Export(ctx context.Context, req *MCPExportRequest) (io.ReadCloser, error) {
	docs, err := c.collectExportDocs(req)
	if err != nil {
		return nil, err
	}

	// Stream NDJSON rather than materialising it (GO-021).
	//
	// This used to marshal every document into one bytes.Buffer and hand back
	// a reader over it, so exporting a large collection built the entire
	// export in memory before the caller received a single byte — twice the
	// corpus, since the documents were already collected above. A pipe lets
	// the caller consume while the encoder writes, and the pooled writer
	// keeps the encoder from allocating a fresh buffer per document.
	pr, pw := io.Pipe()
	go func() {
		zw := NewZeroCopyWriter(pw, exportBufferSize)
		var writeErr error
		for i := range docs {
			b, err := json.Marshal(docs[i])
			if err != nil {
				// One unserialisable document must not silently truncate
				// the export into something that looks complete.
				writeErr = fmt.Errorf("document %q: %w", docs[i].ID, err)
				break
			}
			if _, err := zw.Write(append(b, '\n')); err != nil {
				writeErr = err
				break
			}
		}
		// Close flushes the tail and returns the pooled buffer; its error
		// only matters when nothing has failed already.
		if cerr := zw.Close(); writeErr == nil {
			writeErr = cerr
		}
		// Closing with an error propagates it to the reader, so a caller
		// cannot mistake a failed export for a short one.
		_ = pw.CloseWithError(writeErr)
	}()

	return pr, nil
}

// exportBufferSize is how much export output is buffered before a write
// reaches the pipe. 64 KiB is large enough that a document rarely spans two
// flushes and small enough that a slow consumer does not pin much memory.
const exportBufferSize = 64 << 10

// Backup creates a database backup via the direct client.
func (c *DirectClient) Backup(ctx context.Context, req *MCPBackupRequest) (*MCPBackupResponse, error) {
	dst := req.To
	if dst == "" {
		dst = fmt.Sprintf("backup-%d.db", time.Now().Unix())
	}
	safeDst, err := safeBackupPath(dst, false)
	if err != nil {
		return nil, err
	}
	if err := copyFile(c.server.Path, safeDst); err != nil {
		return nil, err
	}
	return &MCPBackupResponse{Backup: safeDst}, nil
}

// Restore restores documents from a backup via the direct client.
func (c *DirectClient) Restore(ctx context.Context, req *MCPRestoreRequest) (*MCPRestoreResponse, error) {
	if req.From == "" {
		return nil, errors.New("missing from")
	}
	safeFrom, err := safeBackupPath(req.From, true)
	if err != nil {
		return nil, err
	}
	_ = c.server.DB.Close()
	if err := copyFile(safeFrom, c.server.Path); err != nil {
		return nil, err
	}
	db, err := bolt.Open(c.server.Path, 0600, getOptimizedBoltOptions())
	if err != nil {
		return nil, err
	}
	c.server.DB = db
	return &MCPRestoreResponse{Restored: safeFrom}, nil
}

// Truncate removes all documents from a collection via the direct client.
func (c *DirectClient) Truncate(ctx context.Context, req *MCPTruncateRequest) (*MCPTruncateResponse, error) {
	err := c.server.DBUpdate(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		bDocs := tx.Bucket([]byte("docs"))

		cur := bDocs.Cursor()
		prefix := []byte("doc|" + req.Collection + "|")
		for k, v := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cur.Next() {
			dPtr, err := loadDoc(v)
			if err != nil {
				return err
			}
			d := *dPtr
			rc := bRev.Cursor()
			rp := storage.RevPrefix(req.Collection, d.ID)
			var revKeys [][]byte
			for rk, _ := rc.Seek(rp); rk != nil && bytes.HasPrefix(rk, rp); rk, _ = rc.Next() {
				cp := make([]byte, len(rk))
				copy(cp, rk)
				revKeys = append(revKeys, cp)
			}
			if req.KeepRevs >= 0 && len(revKeys) > req.KeepRevs {
				toDel := revKeys[:len(revKeys)-req.KeepRevs]
				for _, delk := range toDel {
					_ = bRev.Delete(delk)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &MCPTruncateResponse{Status: "truncated"}, nil
}
