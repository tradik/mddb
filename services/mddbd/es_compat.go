package main

import (
	"net/http"
	"strings"

	"mddb/internal/envconf"
)

// Elasticsearch-compatible subset (INT-019).
//
// Clients that speak the Elasticsearch REST API — the official SDKs,
// LangChain's ElasticsearchStore, scripts written for Elasticsearch — can
// index, search and delete MDDB documents without being rewritten. It is a
// subset mapped onto MDDB's own engine, not a second one: every search runs
// through /v1/fts, /v1/vector-search, /v1/hybrid-search or the metadata
// listing, and every write through the single write path or the batch path,
// so schemas, quotas, read-only collections and the incremental FTS index
// apply as they do on /v1. docs/ELASTICSEARCH-COMPAT.md lists what is
// supported and what is refused.
//
// The surface is off unless MDDB_ES_COMPAT=true and lives under /es, so it
// cannot collide with /v1, /health, /metrics, /graphql, /mcp or /debug, and a
// collection named like one of them is still reachable: /es/metrics/_search.
// It sits behind the same middleware as /v1 — authentication, per-collection
// permissions, the rate limiter, the body cap and the search concurrency
// limit.

const (
	// esCompatEnv switches the surface on.
	esCompatEnv = "MDDB_ES_COMPAT"
	// esPathPrefix is where the surface is mounted.
	esPathPrefix = "/es"
	// esCompatVersion is the Elasticsearch API level the subset follows,
	// reported by GET /es. Clients do not negotiate on it; see the docs.
	esCompatVersion = "8.19.0"
	// esDefaultLang is the language of a document written without one.
	esDefaultLang = "en"
	// esContentField and esLangField are the _source fields that map onto a
	// document's body and language; every other field is metadata.
	esContentField = "content"
	esLangField    = "lang"
)

// esCompatEnabled reports whether MDDB_ES_COMPAT turns the surface on.
func esCompatEnabled() bool {
	return envconf.String(esCompatEnv, "false") == "true"
}

// isESPath reports whether path belongs to the surface.
func isESPath(path string) bool {
	return path == esPathPrefix || strings.HasPrefix(path, esPathPrefix+"/")
}

// esAPIKey is the key in `Authorization: ApiKey <key>`, the scheme the
// Elasticsearch clients send for api_key=...; "" for any other header.
func esAPIKey(r *http.Request) string {
	scheme, key, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "ApiKey") {
		return ""
	}
	return strings.TrimSpace(key)
}

// registerESRoutes mounts the surface when it is enabled.
func (s *Server) registerESRoutes(mux routeMux) {
	if !esCompatEnabled() {
		return
	}
	mux.HandleFunc(esPathPrefix, s.handleES)
	mux.HandleFunc(esPathPrefix+"/", s.handleES)
}

// handleES routes an Elasticsearch request. Every answer carries
// X-Elastic-Product: the official clients refuse a 2xx response without it.
func (s *Server) handleES(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, esPathPrefix), "/"), "/")
	if parts[0] == "" {
		s.esRoot(w, r)
		return
	}
	if parts[0] == "_bulk" && len(parts) == 1 && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
		s.esWrite(func(w http.ResponseWriter, r *http.Request) { s.esBulk(w, r, "") })(w, r)
		return
	}
	if strings.HasPrefix(parts[0], "_") || strings.HasPrefix(parts[0], ".") {
		esUnsupported(w, r)
		return
	}
	index := s.Aliases.Resolve(parts[0])
	route := strings.Join(append([]string{r.Method}, parts[1:]...), " ")
	if len(parts) == 3 {
		route = r.Method + " " + parts[1] + " {id}"
	}
	s.esRouteIndex(w, r, index, parts, route)
}

// esRouteIndex dispatches a request on one index.
func (s *Server) esRouteIndex(w http.ResponseWriter, r *http.Request, index string, parts []string, route string) {
	switch route {
	case "HEAD":
		s.esIndexExists(w, r, index)
	case "GET _search", "POST _search":
		s.withSearchLimit(func(w http.ResponseWriter, r *http.Request) { s.esSearch(w, r, index, false) })(w, r)
	case "GET _count", "POST _count":
		s.withSearchLimit(func(w http.ResponseWriter, r *http.Request) { s.esSearch(w, r, index, true) })(w, r)
	case "GET _refresh", "POST _refresh":
		s.esRefresh(w, r, index)
	case "POST _bulk", "PUT _bulk":
		s.esWrite(func(w http.ResponseWriter, r *http.Request) { s.esBulk(w, r, index) })(w, r)
	case "POST _doc":
		s.esWrite(func(w http.ResponseWriter, r *http.Request) { s.esIndexDoc(w, r, index, "", false) })(w, r)
	case "PUT _doc {id}", "POST _doc {id}":
		s.esWrite(func(w http.ResponseWriter, r *http.Request) {
			s.esIndexDoc(w, r, index, parts[2], r.URL.Query().Get("op_type") == "create")
		})(w, r)
	case "PUT _create {id}", "POST _create {id}":
		s.esWrite(func(w http.ResponseWriter, r *http.Request) { s.esIndexDoc(w, r, index, parts[2], true) })(w, r)
	case "GET _doc {id}", "HEAD _doc {id}":
		s.esGetDoc(w, r, index, parts[2])
	case "DELETE _doc {id}":
		s.esWrite(func(w http.ResponseWriter, r *http.Request) { s.esDeleteDoc(w, r, index, parts[2]) })(w, r)
	default:
		esUnsupported(w, r)
	}
}

// esWrite is guardWrite for the surface: read-only mode and a database that
// is not ready refuse the write before it starts, as on /v1.
func (s *Server) esWrite(next http.HandlerFunc) http.HandlerFunc {
	return s.guardWrite(next)
}

// esRoot answers GET / (the client's info call) and HEAD / (ping).
func (s *Server) esRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodHead:
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		esJSON(w, http.StatusOK, map[string]any{
			"name":         "mddb",
			"cluster_name": "mddb",
			"cluster_uuid": "mddb",
			"version": map[string]any{
				"number":                              esCompatVersion,
				"build_flavor":                        "default",
				"build_type":                          "mddb",
				"build_hash":                          "mddb-" + VERSION,
				"lucene_version":                      "none",
				"minimum_wire_compatibility_version":  "8.0.0",
				"minimum_index_compatibility_version": "8.0.0",
			},
			"tagline": "MDDB " + VERSION + ": an Elasticsearch-compatible subset, not Elasticsearch",
		})
	default:
		esUnsupported(w, r)
	}
}

// esIndexExists answers HEAD /{index}: 200 when the collection has a
// configuration or a document, 404 otherwise.
func (s *Server) esIndexExists(w http.ResponseWriter, r *http.Request, index string) {
	if !s.permitted(w, r, index, PermRead) {
		return
	}
	if s.collectionExists(index) {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// esRefresh answers POST /{index}/_refresh by waiting, up to the longest
// waitForIndex allows, for the collection's queued embeddings: what a
// refresh promises — recent writes become searchable — is that wait.
func (s *Server) esRefresh(w http.ResponseWriter, r *http.Request, index string) {
	if !s.permitted(w, r, index, PermRead) {
		return
	}
	successful := 1
	if res := s.waitForIndex(r.Context(), index, IndexWait{WaitForIndex: int(maxWaitForIndex.Milliseconds())}); res != nil && res.TimedOut {
		successful = 0
	}
	esJSON(w, http.StatusOK, map[string]any{"_shards": map[string]int{"total": 1, "successful": successful, "failed": 0}})
}

// esRefreshAfterWrite honours ?refresh=true|wait_for on a write the same way.
func (s *Server) esRefreshAfterWrite(r *http.Request, index string) {
	if v, set := r.URL.Query()["refresh"]; set && (v[0] == "" || v[0] == "true" || v[0] == "wait_for") {
		s.waitForIndex(r.Context(), index, IndexWait{WaitForIndex: int(maxWaitForIndex.Milliseconds())})
	}
}
