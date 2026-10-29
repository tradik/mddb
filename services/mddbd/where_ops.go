package main

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// Operations by metadata filter (GO-045): delete, merge metadata into, or set
// the time to live of every document a filter matches — instead of listing
// keys and sending them back in batches, with a race between the two.
//
// Each document goes through the same write path as a single delete or
// update (revisions, FTS, vectors, webhooks, binlog); there is no shortcut
// beside it. A call affects at most limit documents and returns a cursor to
// continue; dryRun reports what would be affected and writes nothing.

const (
	defaultWhereLimit = 1000
	maxWhereLimit     = 10000
	whereSampleSize   = 20
)

// WhereOp names the operation.
type WhereOp string

// The three operations.
const (
	WhereDelete WhereOp = "delete"
	WhereUpdate WhereOp = "update"
	WhereSetTTL WhereOp = "setTTL"
)

// WhereRequest selects documents by metadata and says what to do with them.
type WhereRequest struct {
	Collection string              `json:"collection"`
	FilterMeta map[string][]string `json:"filterMeta,omitempty"`
	Filter     MetaFilter          `json:"filter,omitempty"`
	// Limit caps the documents affected by this call (default 1000, max 10000).
	Limit int `json:"limit,omitempty"`
	// Cursor continues after the last document of a previous call.
	Cursor string `json:"cursor,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
	// Meta is merged into each document's metadata (updateWhere): its keys
	// replace the document's values for those keys; content is untouched.
	Meta map[string][]string `json:"meta,omitempty"`
	// TTL is seconds from now for setTTLWhere; 0 removes the expiry.
	TTL *int64 `json:"ttl,omitempty"`
}

// WhereResult reports what a call matched and did.
type WhereResult struct {
	// Matched counts the documents the filter matches from the cursor on.
	Matched  int `json:"matched"`
	Affected int `json:"affected"`
	// Sample holds the keys of the first documents affected (or, in a dry
	// run, that would be).
	Sample     []string `json:"sample"`
	NextCursor string   `json:"nextCursor,omitempty"`
	DryRun     bool     `json:"dryRun,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

// validate rejects a request that cannot be run as asked.
func (r *WhereRequest) validate(op WhereOp) error {
	if r.Collection == "" {
		return errors.New("collection is required")
	}
	if len(mergeFilters(r.FilterMeta, r.Filter)) == 0 {
		return errors.New("a filter or filterMeta is required: an operation on every document is not run by filter")
	}
	if r.Limit < 0 || r.Limit > maxWhereLimit {
		return fmt.Errorf("limit must be between 1 and %d", maxWhereLimit)
	}
	switch op {
	case WhereUpdate:
		if len(r.Meta) == 0 {
			return errors.New("meta is required for updateWhere")
		}
	case WhereSetTTL:
		if r.TTL == nil || *r.TTL < 0 {
			return errors.New("ttl (seconds, 0 to remove the expiry) is required for setTTLWhere")
		}
	}
	return nil
}

// RunWhere matches, then deletes, updates or sets TTL document by document.
func (s *Server) RunWhere(op WhereOp, req WhereRequest) (*WhereResult, error) {
	if err := req.validate(op); err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit == 0 {
		limit = defaultWhereLimit
	}
	ids, _ := s.allowedDocIDs(req.Collection, req.FilterMeta, req.Filter)
	var todo []string
	for _, id := range sortedIDs(ids) {
		if id > req.Cursor {
			todo = append(todo, id)
		}
	}
	res := &WhereResult{Matched: len(todo), DryRun: req.DryRun, Sample: []string{}}
	if len(todo) > limit {
		todo = todo[:limit]
		res.NextCursor = todo[len(todo)-1]
	}
	for _, id := range todo {
		doc := s.whereDoc(req.Collection, id)
		if doc == nil {
			continue
		}
		if !req.DryRun {
			if err := s.applyWhere(op, req, doc); err != nil {
				res.Errors = append(res.Errors, doc.Key+": "+err.Error())
				continue
			}
		}
		res.Affected++
		if len(res.Sample) < whereSampleSize {
			res.Sample = append(res.Sample, doc.Key)
		}
	}
	return res, nil
}

// whereDoc loads a matched document; nil when it is gone or unreadable.
func (s *Server) whereDoc(collection, id string) *storage.Doc {
	var doc *storage.Doc
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		if v := tx.Bucket([]byte("docs")).Get(storage.DocKey(collection, id)); v != nil {
			doc, _ = loadDoc(v)
		}
		return nil
	})
	return doc
}

// applyWhere runs one document through the single-document write path.
func (s *Server) applyWhere(op WhereOp, req WhereRequest, doc *storage.Doc) error {
	switch op {
	case WhereDelete:
		return s.deleteDocumentInternal(req.Collection, doc.Key, doc.Lang)
	case WhereUpdate:
		merged := maps.Clone(doc.Meta)
		if merged == nil {
			merged = map[string][]string{}
		}
		maps.Copy(merged, req.Meta)
		_, err := s.updateDocument(req.Collection, doc.Key, doc.Lang, DocPatch{HasMeta: true, Meta: merged})
		return err
	default: // WhereSetTTL
		_, err := s.updateDocument(req.Collection, doc.Key, doc.Lang, DocPatch{TTL: req.TTL})
		return err
	}
}

// handleWhere serves POST /v1/documents:deleteWhere, :updateWhere and
// :setTTLWhere. A dry run only reads, so a read-only server answers it.
func (s *Server) handleWhere(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	op := map[string]WhereOp{"deleteWhere": WhereDelete, "updateWhere": WhereUpdate, "setTTLWhere": WhereSetTTL}[strings.TrimPrefix(r.URL.Path, "/v1/documents:")]
	var req WhereRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	run := func(w http.ResponseWriter, r *http.Request) {
		perm := PermWrite
		if req.DryRun {
			perm = PermRead
		}
		if !s.permitted(w, r, req.Collection, perm) {
			return
		}
		res, err := s.RunWhere(op, req)
		if err != nil {
			bad(w, err)
			return
		}
		ok(w, res)
	}
	if req.DryRun {
		run(w, r)
		return
	}
	s.guardWrite(run)(w, r)
}
