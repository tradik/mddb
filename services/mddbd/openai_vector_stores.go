package main

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// OpenAI-compatible vector stores (#446).
//
// Open WebUI's "OpenAI-compatible" External Knowledge Source provider is
// configured with an endpoint (this server's base URL + /v1) and an API key,
// and calls two routes of the OpenAI Vector Stores API: list the stores, and
// search one. This file and openai_vector_stores_search.go serve exactly
// that subset, mapped onto MDDB: a vector store is a collection the caller
// may read, and a search runs MDDB's own hybrid or full-text search. Nothing
// is created, updated or deleted through this API; writes keep using /v1/add
// and the other MDDB endpoints.
//
// Open WebUI first tries GET {endpoint}/vector_store/list (a registry route
// of another proxy) and falls back on any error, so that route is left to
// the mux's 404.

const (
	// openAIVectorStoresPath lists the stores; with a trailing slash it is
	// the subtree holding /{id}/search.
	openAIVectorStoresPath = "/v1/vector_stores"
	// openAIDefaultListLimit and openAIMaxListLimit follow the OpenAI API:
	// limit defaults to 20 and may be 1..100.
	openAIDefaultListLimit = 20
	openAIMaxListLimit     = 100
)

// openAIVectorStore is one store of the list: a collection.
type openAIVectorStore struct {
	ID     string `json:"id"`
	Object string `json:"object"`
	Name   string `json:"name"`
	// CreatedAt is 0: MDDB does not record when a collection was created.
	CreatedAt  int64                 `json:"created_at"`
	Status     string                `json:"status"`
	FileCounts openAIStoreFileCounts `json:"file_counts"`
	Metadata   map[string]string     `json:"metadata"`
}

// openAIStoreFileCounts counts a store's documents as OpenAI counts files.
// Every stored document is searchable, so all of them are "completed".
type openAIStoreFileCounts struct {
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Failed     int `json:"failed"`
	Cancelled  int `json:"cancelled"`
	Total      int `json:"total"`
}

// openAIStoreList is the list page.
type openAIStoreList struct {
	Object  string              `json:"object"`
	Data    []openAIVectorStore `json:"data"`
	FirstID *string             `json:"first_id"`
	LastID  *string             `json:"last_id"`
	HasMore bool                `json:"has_more"`
}

// openAIErrorDetail is the OpenAI error object; the status codes stay MDDB's.
type openAIErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code"`
}

// openAIFail writes an error in the OpenAI shape, typed by its status.
func openAIFail(w http.ResponseWriter, status int, code, message string) {
	typ := "invalid_request_error"
	switch {
	case status == http.StatusUnauthorized:
		typ = "authentication_error"
	case status == http.StatusForbidden:
		typ = "permission_error"
	case status >= http.StatusInternalServerError:
		typ = "server_error"
	}
	writeJSONStatus(w, status, map[string]openAIErrorDetail{"error": {Message: message, Type: typ, Code: code}})
}

// openAIAuthFail answers an authorization refusal: 401 without credentials,
// 403 without the permission, as writeAccessDenied does on the rest of /v1.
func openAIAuthFail(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrUnauthorized) {
		openAIFail(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	openAIFail(w, http.StatusForbidden, "forbidden", "no read permission on this vector store")
}

// handleOpenAIVectorStores answers GET /v1/vector_stores.
func (s *Server) handleOpenAIVectorStores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openAIFail(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET lists vector stores")
		return
	}
	q := r.URL.Query()
	limit := openAIDefaultListLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > openAIMaxListLimit {
			openAIFail(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer from 1 to 100")
			return
		}
		limit = n
	}
	stores, err := s.openAIReadableStores(r)
	if err != nil {
		openAIFail(w, http.StatusServiceUnavailable, "stats_pending", err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, openAIStorePage(stores, q.Get("after"), limit))
}

// openAIStorePage cuts the page after the store named after (cursor paging,
// as OpenAI pages): stores are sorted by id, so the cursor is a position.
func openAIStorePage(stores []openAIVectorStore, after string, limit int) openAIStoreList {
	start := 0
	if after != "" {
		start = sort.Search(len(stores), func(i int) bool { return stores[i].ID > after })
	}
	end := min(start+limit, len(stores))
	page := openAIStoreList{Object: "list", Data: stores[start:end], HasMore: end < len(stores)}
	if len(page.Data) > 0 {
		page.FirstID, page.LastID = &page.Data[0].ID, &page.Data[len(page.Data)-1].ID
	}
	return page
}

// openAIReadableStores is every collection the caller may read, sorted: the
// collections that hold documents and those that only have a configuration.
// Read permission (which carries the tenant namespace) and an MCP API key's
// scope are applied per collection, as the scenario listing applies them, so
// a user with read on two collections lists those two rather than being
// refused the whole list.
func (s *Server) openAIReadableStores(r *http.Request) ([]openAIVectorStore, error) {
	snap := s.dbStats(statsWait())
	if snap == nil {
		return nil, errors.New("database statistics are being computed; retry shortly")
	}
	descriptions := map[string]string{}
	names := snap.sortedNames()
	if s.CollectionManager != nil {
		for name, cfg := range s.CollectionManager.ListAll() {
			descriptions[name] = cfg.Description
			if snap.Collections[name] == nil {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	tenant := TenantFromContext(r.Context())
	stores := []openAIVectorStore{}
	for _, name := range names {
		if !CollectionInTenant(tenant, name) || s.openAIMayRead(r, name) != nil {
			continue
		}
		docs := 0
		if cc := snap.Collections[name]; cc != nil {
			docs = cc.Documents
		}
		stores = append(stores, openAIVectorStore{ID: name, Object: "vector_store", Name: name, Status: "completed",
			FileCounts: openAIStoreFileCounts{Completed: docs, Total: docs},
			Metadata:   map[string]string{"description": descriptions[name]}})
	}
	return stores, nil
}

// openAIMayRead is the read check of one collection: RBAC (with the tenant
// namespace) and the scope of an MCP API key. nil, ErrUnauthorized or
// ErrForbidden; it does not depend on whether the collection exists.
func (s *Server) openAIMayRead(r *http.Request, collection string) error {
	if err := s.authorize(r.Context(), ruleRead, []string{collection}); err != nil {
		return err
	}
	return s.keyScopeReads(r.Context(), []string{collection})
}

// openAIStoreID reads {id} from /v1/vector_stores/{id}/{action}. Open WebUI
// escapes the id (a tenant collection "acme/docs" arrives as acme%2Fdocs), so
// the id is taken from the escaped path, as everything between the prefix
// and the last segment, and unescaped once: an unescaped "/" in the id works
// too.
func openAIStoreID(r *http.Request) (id, action string, ok bool) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), openAIVectorStoresPath+"/")
	cut := strings.LastIndexByte(rest, '/')
	if cut <= 0 {
		return "", "", false
	}
	// EscapedPath is always validly escaped, so unescaping cannot fail.
	id, _ = url.PathUnescape(rest[:cut])
	return id, rest[cut+1:], true
}
