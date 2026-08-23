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

	var revisions []RevisionEntry
	err := s.DBView(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		if bRev == nil {
			return nil
		}

		prefix := storage.RevPrefix(req.Collection, docID)
		c := bRev.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			// Extract timestamp from key suffix (last part after final |)
			keyStr := string(k)
			lastPipe := bytes.LastIndexByte(k, '|')
			if lastPipe < 0 || lastPipe >= len(k)-1 {
				continue
			}
			tsStr := keyStr[lastPipe+1:]
			ts, err := strconv.ParseInt(tsStr, 10, 64)
			if err != nil {
				continue
			}

			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}

			revisions = append(revisions, RevisionEntry{
				Timestamp: ts,
				UpdatedAt: docPtr.UpdatedAt,
				ContentMD: docPtr.ContentMD,
				Meta:      docPtr.Meta,
			})
		}
		return nil
	})
	if err != nil {
		bad(w, err)
		return
	}

	// Sort newest first
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Timestamp > revisions[j].Timestamp
	})

	ok(w, RevisionListResponse{
		Collection: req.Collection,
		Key:        req.Key,
		Lang:       req.Lang,
		Revisions:  revisions,
		Total:      len(revisions),
	})
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

	docID := genID(req.Collection, req.Key, req.Lang)

	// Load the specific revision
	tsKey := fmt.Sprintf("%020d", req.Timestamp)
	revKey := append(storage.RevPrefix(req.Collection, docID), []byte(tsKey)...)

	var revDoc *storage.Doc
	err := s.DBView(func(tx *bolt.Tx) error {
		bRev := tx.Bucket([]byte("rev"))
		if bRev == nil {
			return fmt.Errorf("revision not found")
		}
		v := bRev.Get(revKey)
		if v == nil {
			return fmt.Errorf("revision not found for timestamp %d", req.Timestamp)
		}
		var err error
		revDoc, err = loadDoc(v)
		return err
	})
	if err != nil {
		bad(w, err)
		return
	}

	// Restore by saving through addDocument (handles binlog, FTS, embeddings, webhooks)
	doc, _, err := s.addDocument(req.Collection, req.Key, req.Lang, revDoc.Meta, revDoc.ContentMD, 0, true)
	if err != nil {
		bad(w, err)
		return
	}

	ok(w, doc)
}
