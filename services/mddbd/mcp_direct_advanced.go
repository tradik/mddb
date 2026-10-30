package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mddb/internal/embedding"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
	proto "mddb/proto"
	"sort"
	"strconv"
	"time"

	json "mddb/internal/jsonx"

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
	err := c.server.DBViewC(collection, func(tx *bolt.Tx) error {
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
	doc, err := c.server.restoreRevision(collection, key, lang, timestamp)
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
		// GO-035: credentials masked. This path answers an LLM, which may
		// repeat what it is given into a transcript, a log or a reply — the
		// one audience for which handing over an S3 secret key is least
		// recoverable.
		Config:     redactCollectionConfig(cfg),
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
	// Only what was sent changes. These six used to be assigned
	// unconditionally, so an agent setting a description erased the icon,
	// the colour and the custom meta — and reset maxRevisions to unlimited,
	// since the tool never sent it — two lines below the comment saying the
	// config is merged.
	if req.Type != "" {
		cfg.Type = req.Type
	}
	if req.Description != "" {
		cfg.Description = req.Description
	}
	if req.Icon != "" {
		cfg.Icon = req.Icon
	}
	if req.Color != "" {
		cfg.Color = req.Color
	}
	if req.CustomMeta != nil {
		cfg.CustomMeta = req.CustomMeta
	}
	if req.MaxRevisions != nil {
		if *req.MaxRevisions < 0 {
			return errors.New("invalid maxRevisions: must be >= 0 (0 = unlimited)")
		}
		cfg.MaxRevisions = *req.MaxRevisions
	}
	if req.Code != nil {
		if err := req.Code.Validate(); err != nil {
			return err
		}
		cfg.Code = req.Code
	}
	if req.Embedding != nil {
		if err := validateChunking(req.Embedding); err != nil {
			return err
		}
		cfg.Embedding = req.Embedding
	}
	if req.FTS != nil {
		cfg.FTS = req.FTS
	}
	// GO-035: a nil target means "not sent". Assigning it unconditionally
	// deleted a configured publishing target whenever an agent touched
	// anything else — the same shape as the bug the merge above exists to
	// stop, one line below the comment describing it.
	if req.WordPress != nil {
		cfg.WordPress = req.WordPress
	}

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

	// GO-043: a change needs a global admin; nil keeps the stored quota.
	if err := c.server.checkQuotaChange(ctx, req.Collection, req.Quota); err != nil {
		return err
	}
	cfg.Quota = req.Quota

	// GO-035: an agent that read this config back saw masked credentials, so
	// an empty one means "unchanged".
	if stored, found := c.server.CollectionManager.Get(req.Collection); found {
		carryOverSecrets(cfg, stored)
	} else {
		carryOverSecrets(cfg, nil)
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
	// GO-035: masked, same reasoning as the single-config read.
	redacted := make(map[string]*CollectionConfig, len(all))
	for collection, cfg := range all {
		redacted[collection] = redactCollectionConfig(cfg)
	}
	return &MCPCollectionConfigListResponse{
		Configs: redacted,
		Total:   len(redacted),
	}, nil
}

// --- Cross-Collection Search ---

// CrossSearch searches across multiple collections via the direct client.
// MCP and gRPC run it; every answer reports a limit the cap lowered (#368).
func (c *DirectClient) CrossSearch(ctx context.Context, req *MCPCrossSearchRequest) (*MCPCrossSearchResponse, error) {
	resp, err := c.crossSearch(ctx, req)
	if resp != nil {
		resp.LimitReport = req.limitReport
		if resp.Passages != nil {
			resp.Passages.LimitReport = req.limitReport
		}
	}
	return resp, err
}

// crossSearch is CrossSearch before the limit report is attached.
func (c *DirectClient) crossSearch(ctx context.Context, req *MCPCrossSearchRequest) (*MCPCrossSearchResponse, error) {
	s := c.server
	if err := req.GroupBy.Validate(); err != nil {
		return nil, err
	}
	if err := req.ValidateIndexWait(); err != nil {
		return nil, err
	}
	if err := req.ValidatePassageOptions(); err != nil {
		return nil, err
	}
	if err := validCrossSearchType(req.SearchType); err != nil {
		return nil, err
	}
	if err := req.ValidatePlanOptions(); err != nil {
		return nil, err
	}
	if err := req.ValidateFeedback(); err != nil {
		return nil, err
	}
	req.TargetCollections = s.expandTargets(req.TargetCollections)
	if len(req.TargetCollections) == 0 {
		return nil, fmt.Errorf("no collection matches target_collections")
	}
	topK, limitReport := s.crossTopK(req.TargetCollections, req.TopK) // #368
	req.limitReport = limitReport
	if req.SearchType == CrossSearchFTS || req.SearchType == CrossSearchHybrid {
		if req.Query == "" {
			return nil, fmt.Errorf("searchType %s needs a query", req.SearchType)
		}
		waited := s.waitForIndexAll(ctx, req.TargetCollections, req.IndexWait)
		items := s.crossTextItems(ctx, crossTextParams{SearchType: req.SearchType, Query: req.Query,
			Targets: req.TargetCollections, TopK: topK, FilterMeta: req.FilterMeta, Filter: req.Filter,
			GroupBy: req.GroupBy, IncludeContent: req.IncludeContent || req.wantsPassages()})
		s.countCross("", items, req.wantsPassages())
		if req.wantsPassages() {
			p := s.crossPassages(req.Query, items, req.PassageOptions, waited)
			s.passageImages(&p, req.IncludeImages, nil) // RAG-010
			return &MCPCrossSearchResponse{Passages: &p}, nil
		}
		s.crossImages(items, req.IncludeImages) // RAG-010
		return &MCPCrossSearchResponse{Results: items, Total: len(items), TargetCollections: req.TargetCollections,
			Algorithm: req.SearchType, Indexing: waited}, nil
	}

	// Oversample per collection for better merging (SRCH-005). No single
	// collection profile can own the factor across targets, so only the
	// request parameter and the default apply.
	searchTopK := OversampledTopK(topK, s.ResolveOversample("", req.Oversample), 20)
	// The readiness rule every vector path shares: algo, else flat, else
	// "loading" (which gRPC reports as Unavailable). SRCH-013: "auto" plans
	// each target.
	searcher, algo, plan, err := s.crossSearcher(req.SourceCollection, req.Algorithm, searchTopK)
	if err != nil {
		return nil, err
	}

	// Resolve query vector
	var queryVector []float32
	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if req.SourceDocID != "" {
		if req.SourceCollection == "" {
			return nil, fmt.Errorf("sourceCollection required when using sourceDocID")
		}
		rec, err := s.VectorStore.Get(req.SourceCollection, req.SourceDocID)
		if err != nil || rec == nil {
			return nil, fmt.Errorf("%w: %s/%s", errSourceNoEmbedding, req.SourceCollection, req.SourceDocID)
		}
		queryVector = rec.Vector
	} else if req.Query != "" && s.Embedding != nil {
		embedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		var err error
		queryVector, err = s.Embedding.Embed(embedCtx, req.Query, embedding.RoleQuery)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errEmbedQuery, err)
		}
	} else if req.Query != "" {
		return nil, errNoEmbeddingProvider
	} else {
		return nil, fmt.Errorf("one of query, queryVector or sourceDocID is required")
	}
	// SRCH-015: the examples as vectors; query mode corrects the query here.
	fb, queryVector, err := s.crossFeedback(req.FeedbackOptions, req.SourceCollection, req.TargetCollections, queryVector, plan)
	if err != nil {
		return nil, err
	}

	metric := vec.ResolveSimilarity(req.DistanceMetric)
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	waited := s.waitForIndexAll(ctx, req.TargetCollections, req.IndexWait)
	items := s.crossSearchItems(crossSearchParams{
		Searcher: searcher, QueryVector: queryVector, Targets: req.TargetCollections,
		SearchTopK: searchTopK, TopK: topK, Threshold: req.Threshold, Metric: metric,
		FilterMeta: req.FilterMeta, Filter: req.Filter, GroupBy: req.GroupBy,
		IncludeContent: req.IncludeContent || req.wantsPassages(),
		Plan:           plan, LatencyTargetMs: req.LatencyTargetMs, Feedback: fb,
	})
	s.countCross(algo, items, req.wantsPassages())

	if req.wantsPassages() {
		p := s.crossPassages(req.Query, items, req.PassageOptions, waited)
		s.passageImages(&p, req.IncludeImages, nil) // RAG-010
		return &MCPCrossSearchResponse{Passages: &p}, nil
	}
	s.crossImages(items, req.IncludeImages) // RAG-010
	return &MCPCrossSearchResponse{
		Results:           items,
		Total:             len(items),
		SourceCollection:  req.SourceCollection,
		SourceDocID:       req.SourceDocID,
		TargetCollections: req.TargetCollections,
		Algorithm:         algo,
		DistanceMetric:    metricName,
		Indexing:          waited,
		Plan:              req.explained(plan),
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
		TextOnly:                req.Options.TextOnly,
		ExtractImages:           req.Options.ExtractImages,
		FetchRemoteImages:       req.Options.FetchRemoteImages,
	}

	resp, err := c.server.ingestDocuments(ctx, req.Collection, docs, opts)
	if err != nil {
		return nil, err
	}

	return &MCPIngestResponse{
		Added:       resp.Added,
		Updated:     resp.Updated,
		Skipped:     resp.Skipped,
		Failed:      resp.Failed,
		Errors:      resp.Errors,
		Collection:  resp.Collection,
		DurationMs:  resp.DurationMs,
		Conversions: resp.Conversions,
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
