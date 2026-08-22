package main

import (
	"errors"
	"io"
	"net/http"
	"strings"

	json "github.com/goccy/go-json"
	"mddb/internal/schema"
)

// --- HTTP Handlers ---

func (s *Server) handleSchemaSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Collection string `json:"collection"`
		Schema     string `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if req.Schema == "" {
		bad(w, errors.New("missing schema"))
		return
	}
	if err := s.SchemaManager.Set(req.Collection, req.Schema); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "ok", "collection": req.Collection})
}

func (s *Server) handleSchemaGet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Collection string `json:"collection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	raw, found := s.SchemaManager.Get(req.Collection)
	ok(w, map[string]interface{}{
		"collection": req.Collection,
		"schema":     raw,
		"enabled":    found,
	})
}

func (s *Server) handleSchemaDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Collection string `json:"collection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if err := s.SchemaManager.Delete(req.Collection); err != nil {
		bad(w, err)
		return
	}
	ok(w, map[string]string{"status": "ok", "collection": req.Collection})
}

func (s *Server) handleSchemaList(w http.ResponseWriter, r *http.Request) {
	// Drain body to avoid issues
	_, _ = io.Copy(io.Discard, r.Body)
	schemas := s.SchemaManager.List()
	type schemaInfo struct {
		Collection string `json:"collection"`
		Schema     string `json:"schema"`
	}
	var result []schemaInfo
	for col, raw := range schemas {
		result = append(result, schemaInfo{Collection: col, Schema: raw})
	}
	if result == nil {
		result = []schemaInfo{}
	}
	ok(w, map[string]interface{}{"schemas": result})
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Collection string              `json:"collection"`
		Meta       map[string][]string `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	// DOC-012: advisory findings that do not fail validation. Reported
	// alongside errors so a caller sees them even when the document is valid,
	// which is the usual case — a stringified structure is a valid string.
	warnings := schema.LintMetaStrings(req.Meta)
	if warnings == nil {
		warnings = []string{}
	}

	err := s.SchemaManager.Validate(req.Collection, req.Meta)
	if err != nil {
		ok(w, map[string]interface{}{
			"valid":    false,
			"errors":   strings.Split(err.Error(), "; "),
			"warnings": warnings,
		})
		return
	}
	ok(w, map[string]interface{}{
		"valid":    true,
		"errors":   []string{},
		"warnings": warnings,
	})
}
