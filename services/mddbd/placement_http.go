package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

// The placement on the REST API (GO-052): GET /v1/placement shows the
// groups, where each collection is and the recent moves; POST
// /v1/placement/move moves a collection. Both are admin routes, and both are
// answered by the meta group's leader (metaRoutes).

// placementGroupJSON is one group in GET /v1/placement.
type placementGroupJSON struct {
	Name        string          `json:"name"`
	Meta        bool            `json:"meta"`
	Leader      string          `json:"leader"`
	LeaderHTTP  string          `json:"leaderHttp"`
	Nodes       []placementNode `json:"nodes"`
	Collections int             `json:"collections"`
}

// placementCollectionJSON is one collection in GET /v1/placement.
type placementCollectionJSON struct {
	Name     string `json:"name"`
	Group    string `json:"group"`
	MovingTo string `json:"movingTo"`
}

// placementJSON is the body of GET /v1/placement.
type placementJSON struct {
	Enabled     bool                      `json:"enabled"`
	NodeID      string                    `json:"nodeId,omitempty"`
	Group       string                    `json:"group,omitempty"`
	MetaGroup   string                    `json:"metaGroup,omitempty"`
	Version     uint64                    `json:"version,omitempty"`
	Term        uint64                    `json:"term,omitempty"`
	Route       string                    `json:"route,omitempty"`
	Groups      []placementGroupJSON      `json:"groups,omitempty"`
	Collections []placementCollectionJSON `json:"collections,omitempty"`
	Moves       []moveStatus              `json:"moves,omitempty"`
}

// handlePlacement is GET /v1/placement.
func (s *Server) handlePlacement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}
	p := s.placement
	if p == nil {
		writeJSONStatus(w, http.StatusOK, placementJSON{})
		return
	}
	writeJSONStatus(w, http.StatusOK, p.describe())
}

// describe is the node's view of the placement.
func (p *placementRuntime) describe() placementJSON {
	out := placementJSON{Enabled: true, NodeID: p.s.NodeID, Group: p.cfg.self, MetaGroup: p.cfg.meta,
		Route: "forward", Moves: p.moves.list()}
	if p.cfg.redirect {
		out.Route = "redirect"
	}
	counts := map[string]int{}
	if t := p.current(); t != nil {
		out.Version, out.Term = t.Version, t.Term
		for c, e := range t.Entries {
			out.Collections = append(out.Collections, placementCollectionJSON{Name: c, Group: e.Group, MovingTo: e.MovingTo})
			counts[e.Group]++
		}
	}
	sort.Slice(out.Collections, func(i, j int) bool { return out.Collections[i].Name < out.Collections[j].Name })
	for _, g := range p.cfg.names {
		n, _ := p.leaderNode(g)
		out.Groups = append(out.Groups, placementGroupJSON{Name: g, Meta: g == p.cfg.meta, Leader: n.ID,
			LeaderHTTP: n.HTTP, Nodes: p.cfg.groups[g], Collections: counts[g]})
	}
	return out
}

// moveRequest is the body of POST /v1/placement/move.
type moveRequest struct {
	Collection string `json:"collection"`
	Group      string `json:"group"`
}

// handlePlacementMove is POST /v1/placement/move: 202 with the move, or with
// ?wait=true the move's end — 200 when done, 409 when it failed.
func (s *Server) handlePlacementMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Collection == "" || req.Group == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "collection and group are required"})
		return
	}
	if s.placement == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "placement is not configured (MDDB_PLACEMENT_GROUPS)"})
		return
	}
	m, err := s.placement.startMove(s.resolveAlias(req.Collection), req.Group)
	switch {
	case errors.Is(err, errMoveSameGroup) || errors.Is(err, errMoveRunning) || errors.Is(err, errNotMetaLeader):
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if r.URL.Query().Get("wait") != "true" {
		writeJSONStatus(w, http.StatusAccepted, map[string]any{"move": s.placement.moves.get(m)})
		return
	}
	select {
	case <-m.done:
	case <-r.Context().Done():
		return
	}
	st := s.placement.moves.get(m)
	if st.Phase == movePhaseFailed {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": st.Error, "move": st})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"move": st})
}

// resolveAlias is the collection name an alias stands for.
func (s *Server) resolveAlias(name string) string {
	if s.Aliases == nil {
		return name
	}
	return s.Aliases.Resolve(name)
}
