package main

import (
	"errors"
	"fmt"
	"net/http"

	json "mddb/internal/jsonx"
)

// Read-only collections (AGT-005).
//
// A collection installed from a corpus pack is marked readOnly in its
// CollectionConfig. Every write to collection data — a document, a revision,
// the full-text index, vectors, geo points, TTL entries, temporal events —
// asks the server's router for the file to write to (collectionDBForWrite),
// in both storage layouts. The refusal lives there, once: REST, gRPC, MCP,
// GraphQL, batch, ingest, reindexing, TTL and automations are all refused
// by the same check with the same error (lesson of GO-003: one gate, not
// five). Reads never pass through it.

// ReadOnlyError is a write refused because the collection is read-only.
// Every transport maps it the same way: HTTP 403, gRPC PERMISSION_DENIED,
// and a READ_ONLY code in GraphQL extensions and MCP structured content.
type ReadOnlyError struct {
	Collection string
}

// Error says what was refused and how to make the collection writable again.
func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("collection %q is read-only: it was installed from a corpus pack; "+
		"remove it with `mddb-cli corpus remove %s` to write to that name (403)", e.Collection, e.Collection)
}

// detail is the error as structured data, for the REST body, the MCP
// structured content and the GraphQL extensions.
func (e *ReadOnlyError) detail() map[string]any {
	return map[string]any{
		"error":      e.Error(),
		"code":       "READ_ONLY",
		"status":     http.StatusForbidden,
		"collection": e.Collection,
	}
}

// asReadOnlyError finds a ReadOnlyError in err's chain.
func asReadOnlyError(err error) *ReadOnlyError {
	var re *ReadOnlyError
	if errors.As(err, &re) {
		return re
	}
	return nil
}

// checkCollectionWritable is the gate: nil, or a *ReadOnlyError when the
// collection's config marks it read-only. A map lookup under a read lock.
func (s *Server) checkCollectionWritable(collection string) error {
	if s.CollectionManager == nil {
		return nil
	}
	if cfg, found := s.CollectionManager.Get(collection); found && cfg.ReadOnly {
		return &ReadOnlyError{Collection: collection}
	}
	return nil
}

// collectionReadOnly reports whether a collection refuses writes.
func (s *Server) collectionReadOnly(collection string) bool {
	return s.checkCollectionWritable(collection) != nil
}

// wholeBatchRefusal is the error in err's chain that refuses a whole batch
// rather than its documents one by one: a quota (GO-043) or a read-only
// collection. nil for anything else.
func wholeBatchRefusal(err error) error {
	if qe := asQuotaError(err); qe != nil {
		return qe
	}
	if re := asReadOnlyError(err); re != nil {
		return re
	}
	return nil
}

// writeReadOnlyError answers a read-only refusal over HTTP and reports
// whether err was one. Called from bad(), so every REST handler that reports
// a write failure maps it without knowing about read-only collections.
func writeReadOnlyError(w http.ResponseWriter, err error) bool {
	re := asReadOnlyError(err)
	if re == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(re.detail())
	return true
}
