package mddb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// typedCase is one typed method: what it sends, the canned body the server
// answers with, and a check on the decoded result.
type typedCase struct {
	name     string
	call     func(ctx context.Context, c *Client) (any, error)
	method   string
	path     string // path plus raw query
	wantBody string // substring the request body must contain ("" = none sent)
	respBody string
	check    func(t *testing.T, got any)
}

func typedCases() []typedCase {
	alpha := 0.0
	off := false
	return []typedCase{
		{
			name:   "CodeLanguages",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.CodeLanguages(ctx) },
			method: "GET", path: "/v1/code-languages",
			respBody: `{"extensions":{".go":"go"},"languages":["go"]}`,
			check: func(t *testing.T, got any) {
				if r := got.(*CodeLanguages); r.Extensions[".go"] != "go" || r.Languages[0] != "go" {
					t.Errorf("CodeLanguages = %+v", r)
				}
			},
		},
		{
			name:   "SearchAdvisor",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.SearchAdvisor(ctx, "repo", true) },
			method: "POST", path: "/v1/search-advisor",
			wantBody: `"apply":true`,
			respBody: `{"searchType":"hybrid"}`,
			check: func(t *testing.T, got any) {
				if !strings.Contains(string(got.(json.RawMessage)), "hybrid") {
					t.Errorf("SearchAdvisor = %s", got)
				}
			},
		},
		{
			name: "Eval",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.Eval(ctx, map[string]any{"collection": "docs"})
			},
			method: "POST", path: "/v1/eval",
			wantBody: `"collection":"docs"`,
			respBody: `{"k":10,"results":[]}`,
			check: func(t *testing.T, got any) {
				if !strings.Contains(string(got.(json.RawMessage)), `"k":10`) {
					t.Errorf("Eval = %s", got)
				}
			},
		},
		{
			name:   "Gain",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.Gain(ctx, "7d", true) },
			method: "GET", path: "/v1/gain?daily=true&window=7d",
			respBody: `{"windowDays":7}`,
			check: func(t *testing.T, got any) {
				if !strings.Contains(string(got.(json.RawMessage)), `"windowDays":7`) {
					t.Errorf("Gain = %s", got)
				}
			},
		},
		{
			name:   "GainDefault",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.Gain(ctx, "", false) },
			method: "GET", path: "/v1/gain",
			respBody: `{"windowDays":30}`,
			check:    func(t *testing.T, got any) {},
		},
		{
			name: "Ingest",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.Ingest(ctx, IngestRequest{Collection: "blog",
					Documents: []IngestDocument{{URL: "http://x/a", Key: "a#1", Lang: "en", ContentMD: "hi"}},
					Options:   IngestOptions{SkipEmbeddings: true, SkipWebhooks: true, Profile: "fast"}})
			},
			method: "POST", path: "/v1/ingest",
			wantBody: `"options":{"skipEmbeddings":true,"skipWebhooks":true,"profile":"fast"}`,
			respBody: `{"added":1,"collection":"blog","durationMs":3,"keyCollisions":["A"]}`,
			check: func(t *testing.T, got any) {
				r := got.(*IngestResponse)
				if r.Added != 1 || r.Collection != "blog" || len(r.KeyCollisions) != 1 {
					t.Errorf("IngestResponse = %+v", r)
				}
			},
		},
		{
			name: "DeleteBatch",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.DeleteBatch(ctx, DeleteBatchRequest{Collection: "blog", Documents: []DocumentRef{{Key: "a", Lang: "en"}}})
			},
			method: "POST", path: "/v1/delete-batch",
			wantBody: `"documents":[{"key":"a","lang":"en"}]`,
			respBody: `{"deleted":1,"not_found":2,"failed":0,"errors":null}`,
			check: func(t *testing.T, got any) {
				if r := got.(*DeleteBatchResponse); r.Deleted != 1 || r.NotFound != 2 {
					t.Errorf("DeleteBatchResponse = %+v", r)
				}
			},
		},
		{
			name: "Aggregate",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.Aggregate(ctx, AggregateRequest{Collection: "blog",
					Facets: []FacetRequest{{Field: "tag"}}, Histograms: []HistogramRequest{{Field: "addedAt", Interval: "month"}}})
			},
			method: "POST", path: "/v1/aggregate",
			wantBody: `"histograms":[{"field":"addedAt","interval":"month"}]`,
			respBody: `{"collection":"blog","totalDocs":4,"facets":{"tag":[{"value":"go","count":3}]},"histograms":{"addedAt":[{"key":"2026-10","from":1,"to":2,"count":4}]}}`,
			check: func(t *testing.T, got any) {
				r := got.(*AggregateResponse)
				if r.TotalDocs != 4 || r.Facets["tag"][0].Count != 3 || r.Histograms["addedAt"][0].Key != "2026-10" {
					t.Errorf("AggregateResponse = %+v", r)
				}
			},
		},
		{
			name:   "GetCollectionConfig",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.GetCollectionConfig(ctx, "my docs") },
			method: "GET", path: "/v1/collection-config?collection=my+docs",
			respBody: `{"collection":"my docs","configured":true,"config":{"type":"default","code":{"includePaths":["src"]},"embedding":{"chunkSize":800,"chunkMode":"code"},"fts":{"enabled":false}}}`,
			check: func(t *testing.T, got any) {
				r := got.(*CollectionConfigResponse)
				cfg := r.Config
				if !r.Configured || cfg.Code.IncludePaths[0] != "src" || cfg.Embedding.ChunkSize != 800 ||
					cfg.Embedding.ChunkMode != "code" || cfg.FTS.Enabled == nil || *cfg.FTS.Enabled {
					t.Errorf("CollectionConfigResponse = %+v", r)
				}
			},
		},
		{
			name: "SetCollectionConfig",
			call: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.SetCollectionConfig(ctx, SetCollectionConfigRequest{Collection: "blog",
					Code: &CodeConfig{IncludePaths: []string{"src"}}, FTS: &CollectionFTSSettings{Enabled: &off}})
			},
			method: "PUT", path: "/v1/collection-config",
			wantBody: `"code":{"includePaths":["src"]},"fts":{"enabled":false}`,
			respBody: `{"status":"ok","collection":"blog"}`,
		},
		{
			name:   "StorageAll",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.Storage(ctx, "") },
			method: "GET", path: "/v1/storage",
			respBody: `{"fileBytes":100,"pageSize":4096,"buckets":{"docs":{"allocBytes":8192,"inUseBytes":10,"keys":1}},"collections":{"blog":{"totalBytes":10,"byBucket":{"docs":10},"byCategory":{"content":10}}}}`,
			check: func(t *testing.T, got any) {
				r := got.(*StorageBreakdown)
				if r.FileBytes != 100 || r.Buckets["docs"].Keys != 1 || r.Collections["blog"].ByCategory["content"] != 10 {
					t.Errorf("StorageBreakdown = %+v", r)
				}
			},
		},
		{
			name:   "StorageCollection",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.Storage(ctx, "a&b") },
			method: "GET", path: "/v1/storage?collection=a%26b",
			respBody: `{"collections":{}}`,
		},
		{
			name: "SearchFTS",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.SearchFTS(ctx, FTSRequest{Collection: "blog", Query: "go", Highlight: true, FacetBy: []string{"tag"}})
			},
			method: "POST", path: "/v1/fts",
			wantBody: `"highlight":true,"facetBy":["tag"]`,
			respBody: `{"results":[{"document":{"key":"a"},"score":1.5,"matchedTerms":["go"],"highlights":[{"fragment":"<mark>go</mark>","startLine":3,"endLine":3}]}],"total":1,"searchStats":{"durationMs":0.4},"spellCorrected":{"original":"og","corrected":"go"},"facets":{"tag":[{"value":"x","count":1}]}}`,
			check: func(t *testing.T, got any) {
				r := got.(*FTSResponse)
				if r.Total != 1 || r.Results[0].Document.Key != "a" || r.Results[0].Highlights[0].StartLine != 3 ||
					r.Stats == nil || r.SpellCorrected.Corrected != "go" || r.Facets["tag"][0].Value != "x" {
					t.Errorf("FTSResponse = %+v", r)
				}
			},
		},
		{
			name: "SearchVectors",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.SearchVectors(ctx, VectorSearchRequest{Collection: "blog", Query: "go", RetrievalMode: "graph", GraphExpand: &GraphExpand{Depth: 2}})
			},
			method: "POST", path: "/v1/vector-search",
			wantBody: `"graphExpand":{"graphDepth":2}`,
			respBody: `{"results":[{"document":{"key":"a"},"score":0.9,"rank":1,"chunkIndex":0}],"total":1,"model":"m","dimensions":3,"graphExpansions":[{"key":"b","fromKey":"a","kind":"imports","depth":1}]}`,
			check: func(t *testing.T, got any) {
				r := got.(*VectorSearchResponse)
				if r.Total != 1 || r.Results[0].ChunkIndex == nil || r.Dimensions != 3 || r.GraphExpansions[0].Kind != "imports" {
					t.Errorf("VectorSearchResponse = %+v", r)
				}
			},
		},
		{
			name: "SearchHybrid",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.SearchHybrid(ctx, HybridSearchRequest{Collection: "blog", Query: "go", Alpha: &alpha, Strategy: "weighted",
					Signals: &FusionSignals{Freshness: 0.2}, Geo: &GeoFilter{Lat: 1, Lng: 2, RadiusMeters: 3}})
			},
			method: "POST", path: "/v1/hybrid-search",
			wantBody: `"alpha":0,"strategy":"weighted","signals":{"freshness":0.2}`,
			respBody: `{"results":[{"document":{"key":"a"},"combinedScore":0.7,"rank":1}],"total":1,"strategy":"weighted","signalBreakdown":[{"base":0.6,"final":0.7}],"ftsAlgorithm":"bm25","vectorAlgorithm":"flat"}`,
			check: func(t *testing.T, got any) {
				r := got.(*HybridSearchResponse)
				if r.Total != 1 || r.Results[0].CombinedScore != 0.7 || r.SignalBreakdown[0].Final != 0.7 || r.FTSAlgorithm != "bm25" {
					t.Errorf("HybridSearchResponse = %+v", r)
				}
			},
		},
		{
			name:   "GetVectorStats",
			call:   func(ctx context.Context, c *Client) (any, error) { return c.GetVectorStats(ctx) },
			method: "GET", path: "/v1/vector-stats",
			respBody: `{"enabled":true,"model":"m","dimensions":3,"index_ready":true,"collections":{"blog":{"total_documents":5,"embedded_documents":4,"total_chunks":9,"chunkSize":1500}},"chunking":{"enabled":true,"chunkSize":1500},"queue":{"size":100,"depth":2,"dropped":1,"wait":"5s"}}`,
			check: func(t *testing.T, got any) {
				r := got.(*VectorStats)
				if !r.IndexReady || r.Collections["blog"].EmbeddedDocuments != 4 || r.Chunking.ChunkSize != 1500 || r.Queue.Dropped != 1 {
					t.Errorf("VectorStats = %+v", r)
				}
			},
		},
		{
			name: "ReindexVectors",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.ReindexVectors(ctx, VectorReindexRequest{Collection: "blog", Force: true})
			},
			method: "POST", path: "/v1/vector-reindex",
			wantBody: `{"collection":"blog","force":true}`,
			respBody: `{"embedded":3,"skipped":1,"failed":0,"totalChunks":7,"errors":null}`,
			check: func(t *testing.T, got any) {
				if r := got.(*VectorReindexResponse); r.Embedded != 3 || r.TotalChunks != 7 {
					t.Errorf("VectorReindexResponse = %+v", r)
				}
			},
		},
		{
			name: "CodeGraph",
			call: func(ctx context.Context, c *Client) (any, error) {
				return c.CodeGraph(ctx, CodeGraphRequest{Collection: "src", Key: "net/conn.c", Direction: "in", Lines: true})
			},
			method: "POST", path: "/v1/code-graph",
			wantBody: `"direction":"in","lines":true`,
			respBody: `{"collection":"src","root":"net/conn.c","nodes":[{"key":"main.c","docId":"d","depth":1}],"edges":[{"from":"main.c","to":"net/conn.c","kind":"imports","symbol":"conn.h","lines":{"fromLine":4}}],"truncated":true}`,
			check: func(t *testing.T, got any) {
				r := got.(*CodeGraph)
				if !r.Truncated || r.Nodes[0].Key != "main.c" || r.Edges[0].Lines.FromLine != 4 {
					t.Errorf("CodeGraph = %+v", r)
				}
			},
		},
	}
}
