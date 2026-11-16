package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/storage"
)

// Cross-search over collections of several groups (GO-052). The node that
// receives it asks every group for its part — the hits of its targets,
// merged and loaded, before reranking, grouping and the final cut — and
// finishes the merged list itself, exactly as one node holding every target
// would: a vector hit's score does not depend on the other collections, and
// a full-text hit's reciprocal-rank score depends only on its rank in its
// own collection. Ties keep the order of the targets in the request.
//
// Not across groups: feedback examples (they are vectors of documents in
// other groups) and sourceDocID of a collection held elsewhere — send
// queryVector instead.

// crossPartItem is one hit of a part, with what the finish needs.
type crossPartItem struct {
	Collection string      `json:"collection"`
	Document   storage.Doc `json:"document"`
	Score      float32     `json:"score"`
	Chunks     []int       `json:"chunks,omitempty"`
	RerankText string      `json:"rerankText,omitempty"`
	Images     []ImageRef  `json:"images,omitempty"`
}

// crossPart is one group's answer to its part of a cross-search.
type crossPart struct {
	Items       []CrossSearchResultItem `json:"-"`
	Hits        []crossPartItem         `json:"hits"`
	LimitReport                         // the part's cap, the strictest of its targets
	Algorithm   string                  `json:"algorithm"`
	Plan        *SearchPlan             `json:"plan,omitempty"`
	Indexing    *IndexWaitResult        `json:"indexing,omitempty"`
}

// errCrossFeedback refuses feedback over targets of several groups.
var errCrossFeedback = errors.New("feedback examples are not supported in a cross-search over several node groups")

// isPartial reports a request that is a group's part of a fan-out.
func (s *Server) isPartial(r *http.Request) bool {
	return s.placement != nil && r.Header.Get(hdrPlacementPartial) != ""
}

// partialTargets keeps, in a fan-out's part, the targets this group holds,
// and answers an empty part when none is left.
func (s *Server) partialTargets(w http.ResponseWriter, r *http.Request, req *CrossSearchRequest) bool {
	if !s.isPartial(r) {
		return false
	}
	own := req.TargetCollections[:0]
	for _, c := range req.TargetCollections {
		if g, _ := s.placement.owner(c); g == s.placement.cfg.self {
			own = append(own, c)
		}
	}
	req.TargetCollections = own
	if len(own) > 0 {
		return false
	}
	writeJSONStatus(w, http.StatusOK, crossPart{Hits: []crossPartItem{}})
	return true
}

// writeCrossPart answers a part, with each hit's images when asked: they
// are read where the collection is.
func (s *Server) writeCrossPart(w http.ResponseWriter, part crossPart, images bool) {
	s.crossImages(part.Items, images)
	part.Hits = make([]crossPartItem, 0, len(part.Items))
	for _, it := range part.Items {
		part.Hits = append(part.Hits, crossPartItem{Collection: it.Collection, Document: it.Document, Score: it.Score,
			Chunks: it.chunks, RerankText: it.rerankText, Images: it.Images})
	}
	writeJSONStatus(w, http.StatusOK, part)
}

// fanoutCross answers a cross-search whose targets span groups.
func (p *placementRuntime) fanoutCross(w http.ResponseWriter, r *http.Request, next http.Handler) {
	body, err := readBody(r)
	var req CrossSearchRequest
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	if err != nil {
		bad(w, err)
		return
	}
	if err := p.prepareCross(r.Context(), &req); err != nil {
		unprocessable(w, err)
		return
	}
	parts, err := p.gatherCross(r, req, next)
	if err != nil {
		writePlacementBusy(w, err.Error())
		return
	}
	p.s.finishCross(w, r, req, parts)
}

// prepareCross checks what cannot cross groups and resolves the query
// vector once, so every group searches with the same one.
func (p *placementRuntime) prepareCross(ctx context.Context, req *CrossSearchRequest) error {
	if len(req.Positive) > 0 || len(req.Negative) > 0 {
		return errCrossFeedback
	}
	textSearch := req.SearchType == CrossSearchFTS || req.SearchType == CrossSearchHybrid
	if textSearch || len(req.QueryVector) > 0 {
		return nil
	}
	s := p.s
	switch {
	case req.SourceDocID != "":
		if g, _ := p.owner(req.SourceCollection); g != p.cfg.self || s.VectorStore == nil {
			return errors.New("sourceDocID of a collection held by another node group: send queryVector")
		}
		rec, err := s.VectorStore.Get(req.SourceCollection, req.SourceDocID)
		if err != nil || rec == nil {
			return errors.New("source document has no embedding: " + req.SourceCollection + "/" + req.SourceDocID)
		}
		req.QueryVector = rec.Vector
	case req.Query != "" && s.Embedding != nil:
		ectx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		v, err := s.Embedding.Embed(ectx, req.Query, embedding.RoleQuery)
		if err != nil {
			return errors.New("failed to embed query: " + err.Error())
		}
		req.QueryVector = v
	}
	return nil
}

// gatherCross asks every group concerned for its part, at once.
func (p *placementRuntime) gatherCross(r *http.Request, req CrossSearchRequest, next http.Handler) ([]crossPart, error) {
	byGroup, roles := p.splitTargets(req.TargetCollections)
	type answer struct {
		part crossPart
		err  error
	}
	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	answers := make([]answer, len(groups))
	done := make(chan int, len(groups))
	for i, g := range groups {
		sub := req
		sub.TargetCollections = append(byGroup[g], roles...)
		sub.SourceCollection, sub.SourceDocID = "", ""
		go func() {
			defer func() { done <- i }()
			raw, err := p.askPart(r, g, sub, next)
			if err == nil {
				err = json.Unmarshal(raw, &answers[i].part)
			}
			answers[i].err = err
		}()
	}
	for range groups {
		<-done
	}
	parts := make([]crossPart, 0, len(answers))
	for _, a := range answers {
		if a.err != nil {
			return nil, a.err
		}
		parts = append(parts, a.part)
	}
	return parts, nil
}

// splitTargets is each group's explicit targets, in request order — with
// role targets, every group is asked, each answering for its own.
func (p *placementRuntime) splitTargets(targets []string) (map[string][]string, []string) {
	out := map[string][]string{}
	var roles []string
	for _, t := range targets {
		if strings.HasPrefix(t, rolePrefix) {
			roles = append(roles, t)
			continue
		}
		c := p.s.resolveAlias(t)
		g, _ := p.owner(c)
		out[g] = append(out[g], c)
	}
	if len(roles) > 0 {
		for _, g := range p.cfg.names {
			if _, ok := out[g]; !ok {
				out[g] = nil
			}
		}
	}
	return out, roles
}
