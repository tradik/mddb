package main

import (
	"bytes"
	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	"net/http"
	"sort"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// handleMemorySessionsList lists sessions with optional filtering.
func (s *Server) handleMemorySessionsList(w http.ResponseWriter, r *http.Request) {
	var req MemorySessionsListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 50
	}

	sessions, err := s.listMemorySessions(sessionFilterMeta(req))
	if err != nil {
		bad(w, err)
		return
	}
	sortMemorySessions(sessions, req.Sort, req.Asc)
	ok(w, MemorySessionsListResponse{
		Sessions: pageMemorySessions(sessions, req.Offset, req.Limit),
		Total:    len(sessions),
	})
}

// sessionFilterMeta is the metadata filter a session listing asks for: always
// type=session, plus the user and scenario when given.
func sessionFilterMeta(req MemorySessionsListRequest) map[string][]string {
	filterMeta := map[string][]string{
		"type": {"session"},
	}
	if req.UserID != "" {
		filterMeta["userId"] = []string{req.UserID}
	}
	if req.Scenario != "" {
		filterMeta["scenario"] = []string{req.Scenario}
	}
	return filterMeta
}

// listMemorySessions reads the sessions that match filterMeta and have not
// expired, unsorted.
func (s *Server) listMemorySessions(filterMeta map[string][]string) ([]MemorySessionDetail, error) {
	var sessions []MemorySessionDetail
	err := s.DBViewC(memorySessionsCollection, func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bDocs == nil || bIdx == nil {
			return nil
		}
		for _, docID := range sessionDocIDs(bDocs, bIdx, filterMeta) {
			if d, live := s.memorySessionDetail(bDocs, docID); live {
				sessions = append(sessions, d)
			}
		}
		return nil
	})
	return sessions, err
}

// sessionDocIDs lists the session document IDs: through the metadata index
// when the filter has more than the type, else every session document.
func sessionDocIDs(bDocs, bIdx *bolt.Bucket, filterMeta map[string][]string) []string {
	if len(filterMeta) <= 1 { // just "type": scan all sessions
		return prefixSuffixes(bDocs, []byte("doc|"+memorySessionsCollection+"|"))
	}
	var sets [][]string
	for mk, mvals := range filterMeta {
		var ids []string
		for _, mv := range mvals {
			ids = append(ids, prefixSuffixes(bIdx, storage.MetaKeyPrefix(memorySessionsCollection, mk, mv))...)
		}
		sets = append(sets, sliceutil.Unique(ids))
	}
	return intersect(sets...)
}

// prefixSuffixes returns, in key order, what follows prefix in every key of
// b that starts with it.
func prefixSuffixes(b *bolt.Bucket, prefix []byte) []string {
	var out []string
	c := b.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		out = append(out, string(k[len(prefix):]))
	}
	return out
}

// memorySessionDetail loads one session. live is false for a session that is
// missing, cannot be decoded or has expired.
func (s *Server) memorySessionDetail(bDocs *bolt.Bucket, docID string) (detail MemorySessionDetail, live bool) {
	v := bDocs.Get(storage.DocKey(memorySessionsCollection, docID))
	if v == nil {
		return detail, false
	}
	d, err := loadDoc(v)
	if err != nil {
		return detail, false
	}
	if d.ExpiresAt > 0 && d.ExpiresAt < time.Now().Unix() {
		return detail, false
	}
	return MemorySessionDetail{
		SessionID:    d.Key,
		UserID:       metaFirst(d.Meta, "userId"),
		Scenario:     metaFirst(d.Meta, "scenario"),
		Title:        metaFirst(d.Meta, "title"),
		CreatedAt:    d.AddedAt,
		UpdatedAt:    d.UpdatedAt,
		ExpiresAt:    d.ExpiresAt,
		MessageCount: s.countSessionMessages(d.Key),
	}, true
}

// sortMemorySessions orders sessions by updatedAt or, by default, createdAt;
// newest first unless asc.
func sortMemorySessions(sessions []MemorySessionDetail, by string, asc bool) {
	at := func(d MemorySessionDetail) int64 { return d.CreatedAt }
	if by == "updatedAt" {
		at = func(d MemorySessionDetail) int64 { return d.UpdatedAt }
	}
	sort.Slice(sessions, func(i, j int) bool {
		if asc {
			return at(sessions[i]) < at(sessions[j])
		}
		return at(sessions[i]) > at(sessions[j])
	})
}

// pageMemorySessions applies offset and limit; the result is never nil, so
// the response always carries a list.
func pageMemorySessions(sessions []MemorySessionDetail, offset, limit int) []MemorySessionDetail {
	if offset > 0 && offset < len(sessions) {
		sessions = sessions[offset:]
	} else if offset >= len(sessions) {
		sessions = nil
	}
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}
	if sessions == nil {
		sessions = []MemorySessionDetail{}
	}
	return sessions
}
