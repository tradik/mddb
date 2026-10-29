package main

import (
	"mddb/internal/binlog"
	"mddb/internal/storage"
	"net/http"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
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
	updated, err := s.setDocumentTTL(req.Collection, req.Key, req.Lang, req.TTL)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, updated)
}

// setDocumentTTL sets or clears a document's expiry: the one implementation
// behind REST /v1/set-ttl, MCP set_ttl and gRPC SetTTL, which were three
// copies. ttl is seconds from now; 0 clears the expiry. The document is
// stored as every write stores it — encrypted when the collection is,
// recorded in the binlog — and the read caches drop their copy.
func (s *Server) setDocumentTTL(collection, key, lang string, ttl int64) (storage.Doc, error) {
	if collection == "" || key == "" || lang == "" {
		return storage.Doc{}, invalidInput("missing required fields: collection, key, lang")
	}
	docID := genID(collection, key, lang)
	var expiresAt int64
	if ttl > 0 {
		expiresAt = time.Now().Unix() + ttl
	}
	var updated storage.Doc
	var bo binlog.BinlogOps
	err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		dk := storage.DocKey(collection, docID)
		v := bDocs.Get(dk)
		if v == nil {
			return errDocNotFound
		}
		docPtr, err := loadDoc(v)
		if err != nil {
			return err
		}
		updated = *docPtr
		updated.ExpiresAt = expiresAt
		buf, err := marshalAndEncrypt(&updated, collection)
		if err != nil {
			return err
		}
		bo.Put("docs", dk, buf)
		return bDocs.Put(dk, buf)
	})
	if err != nil {
		return storage.Doc{}, err
	}
	bo.FlushTo(s.Binlog)
	s.invalidateDocReadCaches(collection, key, lang)
	if s.TTLManager != nil {
		if expiresAt > 0 {
			_ = s.TTLManager.Set(collection, docID, expiresAt)
		} else {
			_ = s.TTLManager.Remove(collection, docID)
		}
	}
	return updated, nil
}
