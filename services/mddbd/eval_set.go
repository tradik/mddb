package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	json "mddb/internal/jsonx"
)

// The evaluation set lives in the collection config (SRCH-019) but is never
// written through PUT /v1/collection-config: a client that rewrites a config
// does not know about it, so CollectionManager.Set keeps the stored set, and
// /v1/eval-queries is the only way to change it.

// evalSetMu serialises read-modify-write of the stored sets.
var evalSetMu sync.Mutex

// evalSet returns the collection's stored evaluation queries.
func (s *Server) evalSet(collection string) []EvalQuery {
	if cfg, found := s.CollectionManager.Get(collection); found {
		return cfg.EvalSet
	}
	return nil
}

// AddEvalQuery stores one query (a new ID when it has none) and returns it.
func (s *Server) AddEvalQuery(collection string, q EvalQuery) (EvalQuery, error) {
	if collection == "" {
		return q, errors.New("collection is required")
	}
	if err := q.Validate(); err != nil {
		return q, err
	}
	evalSetMu.Lock()
	defer evalSetMu.Unlock()
	set := s.evalSet(collection)
	if len(set) >= maxEvalQueries {
		return q, fmt.Errorf("an evaluation set holds at most %d queries", maxEvalQueries)
	}
	if q.ID == "" {
		q.ID = "q" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	next := make([]EvalQuery, 0, len(set)+1)
	for _, e := range set {
		if e.ID != q.ID { // same ID replaces
			next = append(next, e)
		}
	}
	return q, s.storeEvalSet(collection, append(next, q))
}

// DeleteEvalQuery removes one query by ID; false when there was none.
func (s *Server) DeleteEvalQuery(collection, id string) (bool, error) {
	evalSetMu.Lock()
	defer evalSetMu.Unlock()
	set := s.evalSet(collection)
	next := make([]EvalQuery, 0, len(set))
	for _, e := range set {
		if e.ID != id {
			next = append(next, e)
		}
	}
	if len(next) == len(set) {
		return false, nil
	}
	return true, s.storeEvalSet(collection, next)
}

// storeEvalSet writes the set into a copy of the stored config.
func (s *Server) storeEvalSet(collection string, set []EvalQuery) error {
	cfg := CollectionConfig{Type: "default"}
	if stored, found := s.CollectionManager.Get(collection); found {
		cfg = *stored
	}
	cfg.EvalSet = set
	return s.CollectionManager.put(collection, &cfg)
}

// handleEvalQueries serves /v1/eval-queries: GET lists, POST adds, DELETE
// removes (?collection=&id=).
func (s *Server) handleEvalQueries(w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Query().Get("collection")
	switch r.Method {
	case http.MethodGet:
		if !s.permitted(w, r, collection, PermRead) {
			return
		}
		set := s.evalSet(collection)
		if set == nil {
			set = []EvalQuery{}
		}
		ok(w, map[string]any{"collection": collection, "queries": set})
	case http.MethodPost:
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Collection string `json:"collection"`
				EvalQuery
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				bad(w, err)
				return
			}
			if !s.permitted(w, r, req.Collection, PermWrite) {
				return
			}
			q, err := s.AddEvalQuery(req.Collection, req.EvalQuery)
			if err != nil {
				bad(w, err)
				return
			}
			ok(w, q)
		})(w, r)
	case http.MethodDelete:
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) {
			if !s.permitted(w, r, collection, PermWrite) {
				return
			}
			found, err := s.DeleteEvalQuery(collection, r.URL.Query().Get("id"))
			if err != nil {
				bad(w, err)
				return
			}
			ok(w, map[string]bool{"deleted": found})
		})(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleEval serves POST /v1/eval. It only reads, so read-only servers run it.
func (s *Server) handleEval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req EvalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if !s.permitted(w, r, req.Collection, PermRead) {
		return
	}
	res, err := s.RunEval(r.Context(), req)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, res)
}

// permitted checks a permission and writes 403 when it is missing.
func (s *Server) permitted(w http.ResponseWriter, r *http.Request, collection string, perm PermissionType) bool {
	if s.AuthManager == nil {
		return true
	}
	if err := s.AuthManager.CheckPermission(r.Context(), collection, perm); err != nil {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return false
	}
	return true
}
