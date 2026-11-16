package main

import "context"

// The rerank stage on each search surface (RAG-011). Every surface calls the
// one stage, rerankItems; these adapters only say how its result type reads.

// restore is the topK a search returns after its stage: cut(final) when the
// run is active, else current unchanged.
func (r *rerankRun) restore(current, final int) int {
	if !r.active() {
		return current
	}
	return r.cut(final)
}

// rerankTrim keeps a reranked list to its final size; an inactive run keeps
// every item, as the search did before.
func rerankTrim[T any](r *rerankRun, items []T, final int) []T {
	return items[:min(len(items), r.restore(len(items), final))]
}

// widenHybridForRerank resolves the request's rerank stage and, when it is
// active, raises TopK to the candidate pool. It returns the topK the answer
// keeps (fallback is the path's default when the request names none).
func (s *Server) widenHybridForRerank(req *HybridSearchRequest, fallback int) int {
	req.rerankRun = s.newRerankRun(req.Collection, req.Query, req.Rerank, req.Explain)
	if !req.rerankRun.active() {
		return req.TopK
	}
	final, _ := s.ResolveTopK(req.Collection, req.TopK, fallback)
	req.TopK = req.rerankRun.pool(final)
	return final
}

// rerankHybrid orders hybrid results, then drops the bodies the caller did
// not ask for — the stage needed them to cut passages.
func (s *Server) rerankHybrid(ctx context.Context, req *HybridSearchRequest, items []HybridSearchResultItem,
	keepContent bool) []HybridSearchResultItem {
	r := req.rerankRun
	items = rerankItems(ctx, s, r, items, rerankAccess[HybridSearchResultItem]{
		text: func(it *HybridSearchResultItem) string {
			return r.textFor(req.Collection, &it.Document, "", it.MatchedTerms)
		},
		id:      func(it *HybridSearchResultItem) string { return it.Document.ID },
		setRank: func(it *HybridSearchResultItem, n int) { it.Rank = n },
	})
	if r.active() && !keepContent {
		for i := range items {
			items[i].Document.ContentMD = ""
		}
	}
	return items
}

// rerankVectorItems orders REST vector results by the passage each carries.
func (s *Server) rerankVectorItems(ctx context.Context, r *rerankRun, items []VectorSearchResultItem, final int) []VectorSearchResultItem {
	items = rerankItems(ctx, s, r, items, rerankAccess[VectorSearchResultItem]{
		text:    func(it *VectorSearchResultItem) string { return it.rerankText },
		id:      func(it *VectorSearchResultItem) string { return it.Document.ID },
		setRank: func(it *VectorSearchResultItem, n int) { it.Rank = n },
	})
	return rerankTrim(r, items, final)
}

// rerankMCPVector orders direct-client vector results (MCP, gRPC, GraphQL).
func (s *Server) rerankMCPVector(ctx context.Context, r *rerankRun, items []MCPVectorSearchResult, final int) []MCPVectorSearchResult {
	items = rerankItems(ctx, s, r, items, rerankAccess[MCPVectorSearchResult]{
		text:    func(it *MCPVectorSearchResult) string { return it.rerankText },
		id:      func(it *MCPVectorSearchResult) string { return it.Document.ID },
		setRank: func(it *MCPVectorSearchResult, n int) { it.Rank = n },
	})
	return rerankTrim(r, items, final)
}

// rerankFTSResults orders full-text results; their documents are whole here.
func (s *Server) rerankFTSResults(ctx context.Context, r *rerankRun, collection string, items []FTSResultWithDoc) []FTSResultWithDoc {
	return rerankItems(ctx, s, r, items, rerankAccess[FTSResultWithDoc]{
		text: func(it *FTSResultWithDoc) string {
			return r.textFor(collection, &it.Document, "", it.MatchedTerms)
		},
		id: func(it *FTSResultWithDoc) string { return it.Document.ID },
	})
}

// rerankCross orders cross-collection results, each read and charged in its
// own collection.
func (s *Server) rerankCross(ctx context.Context, r *rerankRun, items []CrossSearchResultItem) []CrossSearchResultItem {
	return rerankItems(ctx, s, r, items, rerankAccess[CrossSearchResultItem]{
		text: func(it *CrossSearchResultItem) string {
			if it.rerankText != "" {
				return it.rerankText
			}
			return r.textFor(it.Collection, &it.Document, "", nil)
		},
		id:         func(it *CrossSearchResultItem) string { return it.Document.ID },
		collection: func(it *CrossSearchResultItem) string { return it.Collection },
		setRank:    func(it *CrossSearchResultItem, n int) { it.Rank = n },
	})
}
