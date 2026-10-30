package main

import (
	"context"
	"net/http"

	gql "mddb/graphql"
	"mddb/internal/storage"
)

// The image parts of each search surface (RAG-010), kept here so the search
// handlers each gain a line, not a block. REST and MCP build their own item
// types; gRPC and GraphQL go through MCP's.

// imageQueryResult is a queryImage search's answer, before a surface shapes
// it.
type imageQueryResult struct {
	hits      []imageHit
	passages  *PassagesResponse
	model     string
	dimension int
}

// runImageQuery runs a vector search's queryImage, as passages when the
// request asked for them.
func (s *Server) runImageQuery(ctx context.Context, collection, queryImage string, topK int, threshold float64,
	allowed map[string]bool, filtered bool, opts PassageOptions, waited *IndexWaitResult) (*imageQueryResult, error) {
	hits, model, dims, err := s.searchByImage(ctx, collection, queryImage, topK, threshold, allowed, filtered)
	if err != nil {
		return nil, err
	}
	res := &imageQueryResult{hits: hits, model: model, dimension: dims}
	if opts.wantsPassages() {
		src := make([]passageSource, len(hits))
		for i, h := range hits {
			src[i] = passageSource{doc: h.doc, score: float64(h.score), bestChunk: -1}
		}
		p := s.finishPassages(collection, s.buildPassages(collection, src, opts, "image"), waited)
		res.passages = &p
	}
	return res, nil
}

// respondImageQuery answers a REST vector search that has queryImage.
func (s *Server) respondImageQuery(w http.ResponseWriter, r *http.Request, req VectorSearchRequest, topK int,
	allowed map[string]bool, filtered, includeContent bool, waited *IndexWaitResult) {
	res, err := s.runImageQuery(r.Context(), req.Collection, req.QueryImage, topK, req.Threshold, allowed, filtered, req.PassageOptions, waited)
	if err != nil {
		bad(w, err)
		return
	}
	if res.passages != nil {
		res.passages.LimitReport = req.limitReport
		ok(w, res.passages)
		return
	}
	items := make([]VectorSearchResultItem, len(res.hits))
	for i, h := range res.hits {
		items[i] = VectorSearchResultItem{Document: bodyIf(h.doc, includeContent), Score: h.score, Rank: i + 1}
	}
	ok(w, VectorSearchResponseHTTP{Results: items, Total: len(items), Model: res.model, Dimensions: res.dimension,
		Algorithm: "image", DistanceMetric: "cosine", Indexing: waited, LimitReport: req.limitReport})
}

// mcpImageQuery answers an MCP vector search that has queryImage.
func (s *Server) mcpImageQuery(ctx context.Context, req *MCPVectorSearchRequest, topK int, allowed map[string]bool,
	filtered, includeContent bool, waited *IndexWaitResult) (*MCPVectorSearchResponse, error) {
	res, err := s.runImageQuery(ctx, req.Collection, req.QueryImage, topK, req.Threshold, allowed, filtered, req.PassageOptions, waited)
	if err != nil {
		return nil, err
	}
	if res.passages != nil {
		return &MCPVectorSearchResponse{Passages: res.passages, Algorithm: "image"}, nil
	}
	items := make([]MCPVectorSearchResult, len(res.hits))
	for i, h := range res.hits {
		items[i] = MCPVectorSearchResult{Document: docToMCPDocument(bodyIf(h.doc, includeContent)), Score: h.score, Rank: i + 1}
	}
	return &MCPVectorSearchResponse{Results: items, Total: len(items), Model: res.model, Dimensions: res.dimension,
		Algorithm: "image", DistanceMetric: "cosine", Indexing: waited,
		ResponsePrompt: s.ResponsePrompt(req.Collection, req.Query)}, nil
}

// bodyIf is doc with its body only when the caller asked for it.
func bodyIf(doc storage.Doc, includeContent bool) storage.Doc {
	if !includeContent {
		doc.ContentMD = ""
	}
	return doc
}

// docImages is the owner of a document hit of collection.
func docImages(collection string, doc *storage.Doc) imageOwner {
	return imageOwner{collection, doc.Key, doc.Lang}
}

// mcpDocImages is the owner of an MCP document hit of collection.
func mcpDocImages(collection string, doc *MCPDocument) imageOwner {
	return imageOwner{collection, doc.Key, doc.Lang}
}

// hybridImages attaches each hybrid hit's images and returns the images the
// query matches, for a request with includeImages; nil otherwise.
func (s *Server) hybridImages(ctx context.Context, req HybridSearchRequest, items []HybridSearchResultItem) []ImageRef {
	if !req.IncludeImages {
		return nil
	}
	attachImages(s, items, func(it *HybridSearchResultItem) imageOwner { return docImages(req.Collection, &it.Document) },
		func(it *HybridSearchResultItem, refs []ImageRef) { it.Images = refs })
	allowed, filtered := s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter)
	return s.imageHitsFor(ctx, req.Collection, req.Query, req.TopK, req.Threshold, allowed, filtered, req.plan)
}

// passageImages gives passages their documents' images and the image hits,
// for a request with includeImages.
func (s *Server) passageImages(p *PassagesResponse, include bool, hits []ImageRef) {
	if !include {
		return
	}
	s.attachPassageImages(p)
	p.ImageHits = hits
}

// crossImages attaches each cross-collection hit's images, from its own
// collection.
func (s *Server) crossImages(items []CrossSearchResultItem, include bool) {
	if include {
		attachImages(s, items, func(it *CrossSearchResultItem) imageOwner { return docImages(it.Collection, &it.Document) },
			func(it *CrossSearchResultItem, refs []ImageRef) { it.Images = refs })
	}
}

// imageRefsToGQL converts image references for GraphQL.
func imageRefsToGQL(refs []ImageRef) []*gql.ImageRef {
	if len(refs) == 0 {
		return nil
	}
	optInt := func(n int) *int {
		if n == 0 {
			return nil
		}
		return &n
	}
	out := make([]*gql.ImageRef, len(refs))
	for i, r := range refs {
		g := &gql.ImageRef{Key: r.Key, Lang: r.Lang, AltText: optString(r.AltText), Caption: optString(r.Caption),
			Page: optInt(r.Page), MimeType: optString(r.MimeType), Width: optInt(r.Width), Height: optInt(r.Height),
			Parent: optString(r.Parent), URL: r.URL}
		if r.Score != 0 {
			score := float64(r.Score)
			g.Score = &score
		}
		out[i] = g
	}
	return out
}
