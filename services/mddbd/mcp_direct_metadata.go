package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mddb/internal/storage"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// RegisterWebhook registers a new webhook via the direct client.
func (c *DirectClient) RegisterWebhook(ctx context.Context, req *MCPRegisterWebhookRequest) (*MCPWebhook, error) {
	if c.server.WebhookManager == nil {
		return nil, errors.New("webhooks not initialized")
	}
	wh, err := c.server.WebhookManager.Register(req.URL, req.Events, req.Collection)
	if err != nil {
		return nil, err
	}
	return &MCPWebhook{
		ID:         wh.ID,
		URL:        wh.URL,
		Events:     wh.Events,
		Collection: wh.Collection,
		CreatedAt:  wh.CreatedAt,
	}, nil
}

// ListWebhooks returns all registered webhooks via the direct client.
func (c *DirectClient) ListWebhooks(ctx context.Context) ([]MCPWebhook, error) {
	if c.server.WebhookManager == nil {
		return nil, errors.New("webhooks not initialized")
	}
	hooks := c.server.WebhookManager.List()
	result := make([]MCPWebhook, len(hooks))
	for i, wh := range hooks {
		result[i] = MCPWebhook(wh)
	}
	return result, nil
}

// DeleteWebhook removes a webhook via the direct client.
func (c *DirectClient) DeleteWebhook(ctx context.Context, req *MCPDeleteWebhookRequest) error {
	if c.server.WebhookManager == nil {
		return errors.New("webhooks not initialized")
	}
	return c.server.WebhookManager.Delete(req.ID)
}

// SetSchema sets a JSON schema for a collection via the direct client.
func (c *DirectClient) SetSchema(ctx context.Context, req *MCPSetSchemaRequest) error {
	if c.server.SchemaManager == nil {
		return errors.New("schema manager not initialized")
	}
	return c.server.SchemaManager.Set(req.Collection, req.Schema)
}

// GetSchema retrieves the JSON schema for a collection via the direct client.
func (c *DirectClient) GetSchema(ctx context.Context, collection string) (*MCPSchemaResponse, error) {
	if c.server.SchemaManager == nil {
		return nil, errors.New("schema manager not initialized")
	}
	raw, found := c.server.SchemaManager.Get(collection)
	return &MCPSchemaResponse{
		Collection: collection,
		Schema:     raw,
		Enabled:    found,
	}, nil
}

// DeleteSchema removes the JSON schema for a collection via the direct client.
func (c *DirectClient) DeleteSchema(ctx context.Context, collection string) error {
	if c.server.SchemaManager == nil {
		return errors.New("schema manager not initialized")
	}
	return c.server.SchemaManager.Delete(collection)
}

// ListSchemas returns all registered schemas via the direct client.
func (c *DirectClient) ListSchemas(ctx context.Context) (*MCPListSchemasResponse, error) {
	if c.server.SchemaManager == nil {
		return nil, errors.New("schema manager not initialized")
	}
	schemas := c.server.SchemaManager.List()
	result := &MCPListSchemasResponse{
		Schemas: make([]MCPSchemaInfo, 0, len(schemas)),
	}
	for col, raw := range schemas {
		result.Schemas = append(result.Schemas, MCPSchemaInfo{
			Collection: col,
			Schema:     raw,
		})
	}
	return result, nil
}

// ValidateDocument validates a document against its collection schema via the direct client.
func (c *DirectClient) ValidateDocument(ctx context.Context, req *MCPValidateRequest) (*MCPValidateResponse, error) {
	if c.server.SchemaManager == nil {
		return &MCPValidateResponse{Valid: true, Errors: []string{}}, nil
	}
	err := c.server.SchemaManager.Validate(req.Collection, req.Meta)
	if err != nil {
		parts := strings.Split(err.Error(), "; ")
		return &MCPValidateResponse{Valid: false, Errors: parts}, nil
	}
	return &MCPValidateResponse{Valid: true, Errors: []string{}}, nil
}

// UpdateDocument updates an existing document via the direct client.
func (c *DirectClient) UpdateDocument(ctx context.Context, req *MCPUpdateDocumentRequest) (*MCPDocument, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, errors.New("missing required fields: collection, key, lang")
	}

	hasMeta := req.Meta != nil
	hasContent := req.ContentMD != nil
	hasTTL := req.TTL != nil

	if !hasMeta && !hasContent && !hasTTL {
		return nil, errors.New("no fields to update")
	}

	now := time.Now().Unix()
	var saved storage.Doc

	err := c.server.DBUpdate(func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		bDocs := tx.Bucket([]byte("docs"))

		docIDBytes := bByK.Get(storage.ByKeyKey(req.Collection, req.Key, req.Lang))
		if docIDBytes == nil {
			return errors.New("not found")
		}
		v := bDocs.Get(storage.DocKey(req.Collection, string(docIDBytes)))
		if v == nil {
			return errors.New("not found")
		}
		existing, err := loadDoc(v)
		if err != nil {
			return err
		}

		doc := *existing
		doc.UpdatedAt = now

		if hasMeta {
			doc.Meta = req.Meta
		}
		if hasContent {
			doc.ContentMD = *req.ContentMD
		}
		if hasTTL {
			if *req.TTL > 0 {
				doc.ExpiresAt = now + *req.TTL
			} else {
				doc.ExpiresAt = 0
			}
		}

		buf, err := marshalAndEncrypt(&doc, req.Collection)
		if err != nil {
			return err
		}
		if err := bDocs.Put(storage.DocKey(req.Collection, doc.ID), buf); err != nil {
			return err
		}

		saved = doc
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := docToMCPDocument(saved)
	return &result, nil
}

// GetDocumentMeta retrieves document metadata via the direct client.
func (c *DirectClient) GetDocumentMeta(ctx context.Context, req *MCPGetDocMetaRequest) (*MCPDocMetaResponse, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, errors.New("missing required fields: collection, key, lang")
	}

	var doc storage.Doc
	err := c.server.DBView(func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		bDocs := tx.Bucket([]byte("docs"))
		docID := bByK.Get(storage.ByKeyKey(req.Collection, req.Key, req.Lang))
		if docID == nil {
			return errors.New("not found")
		}
		v := bDocs.Get(storage.DocKey(req.Collection, string(docID)))
		if v == nil {
			return errors.New("not found")
		}
		d, err := loadDoc(v)
		if err != nil {
			return err
		}
		doc = *d
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &MCPDocMetaResponse{
		Key:       doc.Key,
		Lang:      doc.Lang,
		Meta:      doc.Meta,
		AddedAt:   doc.AddedAt,
		UpdatedAt: doc.UpdatedAt,
		ExpiresAt: doc.ExpiresAt,
	}, nil
}

// Classify classifies a document via the direct client.
func (c *DirectClient) Classify(ctx context.Context, req *MCPClassifyRequest) (*MCPClassifyResponse, error) {
	resp, err := c.server.classifyDocument(ctx, req.Collection, req.Key, req.Lang, req.Text, req.Labels, req.TopK, req.Multi, req.Threshold)
	if err != nil {
		return nil, err
	}

	results := make([]MCPClassifyLabelScore, len(resp.Results))
	for i, r := range resp.Results {
		results[i] = MCPClassifyLabelScore(r)
	}

	return &MCPClassifyResponse{
		Results:    results,
		Model:      resp.Model,
		Dimensions: resp.Dimensions,
	}, nil
}

// --- Synonyms ---

// ListSynonyms returns synonyms for a collection via the direct client.
func (c *DirectClient) ListSynonyms(ctx context.Context, collection string) (*MCPSynonymListResponse, error) {
	if c.server.SynonymManager == nil {
		return nil, errors.New("synonym manager not initialized")
	}
	m := c.server.SynonymManager.List(collection)
	entries := make([]MCPSynonymEntry, 0, len(m))
	for term, syns := range m {
		entries = append(entries, MCPSynonymEntry{Term: term, Synonyms: syns})
	}
	return &MCPSynonymListResponse{
		Collection: collection,
		Entries:    entries,
		Total:      len(entries),
	}, nil
}

// SetSynonym sets a synonym mapping via the direct client.
func (c *DirectClient) SetSynonym(ctx context.Context, collection, term string, synonyms []string) error {
	if c.server.SynonymManager == nil {
		return errors.New("synonym manager not initialized")
	}
	return c.server.SynonymManager.Set(collection, term, synonyms)
}

// DeleteSynonym removes a synonym mapping via the direct client.
func (c *DirectClient) DeleteSynonym(ctx context.Context, collection, term string) error {
	if c.server.SynonymManager == nil {
		return errors.New("synonym manager not initialized")
	}
	return c.server.SynonymManager.Delete(collection, term)
}

// --- Stop Words ---

// ListStopWords returns stop words for a collection via the direct client.
func (c *DirectClient) ListStopWords(ctx context.Context, collection string) (*MCPStopWordListResponse, error) {
	if c.server.StopWordManager == nil {
		return nil, errors.New("stop word manager not initialized")
	}
	defaults, custom := c.server.StopWordManager.List(collection)
	entries := make([]MCPStopWordEntry, 0, len(defaults)+len(custom))
	for _, w := range defaults {
		entries = append(entries, MCPStopWordEntry{Word: w, IsDefault: true})
	}
	for _, w := range custom {
		entries = append(entries, MCPStopWordEntry{Word: w, IsDefault: false})
	}
	return &MCPStopWordListResponse{
		Collection: collection,
		Entries:    entries,
		Total:      len(entries),
		Defaults:   len(defaults),
		Custom:     len(custom),
	}, nil
}

// AddStopWords adds stop words for a collection via the direct client.
func (c *DirectClient) AddStopWords(ctx context.Context, collection string, words []string) error {
	if c.server.StopWordManager == nil {
		return errors.New("stop word manager not initialized")
	}
	return c.server.StopWordManager.Add(collection, words)
}

// DeleteStopWords removes stop words from a collection via the direct client.
func (c *DirectClient) DeleteStopWords(ctx context.Context, collection string, words []string) error {
	if c.server.StopWordManager == nil {
		return errors.New("stop word manager not initialized")
	}
	var errs []string
	for _, w := range words {
		if err := c.server.StopWordManager.Delete(collection, w); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("some deletions failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// --- Meta Keys / Checksum ---

// GetMetaKeys returns all metadata keys for a collection via the direct client.
func (c *DirectClient) GetMetaKeys(ctx context.Context, collection string) (*MCPMetaKeysResponse, error) {
	meta := make(map[string][]string)
	_ = c.server.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx == nil {
			return nil
		}
		prefix := []byte("meta|" + collection + "|")
		cur := bIdx.Cursor()
		seen := make(map[string]map[string]bool)
		for k, _ := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = cur.Next() {
			rest := string(k[len(prefix):])
			parts := strings.SplitN(rest, "|", 3)
			if len(parts) < 2 {
				continue
			}
			mk, mv := parts[0], parts[1]
			if seen[mk] == nil {
				seen[mk] = make(map[string]bool)
			}
			if !seen[mk][mv] {
				seen[mk][mv] = true
				meta[mk] = append(meta[mk], mv)
			}
		}
		return nil
	})
	return &MCPMetaKeysResponse{Meta: meta}, nil
}

// GetChecksum returns a checksum for a collection via the direct client.
func (c *DirectClient) GetChecksum(ctx context.Context, collection string) (*MCPChecksumResponse, error) {
	checksum, count := c.server.collectionChecksum(collection)
	return &MCPChecksumResponse{
		Collection:    collection,
		Checksum:      checksum,
		DocumentCount: count,
	}, nil
}

// CodeGraph resolves the connection graph around one code document (CODE-005).
//
// Read-only and index-backed: it derives edges from the symbol meta through the
// metadata index, loading no document content.
func (c *DirectClient) CodeGraph(_ context.Context, req *MCPCodeGraphRequest) (*GraphResult, error) {
	direction, err := ParseGraphDirection(req.Direction)
	if err != nil {
		return nil, err
	}
	return c.server.CodeGraph(GraphRequest{
		Collection:   req.Collection,
		Key:          req.Key,
		Direction:    direction,
		Depth:        req.Depth,
		MaxDegree:    req.MaxDegree,
		IncludeLines: req.Lines,
	})
}
