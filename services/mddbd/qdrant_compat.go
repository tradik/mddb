package main

import (
	"net/http"
	"strings"
	"time"

	json "mddb/internal/jsonx"

	"mddb/internal/envconf"
)

// Qdrant-compatible point query (#447).
//
// Open WebUI's External Knowledge Source with the Qdrant provider embeds the
// user's question with its own embedding model and calls qdrant-client's
// query_points over REST. Serving the few requests that call makes lets an
// MDDB collection be picked there without a bridge. It is a translation, not
// a second engine: the query runs through /v1/vector-search (algorithm
// "auto", chunk retrieval), so permissions, the search cap and the
// concurrency limit apply as on /v1. docs/QDRANT_COMPAT.md lists what is
// supported and what is refused.
//
// Like the Elasticsearch subset (INT-019) the surface is off unless
// MDDB_QDRANT_COMPAT=true and lives under its own prefix. qdrant-client
// treats the path of its url as a prefix (QdrantRemote: parsed_url.path
// becomes the REST base), so "http://mddb:11023/qdrant" works as the
// Endpoint in Open WebUI's form, which has no separate prefix field.

const (
	// qdrantCompatEnv switches the surface on.
	qdrantCompatEnv = "MDDB_QDRANT_COMPAT"
	// qdrantPathPrefix is where the surface is mounted.
	qdrantPathPrefix = "/qdrant"
	// qdrantCompatVersion is the Qdrant API level the subset follows,
	// reported by GET /qdrant. qdrant-client compares it with its own
	// version at start-up and only warns when major differs or minor is more
	// than one apart; 1.20.0 is the client Open WebUI pins.
	qdrantCompatVersion = "1.20.0"
	// qdrantDocsHint ends every refusal, so the reader knows where the
	// supported subset is written down.
	qdrantDocsHint = "; see docs/QDRANT_COMPAT.md"
)

// qdrantCompatEnabled reports whether MDDB_QDRANT_COMPAT turns the surface on.
func qdrantCompatEnabled() bool {
	return envconf.String(qdrantCompatEnv, "false") == "true"
}

// isQdrantPath reports whether path belongs to the surface.
func isQdrantPath(path string) bool {
	return path == qdrantPathPrefix || strings.HasPrefix(path, qdrantPathPrefix+"/")
}

// qdrantAPIKey is the `api-key` header qdrant-client sends for api_key=...
func qdrantAPIKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("api-key"))
}

// registerQdrantRoutes mounts the surface when it is enabled.
func (s *Server) registerQdrantRoutes(mux routeMux) {
	if !qdrantCompatEnabled() {
		return
	}
	mux.HandleFunc(qdrantPathPrefix, s.handleQdrant)
	mux.HandleFunc(qdrantPathPrefix+"/", s.handleQdrant)
}

// handleQdrant routes a request: the root probe and the point query; every
// other path is refused by name, in Qdrant's error shape.
func (s *Server) handleQdrant(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("Content-Type", "application/json")
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, qdrantPathPrefix), "/")
	parts := strings.Split(rest, "/")
	switch {
	case rest == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		// The probe qdrant-client sends at start-up; Qdrant answers it
		// without the result envelope.
		qdrantJSON(w, http.StatusOK, map[string]string{"title": "mddb - qdrant-compatible subset",
			"version": qdrantCompatVersion, "commit": "mddb-" + VERSION})
	case len(parts) == 4 && parts[0] == "collections" && parts[1] != "" && parts[2] == "points" && parts[3] == "query" &&
		r.Method == http.MethodPost:
		s.withSearchLimit(func(w http.ResponseWriter, r *http.Request) {
			s.qdrantQueryPoints(w, r, s.Aliases.Resolve(parts[1]), start)
		})(w, r)
	default:
		qdrantError(w, start, http.StatusNotFound,
			"the MDDB Qdrant-compatible subset does not support "+r.Method+" "+r.URL.Path+qdrantDocsHint)
	}
}

// qdrantJSON writes a JSON body with a status.
func qdrantJSON(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.WriteHeader(status)
	_, _ = w.Write(b) // #nosec G705 -- JSON response body
}

// qdrantOK writes Qdrant's success envelope around result.
func qdrantOK(w http.ResponseWriter, start time.Time, result any) {
	qdrantJSON(w, http.StatusOK, map[string]any{"result": result, "status": "ok", "time": time.Since(start).Seconds()})
}

// qdrantError writes Qdrant's error envelope, {"status":{"error":...}}: the
// shape qdrant-client puts in the UnexpectedResponse it raises.
func qdrantError(w http.ResponseWriter, start time.Time, status int, msg string) {
	qdrantJSON(w, status, map[string]any{"status": map[string]string{"error": msg}, "time": time.Since(start).Seconds()})
}
