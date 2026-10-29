package main

import (
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// The alias API (GO-044):
//
//	GET    /v1/aliases                  list
//	PUT    /v1/aliases/{alias}          {"collection": "docs-v2"}  create or move
//	DELETE /v1/aliases/{alias}          remove (the collection stays)
//
// Moving an alias needs write permission on the collection it will point at
// and, when it moves, on the one it pointed at.
func (s *Server) handleAliases(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/aliases"), "/")
	switch {
	case r.Method == http.MethodGet && name == "":
		s.listAliases(w, r)
	case r.Method == http.MethodPut && name != "":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.putAlias(w, r, name) })(w, r)
	case r.Method == http.MethodDelete && name != "":
		s.guardWrite(func(w http.ResponseWriter, r *http.Request) { s.deleteAlias(w, r, name) })(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// listAliases returns the aliases whose collection the caller may read.
func (s *Server) listAliases(w http.ResponseWriter, r *http.Request) {
	out := []AliasEntry{}
	for _, e := range s.Aliases.List() {
		if s.AuthManager == nil || s.AuthManager.CheckPermission(r.Context(), e.Collection, PermRead) == nil {
			out = append(out, e)
		}
	}
	ok(w, map[string]any{"aliases": out})
}

func (s *Server) putAlias(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Collection string `json:"collection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	// Write on the new target, and on the old one when the alias moves.
	for _, c := range []string{req.Collection, s.Aliases.Resolve(name)} {
		if c != name && !s.permitted(w, r, c, PermWrite) {
			return
		}
	}
	if err := s.SetAlias(name, req.Collection); err != nil {
		bad(w, err)
		return
	}
	ok(w, AliasEntry{Alias: name, Collection: req.Collection})
}

func (s *Server) deleteAlias(w http.ResponseWriter, r *http.Request, name string) {
	target := s.Aliases.Resolve(name)
	if target == name {
		http.Error(w, `{"error":"alias not found"}`, http.StatusNotFound)
		return
	}
	if !s.permitted(w, r, target, PermWrite) {
		return
	}
	if err := s.DeleteAlias(name); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]bool{"deleted": true})
}
