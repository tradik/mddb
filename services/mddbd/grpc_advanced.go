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
	"strings"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// automationRuleToProto converts internal AutomationRule to proto.
func automationRuleToProto(r *AutomationRule) *proto.AutomationRuleProto {
	p := &proto.AutomationRuleProto{
		Id:               r.ID,
		Type:             r.Type,
		Name:             r.Name,
		Enabled:          r.Enabled,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
		Url:              r.URL,
		Method:           r.Method,
		Headers:          r.Headers,
		Collection:       r.Collection,
		SearchType:       r.SearchType,
		Query:            r.Query,
		Threshold:        r.Threshold,
		WebhookId:        r.WebhookID,
		Events:           r.Events,
		SentimentEnabled: r.SentimentEnabled,
		SentimentMin:     r.SentimentMin,
		SentimentMax:     r.SentimentMax,
		ConditionLogic:   r.ConditionLogic,
		Schedule:         r.Schedule,
		TriggerId:        r.TriggerID,
		LastRun:          r.LastRun,
		NextRun:          r.NextRun,
	}
	if r.SearchParams != nil {
		if b, err := json.Marshal(r.SearchParams); err == nil {
			p.SearchParamsJson = string(b)
		}
	}
	return p
}

// protoToAutomationRule converts proto to internal AutomationRule.
func protoToAutomationRule(p *proto.AutomationRuleProto) AutomationRule {
	r := AutomationRule{
		ID:               p.Id,
		Type:             p.Type,
		Name:             p.Name,
		Enabled:          p.Enabled,
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
		URL:              p.Url,
		Method:           p.Method,
		Headers:          p.Headers,
		Collection:       p.Collection,
		SearchType:       p.SearchType,
		Query:            p.Query,
		Threshold:        p.Threshold,
		WebhookID:        p.WebhookId,
		Events:           p.Events,
		SentimentEnabled: p.SentimentEnabled,
		SentimentMin:     p.SentimentMin,
		SentimentMax:     p.SentimentMax,
		ConditionLogic:   p.ConditionLogic,
		Schedule:         p.Schedule,
		TriggerID:        p.TriggerId,
		LastRun:          p.LastRun,
		NextRun:          p.NextRun,
	}
	if p.SearchParamsJson != "" {
		var sp map[string]interface{}
		if err := json.Unmarshal([]byte(p.SearchParamsJson), &sp); err == nil {
			r.SearchParams = sp
		}
	}
	return r
}

// ListAutomation implements the ListAutomation RPC.
func (g *GRPCServer) ListAutomation(ctx context.Context, req *proto.ListAutomationRequest) (*proto.ListAutomationResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.AutomationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "automation not initialized")
	}
	rules := g.server.AutomationManager.List(req.Type)
	protoRules := make([]*proto.AutomationRuleProto, len(rules))
	for i := range rules {
		protoRules[i] = automationRuleToProto(&rules[i])
	}
	return &proto.ListAutomationResponse{Rules: protoRules, Total: safeInt32(len(protoRules))}, nil
}

// CreateAutomation implements the CreateAutomation RPC.
func (g *GRPCServer) CreateAutomation(ctx context.Context, req *proto.CreateAutomationRequest) (*proto.AutomationRuleProto, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.AutomationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "automation not initialized")
	}
	if req.Rule == nil {
		return nil, status.Error(codes.InvalidArgument, "missing rule")
	}
	rule := protoToAutomationRule(req.Rule)
	created, err := g.server.AutomationManager.Create(rule)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if created.Type == "cron" && g.server.CronScheduler != nil {
		g.server.CronScheduler.Reload()
	}
	return automationRuleToProto(created), nil
}

// GetAutomation implements the GetAutomation RPC.
func (g *GRPCServer) GetAutomation(ctx context.Context, req *proto.GetAutomationRequest) (*proto.AutomationRuleProto, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.AutomationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "automation not initialized")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "missing id")
	}
	rule := g.server.AutomationManager.Get(req.Id)
	if rule == nil {
		return nil, status.Error(codes.NotFound, "automation rule not found")
	}
	return automationRuleToProto(rule), nil
}

// UpdateAutomation implements the UpdateAutomation RPC.
func (g *GRPCServer) UpdateAutomation(ctx context.Context, req *proto.UpdateAutomationRequest) (*proto.AutomationRuleProto, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.AutomationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "automation not initialized")
	}
	if req.Id == "" || req.Rule == nil {
		return nil, status.Error(codes.InvalidArgument, "missing id or rule")
	}
	update := protoToAutomationRule(req.Rule)
	updated, err := g.server.AutomationManager.Update(req.Id, update)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if updated.Type == "cron" && g.server.CronScheduler != nil {
		g.server.CronScheduler.Reload()
	}
	return automationRuleToProto(updated), nil
}

// DeleteAutomation implements the DeleteAutomation RPC.
func (g *GRPCServer) DeleteAutomation(ctx context.Context, req *proto.DeleteAutomationRequest) (*proto.DeleteAutomationResponse, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.AutomationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "automation not initialized")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "missing id")
	}
	// Check if cron type for scheduler reload
	existing := g.server.AutomationManager.Get(req.Id)
	if err := g.server.AutomationManager.Delete(req.Id); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if existing != nil && existing.Type == "cron" && g.server.CronScheduler != nil {
		g.server.CronScheduler.Reload()
	}
	return &proto.DeleteAutomationResponse{Status: "deleted", Id: req.Id}, nil
}

// TestAutomation implements the TestAutomation RPC — dry run of a trigger.
func (g *GRPCServer) TestAutomation(ctx context.Context, req *proto.TestAutomationRequest) (*proto.TestAutomationResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.AutomationManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "automation not initialized")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "missing id")
	}
	rule := g.server.AutomationManager.Get(req.Id)
	if rule == nil {
		return nil, status.Error(codes.NotFound, "automation rule not found")
	}
	if rule.Type != "trigger" {
		return nil, status.Error(codes.InvalidArgument, "only trigger rules can be tested")
	}

	matches, err := g.server.AutomationManager.RunTrigger(rule)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Load matched documents
	var protoDocs []*proto.Document
	_ = g.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for _, m := range matches {
			v := bDocs.Get(storage.DocKey(m.Collection, m.DocID))
			if v == nil {
				continue
			}
			d, err := unmarshalDoc(v)
			if err != nil {
				continue
			}
			protoDocs = append(protoDocs, docToProto(d))
		}
		return nil
	})

	return &proto.TestAutomationResponse{
		Trigger: automationRuleToProto(rule),
		Matches: protoDocs,
		Total:   safeInt32(len(protoDocs)),
	}, nil
}

// GetAutomationLogs implements the GetAutomationLogs RPC.
func (g *GRPCServer) GetAutomationLogs(ctx context.Context, req *proto.GetAutomationLogsRequest) (*proto.GetAutomationLogsResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.AutomationLogStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "automation logs not initialized")
	}

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 50
	}

	logs, nextCursor, err := g.server.AutomationLogStore.List(limit, req.Cursor, req.RuleId, req.Status)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	total, _ := g.server.AutomationLogStore.Count(req.RuleId, req.Status)

	protoLogs := make([]*proto.AutomationLogEntryProto, len(logs))
	for i, l := range logs {
		protoLogs[i] = &proto.AutomationLogEntryProto{
			Id:         l.ID,
			Timestamp:  l.Timestamp,
			RuleId:     l.RuleID,
			RuleName:   l.RuleName,
			RuleType:   l.RuleType,
			WebhookId:  l.WebhookID,
			WebhookUrl: l.WebhookURL,
			Status:     l.Status,
			HttpStatus: safeInt32(l.HTTPStatus),
			DurationMs: l.DurationMs,
			Error:      l.Error,
			Attempt:    safeInt32(l.Attempt),
		}
	}

	return &proto.GetAutomationLogsResponse{
		Logs:       protoLogs,
		Total:      safeInt32(total),
		NextCursor: nextCursor,
		HasMore:    nextCursor != "",
	}, nil
}

// GetCollectionConfig implements the GetCollectionConfig RPC.
func (g *GRPCServer) GetCollectionConfig(ctx context.Context, req *proto.GetCollectionConfigRequest) (*proto.GetCollectionConfigResponse, error) {
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.CollectionManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "collection manager not initialized")
	}
	cfg, found := g.server.CollectionManager.Get(req.Collection)
	resp := &proto.GetCollectionConfigResponse{
		Collection: req.Collection,
		Configured: found,
	}
	if found && cfg != nil {
		// GO-035: every field, secrets masked. See grpc_collection_config.go.
		resp.Config = collectionConfigToProto(cfg)
	}
	return resp, nil
}

// SetCollectionConfig implements the SetCollectionConfig RPC.
func (g *GRPCServer) SetCollectionConfig(ctx context.Context, req *proto.SetCollectionConfigRequest) (*proto.SetCollectionConfigResponse, error) {
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
	if g.server.CollectionManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "collection manager not initialized")
	}
	if req.MaxRevisions < 0 {
		return nil, status.Error(codes.InvalidArgument, "max_revisions must be >= 0")
	}
	// Merge into the stored config rather than replacing it.
	//
	// CollectionManager.Set writes whatever struct it is handed. Building a
	// fresh struct here erased everything the request did not mention — a
	// client updating an icon silently cleared storageBackend, quantization
	// and, worst of all, Encrypted, whose false value is pushed straight into
	// the encryptor, so the next document in an encrypted collection was
	// written as plaintext.
	//
	// GO-035 closed the proto gap, and the merge stays: a client that omits a
	// field means "leave it alone", not "clear it". That is also why the new
	// booleans carry proto3 presence — without it, closing the gap would have
	// reintroduced the bug through the fields that were just added.
	cfg := &CollectionConfig{}
	if existing, found := g.server.CollectionManager.Get(req.Collection); found && existing != nil {
		*cfg = *existing
	}
	cfg.Type = req.Type
	cfg.Description = req.Description
	cfg.Icon = req.Icon
	cfg.Color = req.Color
	cfg.CustomMeta = req.CustomMeta
	cfg.MaxRevisions = int(req.MaxRevisions)

	// RAG-002: proto3 cannot tell an omitted string from an empty one, so a
	// gRPC client clearing the prompt and one not mentioning it look
	// identical. Treated as "set it", which matches the REST PUT semantics
	// this RPC mirrors — the merge above exists for fields gRPC cannot
	// express at all, not for fields a client chose to leave blank.
	cfg.ResponsePrompt = req.ResponsePrompt
	if err := ValidateResponsePrompt(cfg.ResponsePrompt); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// GO-035: the fields the proto gained in v2.12.0, each with the same
	// "omitted means leave it alone" rule.
	applyCollectionConfigRequest(cfg, req)

	// RAG-001: a nil retrieval block means "not sent", which leaves any
	// profile configured over REST in place.
	if req.Retrieval != nil {
		profile := retrievalProfileFromProto(req.Retrieval)
		if err := profile.Validate(); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		cfg.Retrieval = profile
	}

	if err := g.server.CollectionManager.Set(req.Collection, cfg); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.SetCollectionConfigResponse{Status: "ok", Collection: req.Collection}, nil
}

// ListCollectionConfigs implements the ListCollectionConfigs RPC.
func (g *GRPCServer) ListCollectionConfigs(ctx context.Context, req *proto.ListCollectionConfigsRequest) (*proto.ListCollectionConfigsResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.CollectionManager == nil {
		return nil, status.Error(codes.FailedPrecondition, "collection manager not initialized")
	}
	all := g.server.CollectionManager.ListAll()
	tenant := TenantFromContext(ctx)
	entries := make([]*proto.CollectionConfigEntry, 0, len(all))
	for coll, cfg := range all {
		if !CollectionInTenant(tenant, coll) {
			continue
		}
		entries = append(entries, &proto.CollectionConfigEntry{
			Collection: coll,
			// GO-035: same full, secret-masked rendering as the single-config
			// read. A listing that showed fewer fields than a Get would be its
			// own kind of drift.
			Config: collectionConfigToProto(cfg),
		})
	}
	return &proto.ListCollectionConfigsResponse{Configs: entries, Total: safeInt32(len(entries))}, nil
}

// CrossSearch implements the CrossSearch RPC — cross-collection vector search.
func (g *GRPCServer) CrossSearch(ctx context.Context, req *proto.CrossSearchRequest) (*proto.CrossSearchResponse, error) {
	if len(req.TargetCollections) == 0 {
		return nil, status.Error(codes.InvalidArgument, "missing target_collections")
	}
	if req.Query == "" && len(req.QueryVector) == 0 && req.SourceDocId == "" {
		return nil, status.Error(codes.InvalidArgument, "one of query, query_vector, or source_doc_id is required")
	}

	// Check read permission on all collections
	if g.server.AuthManager != nil {
		for _, coll := range req.TargetCollections {
			if err := g.server.AuthManager.CheckPermission(ctx, coll, PermRead); err != nil {
				return nil, status.Error(codes.PermissionDenied, err.Error())
			}
		}
		if req.SourceCollection != "" {
			if err := g.server.AuthManager.CheckPermission(ctx, req.SourceCollection, PermRead); err != nil {
				return nil, status.Error(codes.PermissionDenied, err.Error())
			}
		}
	}

	// Select algorithm
	algo := req.Algorithm
	if algo == "" {
		algo = "flat"
	}
	searcher, algoOk := g.server.VectorSearchers[algo]
	if !algoOk {
		return nil, status.Error(codes.InvalidArgument, "unknown algorithm: "+algo)
	}
	if !searcher.IsReady() {
		searcher = g.server.VectorSearchers["flat"]
		algo = "flat"
	}
	if !searcher.IsReady() {
		return nil, status.Error(codes.Unavailable, "vector index is loading")
	}

	// Resolve query vector
	var queryVector []float32
	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if req.SourceDocId != "" {
		if req.SourceCollection == "" {
			return nil, status.Error(codes.InvalidArgument, "source_collection required when using source_doc_id")
		}
		rec, err := g.server.VectorStore.Get(req.SourceCollection, req.SourceDocId)
		if err != nil || rec == nil {
			return nil, status.Error(codes.NotFound, "source document has no embedding")
		}
		queryVector = rec.Vector
	} else if g.server.Embedding != nil {
		var err error
		queryVector, err = g.server.Embedding.Embed(ctx, req.Query, embedding.RoleQuery)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to embed query: "+err.Error())
		}
	} else {
		return nil, status.Error(codes.FailedPrecondition, "no embedding provider configured")
	}

	// RAG-001 deliberately does not apply here: a cross-collection search
	// has no single collection whose profile could own topK, and picking
	// one of N arbitrarily would be worse than the fixed default.
	topK := int(req.TopK)
	if topK <= 0 {
		topK = 10
	}

	metric := vec.ResolveSimilarity(req.DistanceMetric)
	metricName := req.DistanceMetric
	if metricName == "" {
		metricName = "cosine"
	}

	// Convert proto filter_meta
	filterMeta := make(map[string][]string)
	for k, v := range req.FilterMeta {
		filterMeta[k] = v.Values
	}

	searchTopK := topK * 3
	if searchTopK < 20 {
		searchTopK = 20
	}

	type taggedResult struct {
		collection string
		result     vec.VectorResult
	}
	var allTagged []taggedResult

	for _, coll := range req.TargetCollections {
		var results []vec.VectorResult
		if len(filterMeta) > 0 {
			allowedIDs := g.server.getDocIDsByMeta(coll, filterMeta)
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
	protoResults := make([]*proto.CrossSearchResultItem, 0, len(allTagged))
	_ = g.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for rank, tr := range allTagged {
			v := bDocs.Get(storage.DocKey(tr.collection, tr.result.DocID))
			if v == nil {
				continue
			}
			d, err := unmarshalDoc(v)
			if err != nil {
				continue
			}
			if !req.IncludeContent {
				d.ContentMD = ""
			}
			protoResults = append(protoResults, &proto.CrossSearchResultItem{
				Collection: tr.collection,
				Document:   docToProto(d),
				Score:      tr.result.Score,
				Rank:       int32(rank + 1),
			})
		}
		return nil
	})

	return &proto.CrossSearchResponse{
		Results:           protoResults,
		Total:             safeInt32(len(protoResults)),
		SourceCollection:  req.SourceCollection,
		SourceDocId:       req.SourceDocId,
		TargetCollections: req.TargetCollections,
		Algorithm:         algo,
		DistanceMetric:    metricName,
	}, nil
}

// FindDuplicates implements the FindDuplicates RPC.
func (g *GRPCServer) FindDuplicates(ctx context.Context, req *proto.FindDuplicatesRequest) (*proto.FindDuplicatesResponse, error) {
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	mode := req.Mode
	if mode == "" {
		mode = "both"
	}
	if mode != "exact" && mode != "similar" && mode != "both" {
		return nil, status.Error(codes.InvalidArgument, "mode must be 'exact', 'similar', or 'both'")
	}
	threshold := req.Threshold
	if threshold <= 0 {
		threshold = 0.9
	}
	maxDocs := int(req.MaxDocs)
	if maxDocs <= 0 {
		maxDocs = 5000
	}

	internalReq := FindDuplicatesRequest{
		Collection:     req.Collection,
		Mode:           mode,
		Threshold:      threshold,
		MaxDocs:        maxDocs,
		DistanceMetric: req.DistanceMetric,
		IncludeContent: req.IncludeContent,
	}

	resp, err := g.server.findDuplicates(internalReq)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	convertGroup := func(groups []DuplicateGroup) []*proto.DuplicateGroupProto {
		result := make([]*proto.DuplicateGroupProto, len(groups))
		for i, g := range groups {
			docs := make([]*proto.DuplicateDocInfoProto, len(g.Documents))
			for j, d := range g.Documents {
				docs[j] = &proto.DuplicateDocInfoProto{
					DocId:       d.DocID,
					Key:         d.Key,
					ContentHash: d.ContentHash,
					ContentMd:   d.ContentMD,
					Score:       d.Score,
				}
			}
			result[i] = &proto.DuplicateGroupProto{
				GroupId:   safeInt32(g.GroupID),
				Type:      g.Type,
				Documents: docs,
				Score:     g.Score,
			}
		}
		return result
	}

	return &proto.FindDuplicatesResponse{
		Collection:      resp.Collection,
		Mode:            resp.Mode,
		Threshold:       resp.Threshold,
		DistanceMetric:  resp.DistanceMetric,
		TotalDocuments:  safeInt32(resp.TotalDocuments),
		TotalEmbedded:   safeInt32(resp.TotalEmbedded),
		ExactGroups:     convertGroup(resp.ExactGroups),
		SimilarGroups:   convertGroup(resp.SimilarGroups),
		ExactDuplicates: safeInt32(resp.ExactDuplicates),
		SimilarPairs:    safeInt32(resp.SimilarPairs),
	}, nil
}

// ListRevisions implements the ListRevisions RPC — list revision history for a document.
func (g *GRPCServer) ListRevisions(ctx context.Context, req *proto.ListRevisionsRequest) (*proto.ListRevisionsResponse, error) {
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, key, lang")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	docID := genID(req.Collection, req.Key, req.Lang)

	var revisions []*proto.RevisionEntryProto
	err := g.server.DBView(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		if bRev == nil {
			return nil
		}
		prefix := storage.RevPrefix(req.Collection, docID)
		c := bRev.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			keyStr := string(k)
			lastPipe := strings.LastIndexByte(keyStr, '|')
			if lastPipe < 0 || lastPipe >= len(keyStr)-1 {
				continue
			}
			ts, err := strconv.ParseInt(keyStr[lastPipe+1:], 10, 64)
			if err != nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			protoMeta := make(map[string]*proto.MetaValues)
			for mk, mv := range docPtr.Meta {
				protoMeta[mk] = &proto.MetaValues{Values: mv}
			}
			revisions = append(revisions, &proto.RevisionEntryProto{
				Timestamp: ts,
				UpdatedAt: docPtr.UpdatedAt,
				ContentMd: docPtr.ContentMD,
				Meta:      protoMeta,
			})
		}
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Sort newest first
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Timestamp > revisions[j].Timestamp
	})

	return &proto.ListRevisionsResponse{
		Collection: req.Collection,
		Key:        req.Key,
		Lang:       req.Lang,
		Revisions:  revisions,
		Total:      safeInt32(len(revisions)),
	}, nil
}

// RestoreRevision implements the RestoreRevision RPC — restore a document from a specific revision.
func (g *GRPCServer) RestoreRevision(ctx context.Context, req *proto.RestoreRevisionRequest) (*proto.Document, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" || req.Timestamp == 0 {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, key, lang, timestamp")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	docID := genID(req.Collection, req.Key, req.Lang)
	tsKey := fmt.Sprintf("%020d", req.Timestamp)
	revKey := append(storage.RevPrefix(req.Collection, docID), []byte(tsKey)...)

	var revDoc *storage.Doc
	err := g.server.DBView(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		if bRev == nil {
			return errors.New("revision not found")
		}
		v := bRev.Get(revKey)
		if v == nil {
			return fmt.Errorf("revision not found for timestamp %d", req.Timestamp)
		}
		var err error
		revDoc, err = loadDoc(v)
		return err
	})
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}

	doc, _, err := g.server.addDocument(req.Collection, req.Key, req.Lang, revDoc.Meta, revDoc.ContentMD, 0, true)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return docToProto(&doc), nil
}
