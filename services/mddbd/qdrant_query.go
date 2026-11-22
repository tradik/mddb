package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"mddb/internal/storage"
)

// Execution of the point query (#447). The vector goes to
// /v1/vector-search in chunk retrieval mode, so each point is one passage —
// what a RAG prompt wants — and Open WebUI's default field mapping reads it
// unchanged: payload.text is the passage, payload.metadata the document's
// metadata, id the point id, score the similarity (higher is closer).

// qdrantQueryPoints answers POST /qdrant/collections/{name}/points/query.
// Permission is checked before existence, so a caller without read access
// learns nothing about which collections exist (docs/AUTHENTICATION.md).
func (s *Server) qdrantQueryPoints(w http.ResponseWriter, r *http.Request, collection string, start time.Time) {
	if s.AuthManager != nil && s.AuthManager.CheckPermission(r.Context(), collection, PermRead) != nil {
		qdrantError(w, start, http.StatusForbidden, "forbidden")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		qdrantFail(w, start, err, http.StatusBadRequest)
		return
	}
	q, err := parseQdrantQuery(body)
	if err != nil {
		qdrantError(w, start, http.StatusBadRequest, err.Error()+qdrantDocsHint)
		return
	}
	if !s.collectionExists(collection) {
		qdrantError(w, start, http.StatusNotFound, "Collection `"+collection+"` doesn't exist!")
		return
	}
	if err := s.qdrantCheckDimensions(collection, len(q.Vector)); err != nil {
		qdrantError(w, start, http.StatusBadRequest, err.Error())
		return
	}
	req := VectorSearchRequest{
		Collection: collection, QueryVector: q.Vector, TopK: q.Offset + q.Limit, Threshold: q.Threshold,
		IncludeContent: true, Algorithm: "auto", RetrievalMode: RetrievalModeChunk,
	}
	var resp VectorSearchResponseHTTP
	if err := esCall(r, s.handleVectorSearch, req, &resp); err != nil {
		qdrantFail(w, start, err, http.StatusInternalServerError)
		return
	}
	writeLimitHeaders(w, resp.LimitReport)
	results := resp.Results[min(q.Offset, len(resp.Results)):]
	points := make([]map[string]any, 0, len(results))
	for i := range results {
		points = append(points, qdrantPoint(collection, &results[i], q.WithPayload))
	}
	qdrantOK(w, start, map[string]any{"points": points})
}

// qdrantFail answers an error with the status it carries: 413 over the body
// cap, the /v1 handler's own status for an answer it refused, else fallback.
func qdrantFail(w http.ResponseWriter, start time.Time, err error, fallback int) {
	var tooLarge *http.MaxBytesError
	var up *esUpstreamError
	switch {
	case errors.As(err, &tooLarge):
		qdrantError(w, start, http.StatusRequestEntityTooLarge, err.Error())
	case errors.As(err, &up):
		qdrantError(w, start, up.Status, up.Message)
	default:
		qdrantError(w, start, fallback, err.Error())
	}
}

// qdrantCheckDimensions refuses a vector the collection's vectors cannot be
// compared with. The similarity of two vectors of different lengths is 0, so
// without this the query would "work" and return noise — and the usual cause
// is that Open WebUI and MDDB embed with different models. Without a
// provider MDDB does not know the dimension, and the search decides.
func (s *Server) qdrantCheckDimensions(collection string, got int) error {
	if s.Embedding == nil || s.Embedding.Dimensions() <= 0 || got == s.Embedding.Dimensions() {
		return nil
	}
	return fmt.Errorf("query vector has %d dimensions, but collection %q is embedded by MDDB with %s (%d dimensions): "+
		"Open WebUI and MDDB must use the same embedding model", got, collection, s.Embedding.Model(), s.Embedding.Dimensions())
}

// qdrantPoint is one search result as a ScoredPoint.
func qdrantPoint(collection string, it *VectorSearchResultItem, withPayload bool) map[string]any {
	chunk := 0
	if it.ChunkIndex != nil {
		chunk = *it.ChunkIndex
	}
	p := map[string]any{"id": qdrantPointID(collection, it.Document.ID, chunk), "version": 0, "score": it.Score}
	if withPayload {
		text := it.ChunkText
		if text == "" {
			text = it.Document.ContentMD
		}
		p["payload"] = map[string]any{"text": text, "metadata": qdrantMetadata(collection, &it.Document, chunk)}
	}
	return p
}

// qdrantMetadata is the document's metadata — one value as a string, several
// as a list — plus where the passage came from. collection, key, lang and
// chunk always describe the hit; source and name, which Open WebUI shows as
// the citation, default to the key and to the title (or key) unless the
// document has its own.
func qdrantMetadata(collection string, doc *storage.Doc, chunk int) map[string]any {
	md := make(map[string]any, len(doc.Meta)+6)
	for k, v := range doc.Meta {
		if len(v) == 1 {
			md[k] = v[0]
		} else {
			md[k] = v
		}
	}
	md["collection"], md["key"], md["lang"], md["chunk"] = collection, doc.Key, doc.Lang, chunk
	if _, set := md["source"]; !set {
		md["source"] = doc.Key
	}
	if _, set := md["name"]; !set {
		md["name"] = doc.Key
		if title, ok := md["title"].(string); ok && title != "" {
			md["name"] = title
		}
	}
	return md
}

// qdrantPointID is a stable UUID for a passage. Qdrant point ids are
// unsigned integers or UUIDs, and a client that validates them strictly
// accepts this; the same passage always gets the same id, so a caller that
// de-duplicates by id keeps working. SHA-256 truncated to 128 bits, marked
// version 8 (custom) and variant RFC 9562.
func qdrantPointID(collection, docID string, chunk int) string {
	sum := sha256.Sum256([]byte(collection + "\x00" + docID + "\x00" + strconv.Itoa(chunk)))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x80
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
