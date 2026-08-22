package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
	proto "mddb/proto"
	"sort"
	"strconv"
	"time"

	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
)

// --- Automation ---

// ListAutomation returns automation rules via the direct client.
func (c *DirectClient) ListAutomation(ctx context.Context, filterType string) (*MCPAutomationListResponse, error) {
	if c.server.AutomationManager == nil {
		return nil, errors.New("automation not initialized")
	}
	rules := c.server.AutomationManager.List(filterType)
	return &MCPAutomationListResponse{Rules: rules, Total: len(rules)}, nil
}

// CreateAutomation creates a new automation rule via the direct client.
func (c *DirectClient) CreateAutomation(ctx context.Context, rule AutomationRule) (*AutomationRule, error) {
	if c.server.AutomationManager == nil {
		return nil, errors.New("automation not initialized")
	}
	return c.server.AutomationManager.Create(rule)
}

// GetAutomation retrieves an automation rule by ID via the direct client.
func (c *DirectClient) GetAutomation(ctx context.Context, id string) (*AutomationRule, error) {
	if c.server.AutomationManager == nil {
		return nil, errors.New("automation not initialized")
	}
	rule := c.server.AutomationManager.Get(id)
	if rule == nil {
		return nil, errors.New("not found")
	}
	return rule, nil
}

// UpdateAutomation updates an automation rule via the direct client.
func (c *DirectClient) UpdateAutomation(ctx context.Context, id string, rule AutomationRule) (*AutomationRule, error) {
	if c.server.AutomationManager == nil {
		return nil, errors.New("automation not initialized")
	}
	return c.server.AutomationManager.Update(id, rule)
}

// DeleteAutomation removes an automation rule via the direct client.
func (c *DirectClient) DeleteAutomation(ctx context.Context, id string) error {
	if c.server.AutomationManager == nil {
		return errors.New("automation not initialized")
	}
	return c.server.AutomationManager.Delete(id)
}

// TestAutomation runs an automation rule in test mode via the direct client.
func (c *DirectClient) TestAutomation(ctx context.Context, id string) (string, error) {
	if c.server.AutomationManager == nil {
		return "", errors.New("automation not initialized")
	}
	rule := c.server.AutomationManager.Get(id)
	if rule == nil {
		return "", errors.New("not found")
	}
	if rule.Type != "trigger" {
		return "", fmt.Errorf("can only test trigger rules, got: %s", rule.Type)
	}
	matches, err := c.server.AutomationManager.RunTrigger(rule)
	if err != nil {
		return "", err
	}
	resp := map[string]interface{}{
		"trigger": map[string]interface{}{
			"id":         rule.ID,
			"name":       rule.Name,
			"searchType": rule.SearchType,
			"query":      rule.Query,
			"threshold":  rule.Threshold,
		},
		"matches": matches,
		"total":   len(matches),
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	return string(data), nil
}

// ListAutomationLogs returns automation execution logs via the direct client.
func (c *DirectClient) ListAutomationLogs(ctx context.Context, limit int, cursor, ruleID, status string) (*MCPAutomationLogListResponse, error) {
	if c.server.AutomationLogStore == nil {
		return nil, errors.New("automation logs not initialized")
	}
	if limit <= 0 {
		limit = 50
	}
	logs, nextCursor, err := c.server.AutomationLogStore.List(limit, cursor, ruleID, status)
	if err != nil {
		return nil, err
	}
	total, _ := c.server.AutomationLogStore.Count(ruleID, status)
	return &MCPAutomationLogListResponse{
		Logs:       logs,
		Total:      total,
		NextCursor: nextCursor,
		HasMore:    nextCursor != "",
	}, nil
}

// ListRevisions returns document revision history via the direct client.
func (c *DirectClient) ListRevisions(ctx context.Context, collection, key, lang string) (*RevisionListResponse, error) {
	docID := genID(collection, key, lang)
	var revisions []RevisionEntry
	err := c.server.DBView(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		if bRev == nil {
			return nil
		}
		prefix := storage.RevPrefix(collection, docID)
		cur := bRev.Cursor()
		for k, v := cur.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = cur.Next() {
			lastPipe := bytes.LastIndexByte(k, '|')
			if lastPipe < 0 || lastPipe >= len(k)-1 {
				continue
			}
			ts, err := strconv.ParseInt(string(k[lastPipe+1:]), 10, 64)
			if err != nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			revisions = append(revisions, RevisionEntry{
				Timestamp: ts,
				UpdatedAt: docPtr.UpdatedAt,
				ContentMD: docPtr.ContentMD,
				Meta:      docPtr.Meta,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Timestamp > revisions[j].Timestamp
	})
	return &RevisionListResponse{
		Collection: collection,
		Key:        key,
		Lang:       lang,
		Revisions:  revisions,
		Total:      len(revisions),
	}, nil
}

// RestoreRevision restores a document to a previous revision via the direct client.
func (c *DirectClient) RestoreRevision(ctx context.Context, collection, key, lang string, timestamp int64) (*MCPDocument, error) {
	docID := genID(collection, key, lang)
	tsKey := fmt.Sprintf("%020d", timestamp)
	revKey := append(storage.RevPrefix(collection, docID), []byte(tsKey)...)

	var revDoc *storage.Doc
	err := c.server.DBView(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		if bRev == nil {
			return fmt.Errorf("revision not found")
		}
		v := bRev.Get(revKey)
		if v == nil {
			return fmt.Errorf("revision not found for timestamp %d", timestamp)
		}
		var err error
		revDoc, err = loadDoc(v)
		return err
	})
	if err != nil {
		return nil, err
	}

	doc, _, err := c.server.addDocument(collection, key, lang, revDoc.Meta, revDoc.ContentMD, 0, true)
	if err != nil {
		return nil, err
	}
	mcpDoc := docToMCPDocument(doc)
	return &mcpDoc, nil
}

// --- Collection Config ---

// GetCollectionConfig retrieves configuration for a collection via the direct client.
func (c *DirectClient) GetCollectionConfig(ctx context.Context, collection string) (*MCPCollectionConfigResponse, error) {
	cfg, found := c.server.CollectionManager.Get(collection)
	if !found {
		cfg = &CollectionConfig{Type: "default"}
	}
	return &MCPCollectionConfigResponse{
		Collection: collection,
		Config:     cfg,
		Configured: found,
	}, nil
}

// SetCollectionConfig updates configuration for a collection via the direct client.
func (c *DirectClient) SetCollectionConfig(ctx context.Context, req *MCPSetCollectionConfigRequest) error {
	if err := validateWordPressTarget(req.WordPress); err != nil {
		return err
	}
	// Merge into the stored config rather than replacing it.
	//
	// MCPSetCollectionConfigRequest covers 9 of CollectionConfig's ~18 fields,
	// and CollectionManager.Set writes whatever struct it is handed. Building
	// a fresh one erased storageBackend, quantization, spell settings and —
	// worst — Encrypted, whose false value goes straight into the encryptor,
	// so an agent updating a description left the next document in an
	// encrypted collection stored as plaintext. Same defect as the gRPC path
	// fixed in RAG-001; this is the MCP one.
	cfg := &CollectionConfig{}
	if existing, found := c.server.CollectionManager.Get(req.Collection); found && existing != nil {
		*cfg = *existing
	}
	cfg.Type = req.Type
	cfg.Description = req.Description
	cfg.Icon = req.Icon
	cfg.Color = req.Color
	cfg.CustomMeta = req.CustomMeta
	cfg.MaxRevisions = req.MaxRevisions
	cfg.WordPress = req.WordPress

	if req.Retrieval != nil {
		if err := req.Retrieval.Validate(); err != nil {
			return err
		}
		cfg.Retrieval = req.Retrieval
	}
	if req.ResponsePrompt != "" {
		if err := ValidateResponsePrompt(req.ResponsePrompt); err != nil {
			return err
		}
		cfg.ResponsePrompt = req.ResponsePrompt
	}

	return c.server.CollectionManager.Set(req.Collection, cfg)
}

// ListCurationRules returns all curation rules for a collection (or all collections if empty).
func (c *DirectClient) ListCurationRules(ctx context.Context, collection string) ([]*CurationRule, error) {
	if c.server.CurationManager == nil {
		return nil, fmt.Errorf("curation manager not initialized")
	}
	if collection == "" {
		return c.server.CurationManager.ListAll(), nil
	}
	return c.server.CurationManager.ListByCollection(collection), nil
}

// CreateCurationRule creates a new rule and returns it with its assigned id.
func (c *DirectClient) CreateCurationRule(ctx context.Context, rule *CurationRule) (*CurationRule, error) {
	if c.server.CurationManager == nil {
		return nil, fmt.Errorf("curation manager not initialized")
	}
	if rule == nil {
		return nil, fmt.Errorf("nil rule")
	}
	rule.ID = "" // always new on create
	if err := c.server.CurationManager.Set(rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// UpdateCurationRule replaces a rule by id.
func (c *DirectClient) UpdateCurationRule(ctx context.Context, rule *CurationRule) (*CurationRule, error) {
	if c.server.CurationManager == nil {
		return nil, fmt.Errorf("curation manager not initialized")
	}
	if rule == nil || rule.ID == "" {
		return nil, fmt.Errorf("rule.id is required")
	}
	prev, exists := c.server.CurationManager.Get(rule.ID)
	if !exists {
		return nil, fmt.Errorf("rule %q not found", rule.ID)
	}
	if rule.Collection == "" {
		rule.Collection = prev.Collection
	}
	rule.CreatedAt = prev.CreatedAt
	if err := c.server.CurationManager.Set(rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// DeleteCurationRule removes a rule by id.
func (c *DirectClient) DeleteCurationRule(ctx context.Context, id string) error {
	if c.server.CurationManager == nil {
		return fmt.Errorf("curation manager not initialized")
	}
	if _, exists := c.server.CurationManager.Get(id); !exists {
		return fmt.Errorf("rule %q not found", id)
	}
	return c.server.CurationManager.Delete(id)
}

// ListCollectionConfigs returns all collection configurations via the direct client.
func (c *DirectClient) ListCollectionConfigs(ctx context.Context) (*MCPCollectionConfigListResponse, error) {
	all := c.server.CollectionManager.ListAll()
	return &MCPCollectionConfigListResponse{
		Configs: all,
		Total:   len(all),
	}, nil
}

// --- Cross-Collection Search ---

// CrossSearch searches across multiple collections via the direct client.
func (c *DirectClient) CrossSearch(ctx context.Context, req *MCPCrossSearchRequest) (*MCPCrossSearchResponse, error) {
	s := c.server

	// Select algorithm
	algo := req.Algorithm
	if algo == "" {
		algo = "flat"
	}
	searcher, ok2 := s.VectorSearchers[algo]
	if !ok2 {
		return nil, fmt.Errorf("unknown algorithm: %s", algo)
	}
	if !searcher.IsReady() {
		searcher = s.VectorSearchers["flat"]
		algo = "flat"
	}
	if !searcher.IsReady() {
		return nil, fmt.Errorf("vector index not ready")
	}

	// Resolve query vector
	var queryVector []float32
	if req.SourceDocID != "" {
		if req.SourceCollection == "" {
			return nil, fmt.Errorf("sourceCollection required when using sourceDocID")
		}
		rec, err := s.VectorStore.Get(req.SourceCollection, req.SourceDocID)
		if err != nil || rec == nil {
			return nil, fmt.Errorf("source document has no embedding: %s/%s", req.SourceCollection, req.SourceDocID)
		}
		queryVector = rec.Vector
	} else if req.Query != "" && s.Embedding != nil {
		embedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var err error
		queryVector, err = s.Embedding.Embed(embedCtx, req.Query)
		if err != nil {
			return nil, fmt.Errorf("embedding failed: %w", err)
		}
	} else {
		return nil, fmt.Errorf("one of query or sourceDocID is required")
	}

	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}
	metric := vec.ResolveSimilarity(req.DistanceMetric)
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	// Oversample per collection for better merging (SRCH-005). No single
	// collection profile can own the factor across targets, so only the
	// request parameter and the default apply.
	searchTopK := OversampledTopK(topK, s.ResolveOversample("", req.Oversample), 20)

	// Search each target collection
	type taggedResult struct {
		collection string
		result     vec.VectorResult
	}
	var allTagged []taggedResult

	for _, coll := range req.TargetCollections {
		var results []vec.VectorResult
		if len(req.FilterMeta) > 0 {
			allowedIDs := s.getDocIDsByMeta(coll, req.FilterMeta)
			if len(allowedIDs) == 0 {
				continue
			}
			results = searcher.SearchWithFilter(coll, queryVector, searchTopK, req.Threshold, allowedIDs, metric)
		} else {
			results = searcher.Search(coll, queryVector, searchTopK, req.Threshold, metric)
		}
		results = vec.DeduplicateChunkResults(results)
		for _, vr := range results {
			allTagged = append(allTagged, taggedResult{collection: coll, result: vr})
		}
	}

	sort.Slice(allTagged, func(i, j int) bool {
		return allTagged[i].result.Score > allTagged[j].result.Score
	})
	if len(allTagged) > topK {
		allTagged = allTagged[:topK]
	}

	// Load full documents
	items := make([]CrossSearchResultItem, 0, len(allTagged))
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for rank, tr := range allTagged {
			v := bDocs.Get(storage.DocKey(tr.collection, tr.result.DocID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			doc := *docPtr
			if !req.IncludeContent {
				doc.ContentMD = ""
			}
			items = append(items, CrossSearchResultItem{
				Collection: tr.collection,
				Document:   doc,
				Score:      tr.result.Score,
				Rank:       rank + 1,
			})
		}
		return nil
	})

	return &MCPCrossSearchResponse{
		Results:           items,
		Total:             len(items),
		SourceCollection:  req.SourceCollection,
		SourceDocID:       req.SourceDocID,
		TargetCollections: req.TargetCollections,
		Algorithm:         algo,
		DistanceMetric:    metricName,
	}, nil
}

// --- Find Duplicates ---

// FindDuplicates finds duplicate documents via the direct client.
func (c *DirectClient) FindDuplicates(ctx context.Context, req *MCPFindDuplicatesRequest) (*MCPFindDuplicatesResponse, error) {
	httpReq := FindDuplicatesRequest{
		Collection:     req.Collection,
		Mode:           req.Mode,
		Threshold:      req.Threshold,
		MaxDocs:        req.MaxDocs,
		DistanceMetric: req.DistanceMetric,
		IncludeContent: req.IncludeContent,
	}
	return c.server.findDuplicates(httpReq)
}

// Ingest bulk-imports documents via the direct client.
func (c *DirectClient) Ingest(ctx context.Context, req *MCPIngestRequest) (*MCPIngestResponse, error) {
	docs := make([]IngestDocumentHTTP, len(req.Documents))
	for i, d := range req.Documents {
		docs[i] = IngestDocumentHTTP(d)
	}

	opts := IngestOptionsHTTP{
		SkipDuplicates:          req.Options.SkipDuplicates,
		SkipEmbeddings:          req.Options.SkipEmbeddings,
		SkipFTS:                 req.Options.SkipFTS,
		SkipWebhooks:            req.Options.SkipWebhooks,
		AutoConfigureCollection: req.Options.AutoConfigureCollection,
		SaveRevision:            req.Options.SaveRevision,
	}

	resp, err := c.server.ingestDocuments(ctx, req.Collection, docs, opts)
	if err != nil {
		return nil, err
	}

	return &MCPIngestResponse{
		Added:      resp.Added,
		Updated:    resp.Updated,
		Skipped:    resp.Skipped,
		Failed:     resp.Failed,
		Errors:     resp.Errors,
		Collection: resp.Collection,
		DurationMs: resp.DurationMs,
	}, nil
}

// Aggregate performs aggregation queries via the direct client.
func (c *DirectClient) Aggregate(ctx context.Context, req *AggregateRequest) (*AggregateResponse, error) {
	return c.server.aggregate(req)
}

// Close is a no-op for the direct client since the server manages its own lifecycle.
func (c *DirectClient) Close() error {
	// No-op — Server owns all resources.
	return nil
}

// --- helpers ---

// toProtoMeta converts map[string][]string to proto MetaValues map.
func toProtoMeta(meta map[string][]string) map[string]*proto.MetaValues {
	if meta == nil {
		return nil
	}
	result := make(map[string]*proto.MetaValues, len(meta))
	for k, v := range meta {
		result[k] = &proto.MetaValues{Values: v}
	}
	return result
}
