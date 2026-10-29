package main

import (
	"context"
	"errors"
	"math"
	"mddb/internal/storage"
	proto "mddb/proto"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// VectorSearch implements the VectorSearch RPC
func (g *GRPCServer) VectorSearch(ctx context.Context, req *proto.VectorSearchRequest) (*proto.VectorSearchResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	// SRCH-005: InvalidArgument is the gRPC equivalent of the REST 422.
	if err := ValidateOversample(req.Oversample); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if req.Query == "" && len(req.QueryVector) == 0 && req.QueryImage == "" {
		return nil, status.Error(codes.InvalidArgument, "either query or query_vector is required")
	}
	// The search MCP and REST run (2.18.8): filter, groupBy, passages,
	// retrieval modes, waits and the context budget come with it.
	res, err := NewDirectClient(g.server).VectorSearch(ctx, &MCPVectorSearchRequest{
		Collection: req.Collection, Query: req.Query, QueryVector: req.QueryVector, TopK: int(req.TopK),
		Threshold: req.Threshold, FilterMeta: metaValuesFromProto(req.FilterMeta), Filter: metaFilterFromProto(req.Filter),
		IncludeContent: req.IncludeContent, Algorithm: req.Algorithm, RetrievalMode: req.RetrievalMode,
		WindowSize: int(req.WindowSize), Oversample: req.Oversample, GroupBy: groupByOf(req.GroupBy),
		IndexWait:       indexWait(req.WaitForIndexMs, req.WaitForKeys, req.ReadLevel),
		PassageOptions:  passageOptions(req.Format, req.PassageLines),
		PlanOptions:     planOptions(req.Explain, req.LatencyTargetMs),
		FeedbackOptions: feedbackOptions(req.Positive, req.Negative, req.FeedbackMode, req.FeedbackWeight, req.Rocchio),
		ImageOptions:    ImageOptions{IncludeImages: req.IncludeImages}, // RAG-010
		QueryImage:      req.QueryImage,
	})
	if err != nil {
		return nil, searchStatus(err)
	}
	resp := &proto.VectorSearchResponse{Total: safeInt32(res.Total), Model: res.Model, Dimensions: safeInt32(res.Dimensions),
		Algorithm: res.Algorithm, Indexing: indexWaitToProto(res.Indexing), ContextTruncated: res.ContextTruncated,
		Plan: planToProto(res.Plan), ImageHits: imageRefsToProto(res.ImageHits)}
	if res.Passages != nil {
		resp.Passages, resp.Total, resp.Indexing = passagesOf(res.Passages), safeInt32(res.Passages.Total), indexWaitToProto(res.Passages.Indexing)
		resp.ImageHits = imageRefsToProto(res.Passages.ImageHits)
		return resp, nil
	}
	for _, r := range res.Results {
		resp.Results = append(resp.Results, &proto.VectorSearchResult{Document: mcpDocToProto(r.Document, true), Score: r.Score,
			Rank: safeInt32(r.Rank), ChunkText: r.ChunkText, StartLine: safeInt32(r.StartLine), EndLine: safeInt32(r.EndLine),
			Group: r.Group, GroupRank: safeInt32(r.GroupRank), PendingEmbedding: r.PendingEmbedding, Images: imageRefsToProto(r.Images)})
	}
	return resp, nil
}

// VectorReindex implements the VectorReindex RPC
func (g *GRPCServer) VectorReindex(ctx context.Context, req *proto.VectorReindexRequest) (*proto.VectorReindexResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
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
	if g.server.Embedding == nil {
		return nil, status.Error(codes.FailedPrecondition, "no embedding provider configured")
	}

	// #347: the shared implementation. This copy stored one whole-document
	// vector instead of chunks and never touched the in-memory indexes.
	res, err := g.server.reindexVectors(ctx, req.Collection, req.Force, nil)
	if err != nil {
		return nil, grpcError(codes.Internal, err) // embedding budget → RESOURCE_EXHAUSTED
	}
	return &proto.VectorReindexResponse{
		Embedded: safeInt32(res.Embedded),
		Skipped:  safeInt32(res.Skipped),
		Failed:   safeInt32(res.Failed),
		Errors:   res.Errors,
	}, nil
}

// VectorStats implements the VectorStats RPC
func (g *GRPCServer) VectorStats(ctx context.Context, req *proto.VectorStatsRequest) (*proto.VectorStatsResponse, error) {
	// Check admin permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	resp := &proto.VectorStatsResponse{
		Enabled:     g.server.Embedding != nil,
		Collections: make(map[string]*proto.VectorCollectionStats),
	}

	if g.server.Embedding != nil {
		resp.Provider = g.server.Embedding.Model()
		resp.Model = g.server.Embedding.Model()
		resp.Dimensions = safeInt32(g.server.Embedding.Dimensions())
	}

	// #349: counts from the shared snapshot, not a walk per call.
	docCounts, vectorCounts := map[string]int{}, map[string]int{}
	if snap := g.server.dbStats(statsWait()); snap != nil {
		for name, cc := range snap.Collections {
			if cc.Documents > 0 || cc.Embeddings > 0 {
				docCounts[name], vectorCounts[name] = cc.Documents, cc.Embeddings
			}
		}
	}

	// A tenant user sees only its namespace, as in Stats.
	tenant := TenantFromContext(ctx)
	allColls := make(map[string]bool)
	for c := range docCounts {
		if CollectionInTenant(tenant, c) {
			allColls[c] = true
		}
	}

	for coll := range allColls {
		resp.Collections[coll] = &proto.VectorCollectionStats{
			TotalDocuments:    safeInt32(docCounts[coll]),
			EmbeddedDocuments: safeInt32(vectorCounts[coll]),
		}
	}

	return resp, nil
}

// Helper: convert internal storage.Doc to proto Document
// withRevision sets a document's revision (INT-021). Only reads and writes of
// whole documents carry one: a search result may have had its body dropped,
// and a revision computed from that would be wrong.
func withRevision(d *proto.Document, revision string) *proto.Document {
	d.Revision = revision
	return d
}

func docToProto(doc *storage.Doc) *proto.Document {
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
		ExpiresAt: doc.ExpiresAt,
	}
}

// DeleteBatch implements the DeleteBatch RPC - deletes multiple documents in a single transaction
func (g *GRPCServer) DeleteBatch(ctx context.Context, req *proto.DeleteBatchRequest) (*proto.DeleteBatchResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
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

	resp, err := g.batchDeleter.ProcessBatchDelete(ctx, req.Collection, req.Documents)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return resp, nil
}

// UpdateBatch implements the UpdateBatch RPC - updates multiple documents in a single transaction
func (g *GRPCServer) UpdateBatch(ctx context.Context, req *proto.UpdateBatchRequest) (*proto.UpdateBatchResponse, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
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

	resp, err := g.batchUpdater.ProcessBatchUpdate(ctx, req.Collection, req.Documents)
	if err != nil {
		return nil, grpcError(codes.Internal, err) // GO-043: quota → RESOURCE_EXHAUSTED
	}

	return resp, nil
}

// ImportURL implements the ImportURL RPC
func (g *GRPCServer) ImportURL(ctx context.Context, req *proto.ImportURLRequest) (*proto.Document, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
	}

	// Check write permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" || req.Url == "" || req.Lang == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, url, lang")
	}

	key := req.Key
	if key == "" {
		key = deriveKeyFromURL(req.Url)
		if key == "" {
			return nil, status.Error(codes.InvalidArgument, "cannot derive key from URL; provide key explicitly")
		}
	}

	content, err := fetchURL(ctx, req.Url)
	if err != nil {
		return nil, status.Error(codes.Internal, "fetch failed: "+err.Error())
	}

	fmMeta, body := parseFrontmatter(content)
	mergedMeta := fmMeta
	if mergedMeta == nil {
		mergedMeta = make(map[string][]string)
	}
	for k, v := range req.Meta {
		mergedMeta[k] = v.Values
	}

	saved, _, err := g.server.addDocument(req.Collection, key, req.Lang, mergedMeta, body, req.Ttl, true)
	if err != nil {
		return nil, grpcError(codes.Internal, err)
	}

	return docToProto(&saved), nil
}

// SetTTL implements the SetTTL RPC
func (g *GRPCServer) SetTTL(ctx context.Context, req *proto.SetTTLRequest) (*proto.Document, error) {
	if err := g.checkWritable(); err != nil {
		return nil, err
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

	updated, err := g.server.setDocumentTTL(req.Collection, req.Key, req.Lang, req.Ttl)
	if errors.Is(err, errNotFound) {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return docToProto(&updated), nil
}

// FTS implements the FTS RPC
func (g *GRPCServer) FTS(ctx context.Context, req *proto.FTSRequest) (*proto.FTSResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if req.Collection == "" || req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, query")
	}
	if g.server.FTSIndex == nil {
		return nil, status.Error(codes.FailedPrecondition, "full-text search not initialized")
	}
	// The search POST /v1/fts runs (2.18.8): curation, boosts, facets,
	// filter, groupBy, passages and the context budget come with it.
	fr := FTSSearchRequest{Collection: req.Collection, Query: req.Query, Limit: int(req.Limit), Algorithm: req.Algorithm,
		Fuzzy: int(req.Fuzzy), FilterMeta: metaValuesFromProto(req.FilterMeta), Filter: metaFilterFromProto(req.Filter),
		Lang: req.Lang, Boost: req.Boost, FacetBy: req.FacetBy, FacetMaxValues: int(req.FacetMaxValues),
		GroupBy: groupByOf(req.GroupBy), PassageOptions: passageOptions(req.Format, req.PassageLines),
		BM25K1: req.Bm25K1, BM25B: req.Bm25B, ImageOptions: ImageOptions{IncludeImages: req.IncludeImages}}
	if err := g.server.normalizeFTSRequest(&fr); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	res, err := g.server.searchFTS(fr, time.Now())
	if err != nil {
		return nil, searchStatus(err)
	}
	resp := &proto.FTSResponse{Total: safeInt32(res.Total), Algorithm: res.Algorithm, Fuzzy: safeInt32(res.Fuzzy),
		Lang: res.Lang, ContextTruncated: res.ContextTruncated}
	if res.passages != nil {
		resp.Passages, resp.Total = passagesOf(res.passages), safeInt32(res.passages.Total)
		return resp, nil
	}
	for _, r := range res.Results {
		d := r.Document
		resp.Results = append(resp.Results, &proto.FTSResult{Document: docToProto(&d), Score: r.Score,
			MatchedTerms: r.MatchedTerms, Pinned: r.Pinned, Group: r.Group, GroupRank: safeInt32(r.GroupRank),
			Images: imageRefsToProto(r.Images)})
	}
	if len(res.Facets) > 0 {
		resp.Facets = facetsToProto(res.Facets)
	}
	return resp, nil
}

// FTSReindex implements the FTSReindex RPC — re-indexes all documents using their lang field.
func (g *GRPCServer) FTSReindex(ctx context.Context, req *proto.FTSReindexRequest) (*proto.FTSReindexResponse, error) {
	// GO-009: FTSReindex rewrites the FTS index — a write — so it must honour
	// read-only mode like every other mutating RPC, or a read-only replica
	// could clobber its index and race the replication applier.
	if err := g.checkWritable(); err != nil {
		return nil, err
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required field: collection")
	}
	if g.server.FTSIndex == nil {
		return nil, status.Error(codes.FailedPrecondition, "full-text search not initialized")
	}

	// GO-009: a read error is an error, and a failed document makes the
	// status "partial" rather than "ok".
	res, err := g.server.reindexCollectionFTS(req.Collection)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &proto.FTSReindexResponse{
		Status:    res.Status,
		Reindexed: safeInt32(res.Reindexed),
		Skipped:   safeInt32(res.Skipped + res.Failed),
	}, nil
}

// FTSLanguages implements the FTSLanguages RPC — returns supported languages.
func (g *GRPCServer) FTSLanguages(ctx context.Context, _ *proto.FTSLanguagesRequest) (*proto.FTSLanguagesResponse, error) {
	if g.server.FTSIndex == nil || g.server.FTSIndex.LangRegistry() == nil {
		return &proto.FTSLanguagesResponse{}, nil
	}

	var langs []*proto.FTSLanguageInfo
	for _, code := range g.server.FTSIndex.LangRegistry().Languages() {
		cfg := g.server.FTSIndex.LangRegistry().Resolve(code)
		name := code
		if cfg != nil {
			name = cfg.Name
		}
		langs = append(langs, &proto.FTSLanguageInfo{Code: code, Name: name})
	}

	return &proto.FTSLanguagesResponse{
		Languages:   langs,
		DefaultLang: g.server.FTSIndex.LangRegistry().DefaultLang(),
	}, nil
}

// HybridSearch implements the HybridSearch RPC - combines FTS and vector search
func (g *GRPCServer) HybridSearch(ctx context.Context, req *proto.HybridSearchRequest) (*proto.HybridSearchResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if req.Collection == "" || req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, query")
	}
	if req.Strategy != "" && req.Strategy != "alpha" && req.Strategy != "rrf" {
		return nil, status.Error(codes.InvalidArgument, "unknown strategy: "+req.Strategy+", available: alpha, rrf")
	}
	// The gRPC request carries no geo filter, so "distance" never applies here.
	switch req.Sort {
	case "", "combined":
	case "distance":
		return nil, status.Error(codes.InvalidArgument, "sort=distance is only supported over HTTP where a geo filter can be attached")
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown sort: "+req.Sort+", available: combined, distance")
	}
	// The hybrid search MCP runs (2.18.8): curation, filter, groupBy,
	// passages, waits and the context budget come with it.
	res, err := NewDirectClient(g.server).HybridSearch(ctx, &MCPHybridSearchRequest{
		Collection: req.Collection, Query: req.Query, TopK: int(req.TopK), Algorithm: req.Algorithm,
		VectorAlgorithm: req.VectorAlgorithm, Alpha: req.Alpha, Strategy: req.Strategy, RRFK: int(req.RrfK),
		Fuzzy: int(req.Fuzzy), Threshold: req.Threshold, FilterMeta: metaValuesFromProto(req.FilterMeta),
		Filter: metaFilterFromProto(req.Filter), Boost: req.Boost, FieldWeights: req.FieldWeights, Lang: req.Lang,
		BM25K1: req.Bm25K1, BM25B: req.Bm25B,
		GroupBy:         groupByOf(req.GroupBy),
		IndexWait:       indexWait(req.WaitForIndexMs, req.WaitForKeys, req.ReadLevel),
		PassageOptions:  passageOptions(req.Format, req.PassageLines),
		PlanOptions:     planOptions(req.Explain, req.LatencyTargetMs),
		FeedbackOptions: feedbackOptions(req.Positive, req.Negative, req.FeedbackMode, req.FeedbackWeight, req.Rocchio),
		ImageOptions:    ImageOptions{IncludeImages: req.IncludeImages}, // RAG-010
	})
	if err != nil {
		return nil, searchStatus(err)
	}
	resp := &proto.HybridSearchResponse{Total: safeInt32(res.Total), Strategy: res.Strategy, FtsAlgorithm: res.FTSAlgorithm,
		VectorAlgorithm: res.VectorAlgorithm, Indexing: indexWaitToProto(res.Indexing), ContextTruncated: res.ContextTruncated,
		Plan: planToProto(res.Plan), ImageHits: imageRefsToProto(res.ImageHits)}
	if res.Passages != nil {
		resp.Passages, resp.Total, resp.Indexing = passagesOf(res.Passages), safeInt32(res.Passages.Total), indexWaitToProto(res.Passages.Indexing)
		resp.ImageHits = imageRefsToProto(res.Passages.ImageHits)
		return resp, nil
	}
	docs := make([]MCPDocument, 0, len(res.Results))
	for _, r := range res.Results {
		docs = append(docs, r.Document)
		resp.Results = append(resp.Results, &proto.HybridSearchResult{Document: mcpDocToProto(r.Document, req.IncludeContent),
			CombinedScore: r.CombinedScore, FtsScore: r.FTSScore, VectorScore: r.VectorScore, MatchedTerms: r.MatchedTerms,
			Rank: safeInt32(r.Rank), Pinned: r.Pinned, Group: r.Group, GroupRank: safeInt32(r.GroupRank),
			PendingEmbedding: r.PendingEmbedding, Images: imageRefsToProto(r.Images)})
	}
	if len(req.FacetBy) > 0 && len(docs) > 0 {
		resp.Facets = facetsToProto(computeFacets(docsOfMCP(docs), req.FacetBy, int(req.FacetMaxValues)))
	}
	return resp, nil
}

// SearchAdvisor measures a collection and recommends how to search it
// (SRCH-010).
//
// Added for gRPC parity after the HTTP endpoint and the MCP tool: the Python
// client speaks gRPC, so an adapter built on it could not reach the advice at
// all. Read-only — it measures and returns, and storing the recommendation
// stays an explicit write through the collection-config surface.
func (g *GRPCServer) SearchAdvisor(ctx context.Context, req *proto.SearchAdvisorRequest) (*proto.SearchAdvisorResponse, error) {
	if req.GetCollection() == "" {
		return nil, status.Error(codes.InvalidArgument, "collection is required")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.GetCollection(), PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, "forbidden")
		}
	}

	rec, err := g.server.RecommendSearch(req.GetCollection())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	p := rec.Profile
	return &proto.SearchAdvisorResponse{
		Profile: &proto.CollectionProfile{
			Collection: p.Collection,
			// Counts are clamped rather than converted: a collection of more
			// than two billion documents would otherwise arrive negative, and
			// a negative document count is a worse answer than a saturated one.
			Documents:            clampInt32(p.Documents),
			Sampled:              clampInt32(p.Sampled),
			EmbeddedDocuments:    clampInt32(p.EmbeddedDocuments),
			VectorDimensions:     clampInt32(p.VectorDimensions),
			MedianWords:          clampInt32(p.MedianWords),
			LongDocumentRatio:    p.LongDocumentRatio,
			DistinctTerms:        clampInt32(p.DistinctTerms),
			TermsPerDocument:     p.TermsPerDocument,
			TypeTokenRatio:       p.TypeTokenRatio,
			CodeDocuments:        clampInt32(p.CodeDocuments),
			EstimatedVectorBytes: p.EstimatedVectorBytes,
			SummaryDocuments:     clampInt32(p.SummaryDocuments),
			SummaryRatio:         p.SummaryRatio,
			Semantic:             p.Semantic, // (SRCH-014) nil when nothing is embedded
		},
		SearchType:      rec.SearchType,
		FtsAlgorithm:    rec.FTSAlgorithm,
		VectorAlgorithm: rec.VectorAlgorithm,
		HybridStrategy:  rec.HybridStrategy,
		HybridAlpha:     rec.HybridAlpha,
		RetrievalMode:   rec.RetrievalMode,
		TopK:            clampInt32(rec.TopK),
		SummaryMode:     rec.SummaryMode,
		Reasons:         rec.Reasons,
		Warnings:        rec.Warnings,
	}, nil
}

// clampInt32 narrows a count to the protobuf field that carries it.
//
// Saturating rather than wrapping: a collection larger than MaxInt32 would
// otherwise be reported as a negative number of documents, and a caller
// reading that has no way to tell it from a bug.
func clampInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

// GetIndexingStatus reports queued indexing work (GO-042), as
// GET /v1/indexing-status does over REST.
func (g *GRPCServer) GetIndexingStatus(ctx context.Context, req *proto.IndexingStatusRequest) (*proto.IndexingStatusResponse, error) {
	scope := req.GetCollection()
	if scope == "" {
		scope = "*"
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, scope, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, "forbidden")
		}
	}
	st := g.server.IndexingStatus(req.GetCollection())
	resp := &proto.IndexingStatusResponse{
		Collection:          st.Collection,
		PendingEmbeddings:   clampInt32(st.PendingEmbeddings),
		FailedEmbeddings:    st.FailedEmbeddings,
		CompletedEmbeddings: st.CompletedEmbeddings,
		PendingMetadata:     clampInt32(st.PendingMetadata),
		Progress:            st.Progress,
		Provider:            st.Provider,
		Indexed:             st.Indexed,
		Images:              imageStatusToProto(st.Images), // RAG-010
	}
	if st.OldestPendingAt != nil {
		resp.OldestPendingAt = st.OldestPendingAt.Unix()
	}
	return resp, nil
}
