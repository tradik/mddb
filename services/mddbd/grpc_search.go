package main

import (
	"context"
	"fmt"
	"math"
	"mddb/internal/embedding"
	"mddb/internal/fts"
	"mddb/internal/storage"
	vec "mddb/internal/vector"
	proto "mddb/proto"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// VectorSearch implements the VectorSearch RPC
func (g *GRPCServer) VectorSearch(ctx context.Context, req *proto.VectorSearchRequest) (*proto.VectorSearchResponse, error) {
	// Check read permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	// SRCH-005: InvalidArgument is the gRPC equivalent of the REST 422 —
	// the message parsed, the number is out of range.
	if err := ValidateOversample(req.Oversample); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if req.Query == "" && len(req.QueryVector) == 0 {
		return nil, status.Error(codes.InvalidArgument, "either query or query_vector is required")
	}

	// Select algorithm
	algo := req.Algorithm
	if algo == "" {
		algo = "flat"
	}
	searcher, algo, err := g.server.readySearcher(req.Collection, algo)
	if err != nil {
		return nil, vectorSearcherStatus(err)
	}

	// Get query vector
	var queryVector []float32
	if len(req.QueryVector) > 0 {
		queryVector = req.QueryVector
	} else if g.server.Embedding != nil {
		var err error
		queryVector, err = g.server.Embedding.Embed(ctx, req.Query, embedding.RoleQuery)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to embed query: "+err.Error())
		}
	} else {
		return nil, status.Error(codes.FailedPrecondition, "no embedding provider configured")
	}

	// RAG-001: request > collection profile > historical default.
	topK := g.server.ResolveTopK(req.Collection, int(req.TopK), 5)

	// Convert proto filter
	filterMeta := make(map[string][]string)
	for k, v := range req.FilterMeta {
		filterMeta[k] = v.Values
	}

	// Oversample for chunk deduplication (SRCH-005).
	searchTopK := OversampledTopK(topK, g.server.ResolveOversample(req.Collection, req.Oversample), 20)

	var results []vec.VectorResult
	if len(filterMeta) > 0 {
		allowedIDs := g.server.getDocIDsByMeta(req.Collection, filterMeta)
		results = searcher.SearchWithFilter(req.Collection, queryVector, searchTopK, req.Threshold, allowedIDs, nil)
	} else {
		results = searcher.Search(req.Collection, queryVector, searchTopK, req.Threshold, nil)
	}

	// Deduplicate chunk results
	results = vec.DeduplicateChunkResults(results)
	if len(results) > topK {
		results = results[:topK]
	}

	// Load documents
	protoResults := make([]*proto.VectorSearchResult, 0, len(results))
	_ = g.server.DBViewC(req.Collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(g.server.BucketNames.Docs)
		for rank, vr := range results {
			v := bDocs.Get(storage.DocKey(req.Collection, vr.DocID))
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
			protoResults = append(protoResults, &proto.VectorSearchResult{
				Document: docToProto(d),
				Score:    vr.Score,
				Rank:     int32(rank + 1),
			})
		}
		return nil
	})

	resp := &proto.VectorSearchResponse{
		Results:   protoResults,
		Total:     safeInt32(len(protoResults)),
		Algorithm: algo,
	}
	if g.server.Embedding != nil {
		resp.Model = g.server.Embedding.Model()
		resp.Dimensions = safeInt32(g.server.Embedding.Dimensions())
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
		return nil, status.Error(codes.Internal, err.Error())
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
		return nil, status.Error(codes.Internal, err.Error())
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
		return nil, status.Error(codes.Internal, err.Error())
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

	docID := genID(req.Collection, req.Key, req.Lang)
	now := time.Now().Unix()
	var expiresAt int64
	if req.Ttl > 0 {
		expiresAt = now + req.Ttl
	}

	var updated storage.Doc
	err := g.server.DBUpdateC(req.Collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		v := bDocs.Get(storage.DocKey(req.Collection, docID))
		if v == nil {
			return fmt.Errorf("not found")
		}
		d, err := unmarshalDoc(v)
		if err != nil {
			return err
		}
		d.ExpiresAt = expiresAt
		buf, err := marshalDoc(d)
		if err != nil {
			return err
		}
		updated = *d
		return bDocs.Put(storage.DocKey(req.Collection, docID), buf)
	})
	if err != nil {
		if err.Error() == "not found" {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	if g.server.TTLManager != nil {
		if expiresAt > 0 {
			_ = g.server.TTLManager.Set(req.Collection, docID, expiresAt)
		} else {
			_ = g.server.TTLManager.Remove(req.Collection, docID)
		}
	}

	return docToProto(&updated), nil
}

// FTS implements the FTS RPC
func (g *GRPCServer) FTS(ctx context.Context, req *proto.FTSRequest) (*proto.FTSResponse, error) {
	// Check read permission
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

	// RAG-001: request > collection profile > historical default.
	limit := g.server.ResolveTopK(req.Collection, int(req.Limit), 50)

	algo := req.Algorithm
	if algo == "" {
		algo = "tfidf"
	}

	fuzzy := int(req.Fuzzy)
	if fuzzy < 0 {
		fuzzy = 0
	}
	if fuzzy > 2 {
		fuzzy = 2
	}

	// Convert proto filter_meta to internal format
	var allowed map[string]bool
	if len(req.FilterMeta) > 0 {
		filterMeta := make(map[string][]string)
		for k, v := range req.FilterMeta {
			filterMeta[k] = v.Values
		}
		allowed = g.server.getDocIDsByMeta(req.Collection, filterMeta)
		if len(allowed) == 0 {
			return &proto.FTSResponse{
				Results:   nil,
				Total:     0,
				Algorithm: algo,
				Fuzzy:     int32(fuzzy),
			}, nil
		}
	}

	// Language-aware tokenization
	queryLang := req.Lang
	tokens := g.server.FTSIndex.TokenizeQueryLang(req.Collection, req.Query, queryLang)

	var results []fts.FTSResult
	var err error
	switch algo {
	case "bm25f":
		if fuzzy > 0 {
			results, err = g.server.FTSIndex.SearchBM25FFuzzy(req.Collection, tokens, limit, fuzzy, nil)
		} else {
			results, err = g.server.FTSIndex.SearchBM25F(req.Collection, tokens, limit, nil)
		}
	case "bm25":
		if fuzzy > 0 {
			results, err = g.server.FTSIndex.SearchBM25Fuzzy(req.Collection, req.Query, limit, fuzzy)
		} else {
			results, err = g.server.FTSIndex.SearchBM25(req.Collection, req.Query, limit)
		}
	case "tfidf":
		if fuzzy > 0 {
			results, err = g.server.FTSIndex.SearchFuzzy(req.Collection, req.Query, limit, fuzzy)
		} else {
			results, err = g.server.FTSIndex.Search(req.Collection, req.Query, limit)
		}
	case "pmisparse":
		if fuzzy > 0 {
			results, err = g.server.FTSIndex.SearchPMISparseFuzzy(req.Collection, req.Query, limit, fuzzy)
		} else {
			results, err = g.server.FTSIndex.SearchPMISparse(req.Collection, req.Query, limit)
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown algorithm: "+algo+", available: tfidf, bm25, bm25f, pmisparse")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Apply metadata filter if provided
	if allowed != nil {
		filtered := results[:0]
		for _, r := range results {
			if allowed[r.DocID] {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	// Apply per-query boosts/demotions from request.
	results = g.server.applyBoostFTS(req.Collection, results, req.Boost)

	// Load docs first, then apply curation (which needs doc.Key/lang). We
	// accumulate FTSResultWithDoc locally so curation + facets see the full
	// shape identical to the HTTP handler.
	docResults := make([]FTSResultWithDoc, 0, len(results))
	_ = g.server.DBViewC(req.Collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		for _, res := range results {
			v := bDocs.Get(storage.DocKey(req.Collection, res.DocID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			if docPtr.ExpiresAt > 0 && docPtr.ExpiresAt < time.Now().Unix() {
				continue
			}
			docResults = append(docResults, FTSResultWithDoc{
				Document:     *docPtr,
				Score:        res.Score,
				MatchedTerms: res.MatchedTerms,
			})
		}
		return nil
	})

	docResults = g.server.applyCurationFTS(req.Collection, req.Query, docResults)
	if limit > 0 && len(docResults) > limit {
		docResults = docResults[:limit]
	}

	protoResults := make([]*proto.FTSResult, 0, len(docResults))
	for _, r := range docResults {
		d := r.Document
		protoResults = append(protoResults, &proto.FTSResult{
			Document:     docToProto(&d),
			Score:        r.Score,
			MatchedTerms: r.MatchedTerms,
			Pinned:       r.Pinned,
		})
	}

	resp := &proto.FTSResponse{
		Results:   protoResults,
		Total:     safeInt32(len(protoResults)),
		Algorithm: algo,
		Fuzzy:     int32(fuzzy),
		Lang:      queryLang,
	}
	if len(req.FacetBy) > 0 && len(docResults) > 0 {
		docs := make([]storage.Doc, len(docResults))
		for i, r := range docResults {
			docs[i] = r.Document
		}
		resp.Facets = facetsToProto(computeFacets(docs, req.FacetBy, int(req.FacetMaxValues)))
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
	// Check read permission
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}

	if req.Collection == "" || req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "missing required fields: collection, query")
	}

	// Defaults. RAG-001: request > collection profile > historical default.
	topK := g.server.ResolveTopK(req.Collection, int(req.TopK), 10)
	algo := req.Algorithm
	if algo == "" {
		algo = "bm25"
	}
	vectorAlgo := req.VectorAlgorithm
	if vectorAlgo == "" {
		vectorAlgo = "flat"
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = "alpha"
	}
	alpha := req.Alpha
	if strategy == "alpha" && alpha == 0 {
		alpha = 0.5
	}
	rrfK := int(req.RrfK)
	if rrfK <= 0 {
		rrfK = 60
	}
	fuzzy := int(req.Fuzzy)
	if fuzzy < 0 {
		fuzzy = 0
	}
	if fuzzy > 2 {
		fuzzy = 2
	}

	// Validate strategy
	if strategy != "alpha" && strategy != "rrf" {
		return nil, status.Error(codes.InvalidArgument, "unknown strategy: "+strategy+", available: alpha, rrf")
	}

	// Validate sort. The gRPC HybridSearchRequest does not carry a geo
	// filter, so "distance" is never applicable over gRPC; clients who
	// need distance sort must use the HTTP handler with its Geo field.
	switch req.Sort {
	case "", "combined":
		// ok — "" normalizes to default "combined" behavior below
	case "distance":
		return nil, status.Error(codes.InvalidArgument, "sort=distance is only supported over HTTP where a geo filter can be attached")
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown sort: "+req.Sort+", available: combined, distance")
	}

	// Convert proto filter_meta to internal format
	filterMeta := make(map[string][]string)
	for k, v := range req.FilterMeta {
		filterMeta[k] = v.Values
	}

	// Convert proto field_weights to internal format
	var fieldWeights map[string]float64
	if len(req.FieldWeights) > 0 {
		fieldWeights = req.FieldWeights
	}

	// Build internal hybrid search request
	hybridReq := HybridSearchRequest{
		Collection:      req.Collection,
		Query:           req.Query,
		TopK:            topK,
		Algorithm:       algo,
		VectorAlgorithm: vectorAlgo,
		Alpha:           floatPtr(alpha),
		Strategy:        strategy,
		RRFK:            rrfK,
		Fuzzy:           fuzzy,
		Threshold:       req.Threshold,
		FilterMeta:      filterMeta,
		IncludeContent:  req.IncludeContent,
		FieldWeights:    fieldWeights,
		Lang:            req.Lang,
		Boost:           req.Boost,
		Sort:            req.Sort,
	}

	// Step 1: Run FTS search
	ftsResults, err := g.server.runFTSSearch(hybridReq)
	if err != nil {
		return nil, status.Error(codes.Internal, "FTS search failed: "+err.Error())
	}

	// Step 2: Run vector search
	vectorResults, err := g.server.runVectorSearch(ctx, hybridReq)
	if err != nil {
		return nil, status.Error(codes.Internal, "vector search failed: "+err.Error())
	}

	// Step 3: Merge results
	var merged []HybridSearchResultItem
	switch strategy {
	case "rrf":
		merged = mergeRRF(ftsResults, vectorResults, rrfK, topK)
	default: // "alpha"
		merged = mergeAlpha(ftsResults, vectorResults, alpha, topK)
	}

	// Apply per-query boosts/demotions (metadata-based score multipliers).
	merged = g.server.applyBoostHybrid(req.Collection, merged, req.Boost)
	if len(merged) > topK {
		merged = merged[:topK]
	}

	// Step 4: Load full documents
	items := g.server.loadHybridDocs(req.Collection, merged, req.IncludeContent)

	// Apply curation (pin/hide) on the loaded documents so Keys are available
	// for matching, then trim to topK.
	items = g.server.applyCurationHybrid(req.Collection, req.Query, items)
	if topK > 0 && len(items) > topK {
		items = items[:topK]
	}

	// Convert to proto response
	protoResults := make([]*proto.HybridSearchResult, len(items))
	for i, item := range items {
		protoResults[i] = &proto.HybridSearchResult{
			Document:      docToProto(&item.Document),
			CombinedScore: item.CombinedScore,
			FtsScore:      item.FTSScore,
			VectorScore:   item.VectorScore,
			MatchedTerms:  item.MatchedTerms,
			Rank:          safeInt32(item.Rank),
			Pinned:        item.Pinned,
		}
	}

	resp := &proto.HybridSearchResponse{
		Results:         protoResults,
		Total:           safeInt32(len(protoResults)),
		Strategy:        strategy,
		FtsAlgorithm:    algo,
		VectorAlgorithm: vectorAlgo,
	}
	if len(req.FacetBy) > 0 && len(items) > 0 {
		docs := make([]storage.Doc, len(items))
		for i, it := range items {
			docs[i] = it.Document
		}
		resp.Facets = facetsToProto(computeFacets(docs, req.FacetBy, int(req.FacetMaxValues)))
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
		},
		SearchType:      rec.SearchType,
		FtsAlgorithm:    rec.FTSAlgorithm,
		VectorAlgorithm: rec.VectorAlgorithm,
		HybridStrategy:  rec.HybridStrategy,
		HybridAlpha:     rec.HybridAlpha,
		RetrievalMode:   rec.RetrievalMode,
		TopK:            clampInt32(rec.TopK),
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
	}
	if st.OldestPendingAt != nil {
		resp.OldestPendingAt = st.OldestPendingAt.Unix()
	}
	return resp, nil
}
