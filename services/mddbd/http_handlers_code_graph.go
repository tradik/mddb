package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// REST surface for the code connection graph (CODE-005).
//
// The ticket proposed `GET /v1/collections/{c}/graph/{key}`. All 106 existing
// endpoints are flat `/v1/name` with the collection in the query or body, and
// the auth middleware, the OpenAPI document and every client SDK follow that
// shape — one path-parameterised endpoint would be the odd one out in all four.
// The flat form keeps the surface uniform.
//
// GET and POST both work: GET because "what depends on this file" is a question
// worth typing into a browser or curl, POST because a key can contain slashes
// and callers already send JSON everywhere else.

type codeGraphRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Direction  string `json:"direction"`
	Depth      int    `json:"depth"`
	MaxDegree  int    `json:"maxDegree"`
	Lines      bool   `json:"lines"`
}

func (s *Server) handleCodeGraph(w http.ResponseWriter, r *http.Request) {
	req, err := parseCodeGraphRequest(r)
	if err != nil {
		bad(w, err)
		return
	}

	if s.AuthManager != nil && req.Collection != "" {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	if s.Metrics != nil {
		s.Metrics.IncOp("code_graph", req.Collection)
	}

	direction, err := ParseGraphDirection(req.Direction)
	if err != nil {
		bad(w, err)
		return
	}

	res, err := s.CodeGraph(GraphRequest{
		Collection:   req.Collection,
		Key:          req.Key,
		Direction:    direction,
		Depth:        req.Depth,
		MaxDegree:    req.MaxDegree,
		IncludeLines: req.Lines,
	})
	if err != nil {
		// A missing document is the caller's typo, not a server fault; the
		// difference matters to anything retrying on 5xx.
		if errors.Is(err, errGraphDocNotFound) {
			http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
			return
		}
		bad(w, err)
		return
	}
	ok(w, res)
}

func parseCodeGraphRequest(r *http.Request) (codeGraphRequest, error) {
	var req codeGraphRequest

	switch r.Method {
	case http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, err
		}
	case http.MethodGet:
		q := r.URL.Query()
		req.Collection = q.Get("collection")
		req.Key = q.Get("key")
		req.Direction = q.Get("direction")
		req.Depth = atoiOrZero(q.Get("depth"))
		req.MaxDegree = atoiOrZero(q.Get("maxDegree"))
		req.Lines = q.Get("lines") == "true" || q.Get("lines") == "1"
	default:
		return req, errors.New("method not allowed")
	}

	if req.Collection == "" {
		return req, errors.New("collection is required")
	}
	if req.Key == "" {
		return req, errors.New("key is required")
	}
	return req, nil
}

// atoiOrZero treats an unparseable limit as "not given" so the default applies,
// rather than rejecting the whole query over a typo in an optional parameter.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
