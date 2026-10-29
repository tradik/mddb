package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/binlog"
	json "mddb/internal/jsonx"
	"mddb/internal/storage"
)

// Partial updates and optimistic concurrency (INT-021).
//
// An agent that edits a file reads it, changes it and writes it back. Two
// agents doing that to one document at once — Deep Agents' asynchronous
// subagents do exactly this — lose one of the edits, silently. A write that
// names the revision it was based on fails instead, with a 409 and the
// current revision, when somebody else wrote in between.
//
// The update itself had three copies, one per transport, and they had
// drifted: the MCP one rewrote the document without its metadata index, its
// revision, the binlog or the post-write hooks, so FTS and embeddings kept
// the old text. They are one function now, with the check inside the write
// transaction where it cannot race.

// docRevision identifies a document version: the first 16 hex digits of a
// SHA-256 over what a write can change. Reading a document and writing back
// with this value succeeds only if nothing was written in between.
func docRevision(doc *storage.Doc) string {
	if doc == nil || doc.ID == "" {
		return ""
	}
	// jsonx encodes map keys sorted, so equal documents hash equal; no meta
	// and empty meta are the same document however they were stored.
	meta := doc.Meta
	if len(meta) == 0 {
		meta = nil
	}
	data, _ := json.Marshal(struct {
		Content   string              `json:"c"`
		Meta      map[string][]string `json:"m"`
		Expires   int64               `json:"e"`
		UpdatedAt int64               `json:"u"`
	}{doc.ContentMD, meta, doc.ExpiresAt, doc.UpdatedAt})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// RevisionConflictError is a conditional write that lost: the document is
// not at the revision the writer expected.
type RevisionConflictError struct {
	Expected string
	// Current is the document's revision now; "" when it does not exist.
	Current string
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("revision conflict: expected %s, current %q", e.Expected, e.Current)
}

// checkRevision fails a conditional write whose expectation does not hold.
// An empty expectation is not a condition.
func checkRevision(existing *storage.Doc, expected string) error {
	if expected == "" {
		return nil
	}
	if cur := docRevision(existing); cur != expected {
		return &RevisionConflictError{Expected: expected, Current: cur}
	}
	return nil
}

// DocPatch is a partial update: nil fields are left as they are.
type DocPatch struct {
	Meta    map[string][]string // replaces the meta when HasMeta
	HasMeta bool
	Content *string
	TTL     *int64 // seconds from now; 0 clears the expiry
	// ExpectedRevision makes the update conditional (see docRevision).
	ExpectedRevision string
}

func (p DocPatch) empty() bool { return !p.HasMeta && p.Content == nil && p.TTL == nil }

// updateDocument applies a patch: one implementation for REST, gRPC, MCP and
// GraphQL. Schema validation, the revision check, the metadata index, code
// symbols, the revision history, the binlog and the post-write hooks all
// happen here, so no transport can skip one.
func (s *Server) updateDocument(collection, key, lang string, p DocPatch) (storage.Doc, error) {
	if collection == "" || key == "" || lang == "" {
		return storage.Doc{}, errors.New("missing required fields: collection, key, lang")
	}
	if p.empty() {
		return storage.Doc{}, errors.New("no fields to update")
	}
	if p.HasMeta && s.SchemaManager != nil {
		if err := s.SchemaManager.Validate(collection, p.Meta); err != nil {
			return storage.Doc{}, err
		}
	}
	now := time.Now().Unix()
	var saved storage.Doc
	var bo binlog.BinlogOps
	summaryChanged := false // RAG-009: a new summary is embedded and indexed like new content
	err := s.DBUpdateC(collection, func(tx *bolt.Tx) error {
		bDocs, bIdx, bByK := tx.Bucket([]byte("docs")), tx.Bucket([]byte("idxmeta")), tx.Bucket([]byte("bykey"))
		docID := bByK.Get(storage.ByKeyKey(collection, key, lang))
		if docID == nil {
			return errDocNotFound
		}
		existing, err := loadDoc(bDocs.Get(storage.DocKey(collection, string(docID))))
		if err != nil {
			return errDocNotFound
		}
		if existing.ExpiresAt > 0 && existing.ExpiresAt < now {
			return errDocNotFound
		}
		if err := checkRevision(existing, p.ExpectedRevision); err != nil {
			return err
		}
		doc := applyPatch(*existing, p, now)
		summaryChanged = documentSummary(existing) != documentSummary(&doc)
		EnrichCodeSymbolsWith(&doc, s.codeIncludePaths(collection))
		buf, err := marshalAndEncrypt(&doc, collection)
		if err != nil {
			return err
		}
		docKey := storage.DocKey(collection, doc.ID)
		if err := s.checkWriteQuota(tx, collection, oneQuotaWrite(docKey, buf)); err != nil { // GO-043
			return err
		}
		if err := bDocs.Put(docKey, buf); err != nil {
			return err
		}
		bo.Put("docs", docKey, buf)
		if metadataChanged(existing.Meta, doc.Meta) {
			if err := reindexMeta(bIdx, &bo, collection, doc.ID, existing.Meta, doc.Meta); err != nil {
				return err
			}
		}
		saved = doc
		return s.writeRevision(tx, &bo, collection, doc.ID, now, buf)
	})
	if err != nil {
		return storage.Doc{}, err
	}
	bo.FlushTo(s.Binlog)
	s.runPostUpdateHooks(collection, key, lang, saved, p.Content != nil || summaryChanged, p.TTL != nil)
	return saved, nil
}

// applyPatch returns doc with the patch's fields applied.
func applyPatch(doc storage.Doc, p DocPatch, now int64) storage.Doc {
	doc.UpdatedAt = now
	if p.HasMeta {
		doc.Meta = p.Meta
	}
	if p.Content != nil {
		doc.ContentMD = *p.Content
	}
	if p.TTL != nil {
		doc.ExpiresAt = 0
		if *p.TTL > 0 {
			doc.ExpiresAt = now + *p.TTL
		}
	}
	return doc
}

// reindexMeta replaces a document's metadata index entries.
func reindexMeta(bIdx *bolt.Bucket, bo *binlog.BinlogOps, collection, docID string, old, cur map[string][]string) error {
	for mk, vals := range old {
		for _, mv := range vals {
			mkey := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(docID)...)
			_ = bIdx.Delete(mkey)
			bo.Delete("idxmeta", mkey)
		}
	}
	for mk, vals := range cur {
		for _, mv := range vals {
			mkey := append(storage.MetaKeyPrefix(collection, mk, mv), []byte(docID)...)
			if err := bIdx.Put(mkey, []byte("1")); err != nil {
				return err
			}
			bo.Put("idxmeta", mkey, []byte("1"))
		}
	}
	return nil
}

// boltWrite is one key/value bound for a bucket; name is the bucket's name as
// the binlog records it.
type boltWrite struct {
	bucket     *bolt.Bucket
	name       string
	key, value []byte
}

// putLogged stores each write in order and records it for replication. The
// first failure stops it; the caller's transaction then rolls back, so the
// writes already made never reach the database or the binlog.
func putLogged(bo *binlog.BinlogOps, writes ...boltWrite) error {
	for _, w := range writes {
		if err := w.bucket.Put(w.key, w.value); err != nil {
			return err
		}
		bo.Put(w.name, w.key, w.value)
	}
	return nil
}

// writeRevision records a version in the history and keeps the history
// within the collection's MaxRevisions.
func (s *Server) writeRevision(tx *bolt.Tx, bo *binlog.BinlogOps, collection, docID string, now int64, buf []byte) error {
	bRev := tx.Bucket([]byte("rev"))
	rkey := nextRevisionKey(bRev, collection, docID, now)
	if err := bRev.Put(rkey, buf); err != nil {
		return err
	}
	bo.Put("rev", rkey, buf)
	if s.CollectionManager != nil {
		if cfg, found := s.CollectionManager.Get(collection); found && cfg.MaxRevisions > 0 {
			return trimRevisions(tx, bo, collection, docID, cfg.MaxRevisions)
		}
	}
	return nil
}

// writeDocWriteError maps a write failure to its HTTP status: 404 for a
// missing document, 409 with the current revision for a lost race, 400 for
// the rest.
func writeDocWriteError(w http.ResponseWriter, err error) {
	var conflict *RevisionConflictError
	switch {
	case errors.Is(err, errDocNotFound):
		http.Error(w, `{"error":"document not found"}`, http.StatusNotFound)
	case errors.As(err, &conflict):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "revision conflict", "expectedRevision": conflict.Expected, "currentRevision": conflict.Current,
		})
	default:
		bad(w, err)
	}
}
