package main

import (
	"fmt"
	"mddb/internal/binlog"
	"mddb/internal/storage"
	"net/http"
	"time"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// --- HTTP handlers ---

// SetTTLRequest represents a request to set/remove TTL on a document.
type SetTTLRequest struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	Lang       string `json:"lang"`
	TTL        int64  `json:"ttl"` // seconds; 0 = remove TTL
}

func (s *Server) handleSetTTL(w http.ResponseWriter, r *http.Request) {
	var req SetTTLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" || req.Key == "" || req.Lang == "" {
		bad(w, fmt.Errorf("missing required fields"))
		return
	}

	docID := genID(req.Collection, req.Key, req.Lang)

	// Update ExpiresAt on the document itself
	now := time.Now().Unix()
	var expiresAt int64
	if req.TTL > 0 {
		expiresAt = now + req.TTL
	}

	// Update document in DB
	var updated storage.Doc
	var bo binlog.BinlogOps
	err := s.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		dk := storage.DocKey(req.Collection, docID)
		v := bDocs.Get(dk)
		if v == nil {
			return fmt.Errorf("document not found")
		}
		docPtr, err := loadDoc(v)
		if err != nil {
			return err
		}
		updated = *docPtr
		updated.ExpiresAt = expiresAt
		buf, err := marshalDoc(&updated)
		if err != nil {
			return err
		}
		bo.Put("docs", dk, buf)
		return bDocs.Put(dk, buf)
	})
	if err == nil {
		bo.FlushTo(s.Binlog)
	}
	if err != nil {
		bad(w, err)
		return
	}

	// Update TTL bucket
	if s.TTLManager != nil {
		if expiresAt > 0 {
			_ = s.TTLManager.Set(req.Collection, docID, expiresAt)
		} else {
			_ = s.TTLManager.Remove(req.Collection, docID)
		}
	}

	ok(w, updated)
}
