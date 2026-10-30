package main

import (
	"bytes"
	"fmt"
	"mddb/internal/storage"
	"net/http"
	"sort"
	"strconv"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// RevisionListRequest is the request for listing document revisions.
type RevisionListRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
}

// RevisionEntry represents a single revision in the list.
type RevisionEntry struct {
	Timestamp int64               `json:"timestamp"`
	UpdatedAt int64               `json:"updatedAt"`
	ContentMD string              `json:"contentMd,omitempty"`
	Meta      map[string][]string `json:"meta,omitempty"`
}

// RevisionListResponse is the response containing revision history.
type RevisionListResponse struct {
	Collection string          `json:"collection"`
	Key        string          `json:"key"`
	Lang       string          `json:"lang"`
	Revisions  []RevisionEntry `json:"revisions"`
	Total      int             `json:"total"`
}

// RevisionRestoreRequest is the request to restore a specific revision.
type RevisionRestoreRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
	Timestamp  int64  `json:"timestamp"`
}

// handleRevisions handles POST /v1/revisions — list revision history for a document.
func (s *Server) handleRevisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RevisionListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		bad(w, fmt.Errorf("missing required fields: collection, key, lang"))
		return
	}

	// Check read permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("revisions_list", req.Collection)
	}

	docID := genID(req.Collection, req.Key, req.Lang)
	revisions, err := s.listRevisions(req.Collection, docID)
	if err != nil {
		bad(w, err)
		return
	}

	ok(w, RevisionListResponse{
		Collection: req.Collection,
		Key:        req.Key,
		Lang:       req.Lang,
		Revisions:  revisions,
		Total:      len(revisions),
	})
}

// listRevisions reads the revision history of a document, newest first. It
// is the one reader behind REST /v1/revisions, the ListRevisions RPC and the
// MCP list_revisions tool. A key whose timestamp suffix does not parse, or a
// revision that cannot be decoded, is skipped.
func (s *Server) listRevisions(collection, docID string) ([]RevisionEntry, error) {
	var revisions []RevisionEntry
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		if bRev == nil {
			return nil
		}
		prefix := storage.RevPrefix(collection, docID)
		c := bRev.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			if entry, ok := revisionEntry(k, v); ok {
				revisions = append(revisions, entry)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Timestamp > revisions[j].Timestamp
	})
	return revisions, nil
}

// revisionEntry decodes one stored revision; the timestamp is the key's
// suffix after the final '|'. ok is false when either part is unreadable.
func revisionEntry(k, v []byte) (entry RevisionEntry, ok bool) {
	lastPipe := bytes.LastIndexByte(k, '|')
	if lastPipe < 0 || lastPipe >= len(k)-1 {
		return entry, false
	}
	ts, err := strconv.ParseInt(string(k[lastPipe+1:]), 10, 64)
	if err != nil {
		return entry, false
	}
	docPtr, err := loadDoc(v)
	if err != nil {
		return entry, false
	}
	return RevisionEntry{
		Timestamp: ts,
		UpdatedAt: docPtr.UpdatedAt,
		ContentMD: docPtr.ContentMD,
		Meta:      docPtr.Meta,
	}, true
}

// handleRevisionRestore handles POST /v1/revisions/restore — restore a document to a previous revision.
func (s *Server) handleRevisionRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RevisionRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" || req.Timestamp == 0 {
		bad(w, fmt.Errorf("missing required fields: collection, key, lang, timestamp"))
		return
	}

	// Check write permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), req.Collection, PermWrite); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("revision_restore", req.Collection)
	}

	doc, err := s.restoreRevision(req.Collection, req.Key, req.Lang, req.Timestamp)
	if err != nil {
		bad(w, err)
		return
	}

	ok(w, doc)
}
