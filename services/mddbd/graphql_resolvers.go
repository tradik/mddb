package main

import (
	"context"
	"fmt"
	gql "mddb/graphql"
)

// =============================================================================
// Documents (queries + mutations)
// =============================================================================

func (a *GraphQLAdapter) GetDocument(ctx context.Context, collection, key, lang string, env map[string]string) (*gql.Document, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermRead)); err != nil {
		return nil, err
	}
	doc, err := a.mcp.Get(ctx, &MCPGetRequest{Collection: collection, Key: key, Lang: lang, Env: env})
	if err != nil {
		return nil, err
	}
	return mcpDocToGQL(doc), nil
}

func (a *GraphQLAdapter) SearchDocuments(ctx context.Context, input gql.SearchInput) (*gql.DocumentConnection, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, input.Collection, int(PermRead)); err != nil {
		return nil, err
	}
	req := &MCPSearchRequest{
		Collection:     input.Collection,
		FilterMeta:     gql.MapMetaInputToInternal(input.FilterMeta),
		Sort:           derefString(input.Sort),
		Asc:            derefBool(input.Asc, true),
		Limit:          derefInt(input.Limit, 100),
		Offset:         derefInt(input.Offset, 0),
		IncludeContent: true,
	}
	resp, err := a.mcp.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	edges := make([]*gql.DocumentEdge, 0, len(resp.Documents))
	for i := range resp.Documents {
		d := resp.Documents[i]
		edges = append(edges, &gql.DocumentEdge{
			Cursor: fmt.Sprintf("%d", req.Offset+i),
			Node:   mcpDocToGQL(&d),
		})
	}
	hasNext := req.Offset+len(resp.Documents) < resp.Total
	hasPrev := req.Offset > 0
	var startCursor, endCursor *string
	if len(edges) > 0 {
		s := edges[0].Cursor
		e := edges[len(edges)-1].Cursor
		startCursor = &s
		endCursor = &e
	}
	return &gql.DocumentConnection{
		Edges: edges,
		PageInfo: &gql.PageInfo{
			HasNextPage:     hasNext,
			HasPreviousPage: hasPrev,
			StartCursor:     startCursor,
			EndCursor:       endCursor,
		},
		TotalCount: resp.Total,
	}, nil
}

func (a *GraphQLAdapter) AddDocument(ctx context.Context, input gql.AddDocumentInput) (*gql.Document, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, input.Collection, int(PermWrite)); err != nil {
		return nil, err
	}
	doc, err := a.mcp.Add(ctx, &MCPAddRequest{
		Collection: input.Collection,
		Key:        input.Key,
		Lang:       input.Lang,
		Meta:       gql.MapMetaInputToInternal(input.Meta),
		ContentMD:  input.ContentMd,
	})
	if err != nil {
		return nil, err
	}
	if input.TTL != nil && *input.TTL > 0 {
		if _, err := a.mcp.SetTTL(ctx, &MCPSetTTLRequest{
			Collection: input.Collection, Key: input.Key, Lang: input.Lang, TTL: int64(*input.TTL),
		}); err != nil {
			return nil, fmt.Errorf("document added but TTL set failed: %w", err)
		}
	}
	return mcpDocToGQL(doc), nil
}

func (a *GraphQLAdapter) UpdateDocument(ctx context.Context, input gql.UpdateDocumentInput) (*gql.Document, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, input.Collection, int(PermWrite)); err != nil {
		return nil, err
	}
	req := &MCPUpdateDocumentRequest{
		Collection: input.Collection,
		Key:        input.Key,
		Lang:       input.Lang,
	}
	if input.Meta != nil {
		req.Meta = gql.MapMetaInputToInternal(input.Meta)
	}
	if input.ContentMd != nil {
		req.ContentMD = input.ContentMd
	}
	doc, err := a.mcp.UpdateDocument(ctx, req)
	if err != nil {
		return nil, err
	}
	return mcpDocToGQL(doc), nil
}

func (a *GraphQLAdapter) DeleteDocument(ctx context.Context, collection, key, lang string) error {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return err
	}
	if err := a.CheckPermission(ctx, collection, int(PermWrite)); err != nil {
		return err
	}
	return a.mcp.Delete(ctx, &MCPDeleteRequest{Collection: collection, Key: key, Lang: lang})
}

func (a *GraphQLAdapter) DeleteCollection(ctx context.Context, collection string) (int, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return 0, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermAdmin)); err != nil {
		return 0, err
	}
	resp, err := a.mcp.DeleteCollection(ctx, &MCPDeleteCollectionRequest{Collection: collection})
	if err != nil {
		return 0, err
	}
	return resp.Deleted, nil
}

func (a *GraphQLAdapter) AddBatch(ctx context.Context, collection string, docs []*gql.AddBatchDocumentInput) (*gql.BatchAddResult, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermWrite)); err != nil {
		return nil, err
	}
	mcpDocs := make([]MCPBatchDocument, 0, len(docs))
	for _, d := range docs {
		if d == nil {
			continue
		}
		mcpDocs = append(mcpDocs, MCPBatchDocument{
			Key:          d.Key,
			Lang:         d.Lang,
			Meta:         gql.MapMetaInputToInternal(d.Meta),
			ContentMD:    d.ContentMd,
			SaveRevision: derefBool(d.SaveRevision, false),
		})
	}
	resp, err := a.mcp.AddBatch(ctx, &MCPAddBatchRequest{Collection: collection, Documents: mcpDocs})
	if err != nil {
		return nil, err
	}
	return &gql.BatchAddResult{
		Added:   resp.Added,
		Updated: resp.Updated,
		Failed:  resp.Failed,
		Errors:  resp.Errors,
	}, nil
}

func (a *GraphQLAdapter) IngestDocuments(ctx context.Context, collection string, docs []*gql.IngestDocumentInput, opts *gql.IngestOptionsInput) (*gql.IngestResult, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermWrite)); err != nil {
		return nil, err
	}
	mcpDocs := make([]MCPIngestDocument, 0, len(docs))
	for _, d := range docs {
		if d == nil {
			continue
		}
		md := MCPIngestDocument{
			Lang:      d.Lang,
			ContentMD: d.ContentMd,
			Meta:      gql.MapMetaInputToInternal(d.Meta),
		}
		if d.URL != nil {
			md.URL = *d.URL
		}
		if d.Key != nil {
			md.Key = *d.Key
		}
		if d.ExtractFrontmatter != nil {
			md.ExtractFrontmatter = *d.ExtractFrontmatter
		}
		if d.ScrapedAt != nil {
			md.ScrapedAt = int64(*d.ScrapedAt)
		}
		if d.Scraper != nil {
			md.Scraper = *d.Scraper
		}
		if d.TTL != nil {
			md.TTL = int64(*d.TTL)
		}
		mcpDocs = append(mcpDocs, md)
	}
	mcpOpts := MCPIngestOptions{}
	if opts != nil {
		mcpOpts.SkipDuplicates = derefBool(opts.SkipDuplicates, false)
		mcpOpts.SkipEmbeddings = derefBool(opts.SkipEmbeddings, false)
		mcpOpts.SkipFTS = derefBool(opts.SkipFts, false)
		mcpOpts.SkipWebhooks = derefBool(opts.SkipWebhooks, false)
		mcpOpts.AutoConfigureCollection = derefBool(opts.AutoConfigureCollection, false)
		mcpOpts.SaveRevision = derefBool(opts.SaveRevision, false)
	}
	resp, err := a.mcp.Ingest(ctx, &MCPIngestRequest{
		Collection: collection,
		Documents:  mcpDocs,
		Options:    mcpOpts,
	})
	if err != nil {
		return nil, err
	}
	return &gql.IngestResult{
		Added:      resp.Added,
		Updated:    resp.Updated,
		Skipped:    resp.Skipped,
		Failed:     resp.Failed,
		Errors:     resp.Errors,
		Collection: collection,
		DurationMs: int(resp.DurationMs),
	}, nil
}

func (a *GraphQLAdapter) SetTTL(ctx context.Context, collection, key, lang string, ttl int) (*gql.Document, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermWrite)); err != nil {
		return nil, err
	}
	doc, err := a.mcp.SetTTL(ctx, &MCPSetTTLRequest{Collection: collection, Key: key, Lang: lang, TTL: int64(ttl)})
	if err != nil {
		return nil, err
	}
	return mcpDocToGQL(doc), nil
}

func (a *GraphQLAdapter) ImportURL(ctx context.Context, collection, url string, key *string, lang string, meta []*gql.MetaInput, ttl *int) (*gql.Document, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermWrite)); err != nil {
		return nil, err
	}
	req := &MCPImportURLRequest{
		Collection: collection,
		URL:        url,
		Lang:       lang,
		Meta:       gql.MapMetaInputToInternal(meta),
	}
	if key != nil {
		req.Key = *key
	}
	if ttl != nil {
		req.TTL = int64(*ttl)
	}
	doc, err := a.mcp.ImportURL(ctx, req)
	if err != nil {
		return nil, err
	}
	return mcpDocToGQL(doc), nil
}

// =============================================================================
// Vector / FTS / Stats
// =============================================================================

func (a *GraphQLAdapter) VectorSearch(ctx context.Context, input gql.VectorSearchInput) (*gql.VectorSearchResponse, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, input.Collection, int(PermRead)); err != nil {
		return nil, err
	}
	// SRCH-005: out of range is refused rather than clamped — silently
	// halving a caller's recall setting is worse than telling them it was
	// impossible.
	oversample := derefFloat64(input.Oversample, 0)
	if err := ValidateOversample(oversample); err != nil {
		return nil, err
	}

	req := &MCPVectorSearchRequest{
		Collection:     input.Collection,
		FilterMeta:     gql.MapMetaInputToInternal(input.FilterMeta),
		TopK:           derefInt(input.TopK, 5),
		Threshold:      derefFloat64(input.Threshold, 0),
		IncludeContent: derefBool(input.IncludeContent, false),
		Oversample:     oversample,
	}
	if input.Query != nil {
		req.Query = *input.Query
	}
	if len(input.QueryVector) > 0 {
		req.QueryVector = make([]float32, len(input.QueryVector))
		for i, v := range input.QueryVector {
			req.QueryVector[i] = float32(v)
		}
	}
	resp, err := a.mcp.VectorSearch(ctx, req)
	if err != nil {
		return nil, err
	}
	results := make([]*gql.VectorSearchResult, 0, len(resp.Results))
	for i := range resp.Results {
		r := resp.Results[i]
		results = append(results, &gql.VectorSearchResult{
			Document: mcpDocToGQL(&r.Document),
			Score:    float64(r.Score),
			Rank:     r.Rank,
		})
	}
	model := resp.Model
	dims := resp.Dimensions
	return &gql.VectorSearchResponse{
		Results:    results,
		Total:      resp.Total,
		Model:      &model,
		Dimensions: &dims,
	}, nil
}

func (a *GraphQLAdapter) VectorStats(ctx context.Context) (*gql.VectorStats, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	resp, err := a.mcp.VectorStats(ctx)
	if err != nil {
		return nil, err
	}
	return mcpVectorStatsToGQL(resp), nil
}

func (a *GraphQLAdapter) VectorReindex(ctx context.Context, collection string, force *bool) (*gql.VectorStats, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, collection, int(PermWrite)); err != nil {
		return nil, err
	}
	if _, err := a.mcp.VectorReindex(ctx, &MCPVectorReindexRequest{
		Collection: collection,
		Force:      derefBool(force, false),
	}); err != nil {
		return nil, err
	}
	stats, err := a.mcp.VectorStats(ctx)
	if err != nil {
		return nil, err
	}
	return mcpVectorStatsToGQL(stats), nil
}

func (a *GraphQLAdapter) FullTextSearch(ctx context.Context, input gql.FTSInput) (*gql.FTSResponse, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, input.Collection, int(PermRead)); err != nil {
		return nil, err
	}
	resp, err := a.mcp.FTSSearch(ctx, &MCPFTSSearchRequest{
		Collection:     input.Collection,
		Query:          input.Query,
		Limit:          derefInt(input.Limit, 50),
		IncludeContent: true,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*gql.FTSResult, 0, len(resp.Results))
	for i := range resp.Results {
		r := resp.Results[i]
		out = append(out, &gql.FTSResult{
			Document:     mcpDocToGQL(&r.Document),
			Score:        r.Score,
			MatchedTerms: r.MatchedTerms,
		})
	}
	return &gql.FTSResponse{Results: out, Total: resp.Total}, nil
}
