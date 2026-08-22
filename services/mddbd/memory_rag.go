package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mddb/internal/sliceutil"
	"mddb/internal/storage"
	"mddb/internal/vector"
	"net/http"
	"sort"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	bolt "go.etcd.io/bbolt"
)

// Memory RAG collection naming convention.
const (
	memorySessionsCollection  = "memory_sessions"
	memoryMessagesCollection  = "memory_messages"
	memorySummariesCollection = "memory_summaries"
)

// Default configuration for memory RAG.
const (
	defaultSessionTTL  int64 = 30 * 24 * 3600 // 30 days
	defaultRecallTopK        = 10
	defaultRecallAlpha       = 0.5
)

// MemorySessionRequest is the request body for creating a new memory session.
type MemorySessionRequest struct {
	UserID   string            `json:"userId"`
	Scenario string            `json:"scenario,omitempty"`
	Title    string            `json:"title,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
	TTL      int64             `json:"ttl,omitempty"` // seconds; 0 = use default (30 days)
}

// MemorySessionResponse is the response from creating a memory session.
type MemorySessionResponse struct {
	SessionID string `json:"sessionId"`
	UserID    string `json:"userId"`
	Scenario  string `json:"scenario,omitempty"`
	Title     string `json:"title,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

// MemoryMessageRequest is the request body for adding a message to a session.
type MemoryMessageRequest struct {
	SessionID string            `json:"sessionId"`
	Role      string            `json:"role"` // user, assistant, system
	Content   string            `json:"content"`
	Meta      map[string]string `json:"meta,omitempty"` // extra: topic, source, tool_call, etc.
}

// MemoryMessageResponse is the response from adding a message.
type MemoryMessageResponse struct {
	MessageID string `json:"messageId"`
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"createdAt"`
	Embedded  bool   `json:"embedded"` // whether embedding was enqueued
}

// MemoryRecallRequest is the request body for semantic recall across sessions.
type MemoryRecallRequest struct {
	Query          string              `json:"query"`
	UserID         string              `json:"userId,omitempty"`
	SessionID      string              `json:"sessionId,omitempty"`
	Role           string              `json:"role,omitempty"` // filter by role
	TopK           int                 `json:"topK,omitempty"`
	Threshold      float64             `json:"threshold,omitempty"`
	Strategy       string              `json:"strategy,omitempty"`       // "hybrid" (default), "semantic", "keyword"
	Alpha          float64             `json:"alpha,omitempty"`          // 0=keyword, 1=semantic (default 0.5)
	IncludeContent bool                `json:"includeContent,omitempty"` // include full message content
	FilterMeta     map[string][]string `json:"filterMeta,omitempty"`
}

// MemoryRecallResultItem represents a single recall result.
type MemoryRecallResultItem struct {
	Document      storage.Doc `json:"document"`
	Score         float64     `json:"score"`
	Rank          int         `json:"rank"`
	SessionID     string      `json:"sessionId"`
	Role          string      `json:"role"`
	MatchStrategy string      `json:"matchStrategy"` // "semantic", "keyword", "hybrid"
}

// MemoryRecallResponse is the response from a recall query.
type MemoryRecallResponse struct {
	Results  []MemoryRecallResultItem `json:"results"`
	Total    int                      `json:"total"`
	Strategy string                   `json:"strategy"`
	Query    string                   `json:"query"`
	// ContextTruncated reports that the messages collection's
	// contextTokenBudget dropped results from the tail (RAG-001).
	ContextTruncated bool `json:"contextTruncated,omitempty"`
}

// MemorySummarizeRequest is the request body for summarizing a session.
type MemorySummarizeRequest struct {
	SessionID string `json:"sessionId"`
	UserID    string `json:"userId,omitempty"` // for validation
}

// MemorySummarizeResponse is the response from summarizing a session.
type MemorySummarizeResponse struct {
	SummaryID string `json:"summaryId"`
	SessionID string `json:"sessionId"`
	Summary   string `json:"summary"`
	CreatedAt int64  `json:"createdAt"`
	Messages  int    `json:"messages"` // number of messages summarized
}

// MemorySessionsListRequest is the request for listing sessions.
type MemorySessionsListRequest struct {
	UserID   string `json:"userId,omitempty"`
	Scenario string `json:"scenario,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Sort     string `json:"sort,omitempty"` // createdAt (default), updatedAt
	Asc      bool   `json:"asc,omitempty"`
}

// MemorySessionDetail represents a session with message count.
type MemorySessionDetail struct {
	SessionID    string `json:"sessionId"`
	UserID       string `json:"userId"`
	Scenario     string `json:"scenario,omitempty"`
	Title        string `json:"title,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
	MessageCount int    `json:"messageCount"`
}

// MemorySessionsListResponse is the response for listing sessions.
type MemorySessionsListResponse struct {
	Sessions []MemorySessionDetail `json:"sessions"`
	Total    int                   `json:"total"`
}

// MemoryHistoryRequest is the request for fetching session message history.
type MemoryHistoryRequest struct {
	SessionID string `json:"sessionId"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

// MemoryHistoryResponse is the response for session message history.
type MemoryHistoryResponse struct {
	Messages []storage.Doc `json:"messages"`
	Total    int           `json:"total"`
}

// generateMemoryMessageID creates a unique message identifier.
func generateMemoryMessageID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// handleMemorySessionCreate creates a new memory session.
func (s *Server) handleMemorySessionCreate(w http.ResponseWriter, r *http.Request) {
	var req MemorySessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.UserID == "" {
		bad(w, errors.New("missing userId"))
		return
	}

	sessionID := generateSessionID()
	now := time.Now().Unix()
	ttl := req.TTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}

	title := req.Title
	if title == "" {
		title = "Session " + sessionID[:8]
	}

	meta := map[string][]string{
		"userId":   {req.UserID},
		"type":     {"session"},
		"status":   {"active"},
		"scenario": {req.Scenario},
	}
	for k, v := range req.Meta {
		meta[k] = []string{v}
	}

	content := fmt.Sprintf("# Session: %s\n\nUser: %s\nScenario: %s\nCreated: %s",
		title, req.UserID, req.Scenario, time.Unix(now, 0).UTC().Format(time.RFC3339))

	saved, _, err := s.addDocument(memorySessionsCollection, sessionID, "en", meta, content, ttl, true)
	if err != nil {
		bad(w, err)
		return
	}

	resp := MemorySessionResponse{
		SessionID: sessionID,
		UserID:    req.UserID,
		Scenario:  req.Scenario,
		Title:     title,
		CreatedAt: saved.AddedAt,
		ExpiresAt: saved.ExpiresAt,
	}
	ok(w, resp)
}

// handleMemoryMessageAdd adds a message to an existing session.
func (s *Server) handleMemoryMessageAdd(w http.ResponseWriter, r *http.Request) {
	var req MemoryMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.SessionID == "" || req.Role == "" || req.Content == "" {
		bad(w, errors.New("missing sessionId, role, or content"))
		return
	}

	validRoles := map[string]bool{"user": true, "assistant": true, "system": true, "tool": true}
	if !validRoles[req.Role] {
		bad(w, errors.New("invalid role: must be user, assistant, system, or tool"))
		return
	}

	// Verify session exists
	var sessionExists bool
	_ = s.DBView(func(tx *bolt.Tx) error {
		bByK := tx.Bucket([]byte("bykey"))
		if bByK == nil {
			return nil
		}
		if bByK.Get(storage.ByKeyKey(memorySessionsCollection, req.SessionID, "en")) != nil {
			sessionExists = true
		}
		return nil
	})
	if !sessionExists {
		bad(w, errors.New("session not found: "+req.SessionID))
		return
	}

	msgID := generateMemoryMessageID()
	now := time.Now().Unix()

	meta := map[string][]string{
		"sessionId": {req.SessionID},
		"role":      {req.Role},
		"type":      {"message"},
		"timestamp": {fmt.Sprintf("%d", now)},
	}
	for k, v := range req.Meta {
		meta[k] = []string{v}
	}

	// Key format: sessionID/timestamp-msgID for chronological ordering
	msgKey := fmt.Sprintf("%s/%020d-%s", req.SessionID, now, msgID)

	saved, _, err := s.addDocument(memoryMessagesCollection, msgKey, "en", meta, req.Content, 0, true)
	if err != nil {
		bad(w, err)
		return
	}

	// Update session's updatedAt by touching it
	go s.touchSession(req.SessionID)

	embedded := s.EmbeddingWorker != nil && req.Content != ""
	resp := MemoryMessageResponse{
		MessageID: saved.ID,
		SessionID: req.SessionID,
		Role:      req.Role,
		CreatedAt: saved.AddedAt,
		Embedded:  embedded,
	}
	ok(w, resp)
}

// touchSession updates the session's updatedAt timestamp.
func (s *Server) touchSession(sessionID string) {
	_ = s.DBUpdate(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bByK := tx.Bucket([]byte("bykey"))
		if bDocs == nil || bByK == nil {
			return nil
		}
		docIDBytes := bByK.Get(storage.ByKeyKey(memorySessionsCollection, sessionID, "en"))
		if docIDBytes == nil {
			return nil
		}
		v := bDocs.Get(storage.DocKey(memorySessionsCollection, string(docIDBytes)))
		if v == nil {
			return nil
		}
		docPtr, err := loadDoc(v)
		if err != nil {
			return err
		}
		docPtr.UpdatedAt = time.Now().Unix()
		buf, err := marshalAndEncrypt(docPtr, memorySessionsCollection)
		if err != nil {
			return err
		}
		return bDocs.Put(storage.DocKey(memorySessionsCollection, string(docIDBytes)), buf)
	})
}

// handleMemoryRecall performs semantic/hybrid recall across memory messages.
func (s *Server) handleMemoryRecall(w http.ResponseWriter, r *http.Request) {
	var req MemoryRecallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Query == "" {
		bad(w, errors.New("missing query"))
		return
	}

	// RAG-001: memory recall always searches the fixed messages collection,
	// so a profile set on it configures recall for every caller.
	topK := s.ResolveTopK(memoryMessagesCollection, req.TopK, defaultRecallTopK)
	strategy := req.Strategy
	if strategy == "" {
		strategy = "hybrid"
	}

	// Build metadata filter
	filterMeta := make(map[string][]string)
	filterMeta["type"] = []string{"message"}
	if req.UserID != "" {
		// Get sessions for this user first, then filter messages by sessionId
		sessionIDs := s.getSessionIDsForUser(req.UserID)
		if len(sessionIDs) == 0 {
			ok(w, MemoryRecallResponse{Results: []MemoryRecallResultItem{}, Strategy: strategy, Query: req.Query})
			return
		}
		filterMeta["sessionId"] = sessionIDs
	}
	if req.SessionID != "" {
		filterMeta["sessionId"] = []string{req.SessionID}
	}
	if req.Role != "" {
		filterMeta["role"] = []string{req.Role}
	}
	for k, v := range req.FilterMeta {
		filterMeta[k] = v
	}

	var results []MemoryRecallResultItem

	switch strategy {
	case "semantic":
		results = s.memoryRecallSemantic(r.Context(), req.Query, topK, req.Threshold, filterMeta, req.IncludeContent)
	case "keyword":
		results = s.memoryRecallKeyword(req.Query, topK, filterMeta, req.IncludeContent)
	default: // "hybrid"
		results = s.memoryRecallHybrid(r.Context(), req.Query, topK, req.Threshold, req.Alpha, filterMeta, req.IncludeContent)
	}

	// RAG-001: recall feeds a model's context directly, which is the case the
	// budget exists for. Capped against the messages collection's profile.
	results, contextTruncated := applyContextBudget(s, memoryMessagesCollection, results,
		func(it MemoryRecallResultItem) int { return approxTokens(it.Document.ContentMD) })

	resp := MemoryRecallResponse{
		Results:          results,
		Total:            len(results),
		Strategy:         strategy,
		Query:            req.Query,
		ContextTruncated: contextTruncated,
	}
	ok(w, resp)
}

// getSessionIDsForUser returns all session IDs belonging to a user.
func (s *Server) getSessionIDsForUser(userID string) []string {
	var sessionIDs []string
	_ = s.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx == nil {
			return nil
		}
		prefix := storage.MetaKeyPrefix(memorySessionsCollection, "userId", userID)
		c := bIdx.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			docID := string(k[len(prefix):])
			// Extract session key from docID (format: memory_sessions|sessionid|en)
			parts := strings.Split(docID, "|")
			if len(parts) >= 2 {
				sessionIDs = append(sessionIDs, parts[1])
			}
		}
		return nil
	})
	return sessionIDs
}

// memoryRecallSemantic performs pure vector search for recall.
func (s *Server) memoryRecallSemantic(ctx context.Context, query string, topK int, threshold float64, filterMeta map[string][]string, includeContent bool) []MemoryRecallResultItem {
	if s.Embedding == nil {
		return nil
	}

	searcher, exists := s.VectorSearchers["flat"]
	if !exists || !searcher.IsReady() {
		return nil
	}

	embedCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	queryVec, err := s.Embedding.Embed(embedCtx, query)
	if err != nil {
		return nil
	}

	searchTopK := topK * 3
	if searchTopK < 20 {
		searchTopK = 20
	}

	metric := vector.ResolveSimilarity("cosine")
	allowedIDs := s.getDocIDsByMeta(memoryMessagesCollection, filterMeta)

	var vResults []vector.VectorResult
	if len(allowedIDs) > 0 {
		vResults = searcher.SearchWithFilter(memoryMessagesCollection, queryVec, searchTopK, threshold, allowedIDs, metric)
	} else if len(filterMeta) == 0 {
		vResults = searcher.Search(memoryMessagesCollection, queryVec, searchTopK, threshold, metric)
	}

	vResults = vector.DeduplicateChunkResults(vResults)
	if len(vResults) > topK {
		vResults = vResults[:topK]
	}

	return s.loadRecallResults(vResults, includeContent, "semantic")
}

// memoryRecallKeyword performs FTS-based recall.
func (s *Server) memoryRecallKeyword(query string, topK int, filterMeta map[string][]string, includeContent bool) []MemoryRecallResultItem {
	if s.FTSIndex == nil {
		return nil
	}

	ftsResults, err := s.FTSIndex.Search(memoryMessagesCollection, query, topK*2)
	if err != nil {
		return nil
	}

	// Apply metadata filter
	var allowedIDs map[string]bool
	if len(filterMeta) > 0 {
		allowedIDs = s.getDocIDsByMeta(memoryMessagesCollection, filterMeta)
	}

	var results []MemoryRecallResultItem
	rank := 1
	for _, fr := range ftsResults {
		if allowedIDs != nil && !allowedIDs[fr.DocID] {
			continue
		}
		if rank > topK {
			break
		}

		doc := s.loadDocByID(memoryMessagesCollection, fr.DocID, includeContent)
		if doc == nil {
			continue
		}

		results = append(results, MemoryRecallResultItem{
			Document:      *doc,
			Score:         fr.Score,
			Rank:          rank,
			SessionID:     metaFirst(doc.Meta, "sessionId"),
			Role:          metaFirst(doc.Meta, "role"),
			MatchStrategy: "keyword",
		})
		rank++
	}
	return results
}

// memoryRecallHybrid combines semantic and keyword search with RRF.
func (s *Server) memoryRecallHybrid(ctx context.Context, query string, topK int, threshold, _ float64, filterMeta map[string][]string, includeContent bool) []MemoryRecallResultItem {
	semanticResults := s.memoryRecallSemantic(ctx, query, topK*2, threshold, filterMeta, true)
	keywordResults := s.memoryRecallKeyword(query, topK*2, filterMeta, true)

	// RRF fusion
	type fusedEntry struct {
		docID    string
		rrfScore float64
		doc      storage.Doc
		session  string
		role     string
	}
	const rrfK = 60.0
	fused := make(map[string]*fusedEntry)

	for rank, r := range semanticResults {
		fused[r.Document.ID] = &fusedEntry{
			docID:    r.Document.ID,
			rrfScore: 1.0 / (rrfK + float64(rank+1)),
			doc:      r.Document,
			session:  r.SessionID,
			role:     r.Role,
		}
	}
	for rank, r := range keywordResults {
		e, exists := fused[r.Document.ID]
		if !exists {
			e = &fusedEntry{
				docID:   r.Document.ID,
				doc:     r.Document,
				session: r.SessionID,
				role:    r.Role,
			}
			fused[r.Document.ID] = e
		}
		e.rrfScore += 1.0 / (rrfK + float64(rank+1))
	}

	sorted := make([]MemoryRecallResultItem, 0, len(fused))
	for _, e := range fused {
		doc := e.doc
		if !includeContent {
			doc.ContentMD = ""
		}
		sorted = append(sorted, MemoryRecallResultItem{
			Document:      doc,
			Score:         e.rrfScore,
			SessionID:     e.session,
			Role:          e.role,
			MatchStrategy: "hybrid",
		})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})
	if len(sorted) > topK {
		sorted = sorted[:topK]
	}
	for i := range sorted {
		sorted[i].Rank = i + 1
	}
	return sorted
}

// loadRecallResults loads full documents from vector results.
func (s *Server) loadRecallResults(vResults []vector.VectorResult, includeContent bool, strategy string) []MemoryRecallResultItem {
	var results []MemoryRecallResultItem
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		for rank, vr := range vResults {
			v := bDocs.Get(storage.DocKey(memoryMessagesCollection, vr.DocID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil {
				continue
			}
			doc := *docPtr
			if !includeContent {
				doc.ContentMD = ""
			}
			results = append(results, MemoryRecallResultItem{
				Document:      doc,
				Score:         float64(vr.Score),
				Rank:          rank + 1,
				SessionID:     metaFirst(doc.Meta, "sessionId"),
				Role:          metaFirst(doc.Meta, "role"),
				MatchStrategy: strategy,
			})
		}
		return nil
	})
	return results
}

// loadDocByID loads a single document by collection and docID.
func (s *Server) loadDocByID(collection, docID string, includeContent bool) *storage.Doc {
	var doc *storage.Doc
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		v := bDocs.Get(storage.DocKey(collection, docID))
		if v == nil {
			return nil
		}
		d, err := loadDoc(v)
		if err != nil {
			return nil
		}
		if !includeContent {
			d.ContentMD = ""
		}
		doc = d
		return nil
	})
	return doc
}

// metaFirst returns the first value of a metadata key, or empty string.
func metaFirst(meta map[string][]string, key string) string {
	if vals, exists := meta[key]; exists && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// handleMemorySummarize generates a summary of a session's messages.
func (s *Server) handleMemorySummarize(w http.ResponseWriter, r *http.Request) {
	var req MemorySummarizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.SessionID == "" {
		bad(w, errors.New("missing sessionId"))
		return
	}

	// Load all messages for this session
	messages := s.loadSessionMessages(req.SessionID, 0, 0)
	if len(messages) == 0 {
		bad(w, errors.New("no messages found for session"))
		return
	}

	// Build summary from messages
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Session Summary: %s\n\n", req.SessionID[:8])
	fmt.Fprintf(&sb, "Messages: %d\n\n", len(messages))
	sb.WriteString("## Conversation\n\n")

	for _, msg := range messages {
		role := metaFirst(msg.Meta, "role")
		content := msg.ContentMD
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		fmt.Fprintf(&sb, "**%s**: %s\n\n", role, content)
	}

	summary := sb.String()
	now := time.Now().Unix()

	// Store summary
	summaryKey := fmt.Sprintf("%s/%020d", req.SessionID, now)
	meta := map[string][]string{
		"sessionId": {req.SessionID},
		"type":      {"summary"},
		"timestamp": {fmt.Sprintf("%d", now)},
		"messages":  {fmt.Sprintf("%d", len(messages))},
	}
	if req.UserID != "" {
		meta["userId"] = []string{req.UserID}
	}

	saved, _, err := s.addDocument(memorySummariesCollection, summaryKey, "en", meta, summary, 0, true)
	if err != nil {
		bad(w, err)
		return
	}

	resp := MemorySummarizeResponse{
		SummaryID: saved.ID,
		SessionID: req.SessionID,
		Summary:   summary,
		CreatedAt: saved.AddedAt,
		Messages:  len(messages),
	}
	ok(w, resp)
}

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

	// Build filter
	filterMeta := map[string][]string{
		"type": {"session"},
	}
	if req.UserID != "" {
		filterMeta["userId"] = []string{req.UserID}
	}
	if req.Scenario != "" {
		filterMeta["scenario"] = []string{req.Scenario}
	}

	var sessions []MemorySessionDetail
	err := s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bDocs == nil || bIdx == nil {
			return nil
		}

		var docIDs []string
		if len(filterMeta) > 1 { // more than just "type"
			var sets [][]string
			for mk, mvals := range filterMeta {
				var ids []string
				for _, mv := range mvals {
					prefix := storage.MetaKeyPrefix(memorySessionsCollection, mk, mv)
					c := bIdx.Cursor()
					for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
						ids = append(ids, string(k[len(prefix):]))
					}
				}
				ids = sliceutil.Unique(ids)
				sets = append(sets, ids)
			}
			docIDs = intersect(sets...)
		} else {
			// Scan all sessions
			prefix := []byte("doc|" + memorySessionsCollection + "|")
			c := bDocs.Cursor()
			for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
				docID := string(k[len(prefix):])
				docIDs = append(docIDs, docID)
			}
		}

		for _, docID := range docIDs {
			v := bDocs.Get(storage.DocKey(memorySessionsCollection, docID))
			if v == nil {
				continue
			}
			d, err := loadDoc(v)
			if err != nil {
				continue
			}
			if d.ExpiresAt > 0 && d.ExpiresAt < time.Now().Unix() {
				continue
			}

			msgCount := s.countSessionMessages(d.Key)

			sessions = append(sessions, MemorySessionDetail{
				SessionID:    d.Key,
				UserID:       metaFirst(d.Meta, "userId"),
				Scenario:     metaFirst(d.Meta, "scenario"),
				Title:        metaFirst(d.Meta, "title"),
				CreatedAt:    d.AddedAt,
				UpdatedAt:    d.UpdatedAt,
				ExpiresAt:    d.ExpiresAt,
				MessageCount: msgCount,
			})
		}
		return nil
	})
	if err != nil {
		bad(w, err)
		return
	}

	// Sort
	switch req.Sort {
	case "updatedAt":
		sort.Slice(sessions, func(i, j int) bool {
			if req.Asc {
				return sessions[i].UpdatedAt < sessions[j].UpdatedAt
			}
			return sessions[i].UpdatedAt > sessions[j].UpdatedAt
		})
	default: // createdAt
		sort.Slice(sessions, func(i, j int) bool {
			if req.Asc {
				return sessions[i].CreatedAt < sessions[j].CreatedAt
			}
			return sessions[i].CreatedAt > sessions[j].CreatedAt
		})
	}

	total := len(sessions)

	// Apply offset/limit
	if req.Offset > 0 && req.Offset < len(sessions) {
		sessions = sessions[req.Offset:]
	} else if req.Offset >= len(sessions) {
		sessions = nil
	}
	if req.Limit > 0 && len(sessions) > req.Limit {
		sessions = sessions[:req.Limit]
	}

	if sessions == nil {
		sessions = []MemorySessionDetail{}
	}

	resp := MemorySessionsListResponse{
		Sessions: sessions,
		Total:    total,
	}
	ok(w, resp)
}

// handleMemoryHistory returns the message history for a session.
func (s *Server) handleMemoryHistory(w http.ResponseWriter, r *http.Request) {
	var req MemoryHistoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.SessionID == "" {
		bad(w, errors.New("missing sessionId"))
		return
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}

	messages := s.loadSessionMessages(req.SessionID, req.Limit, req.Offset)

	resp := MemoryHistoryResponse{
		Messages: messages,
		Total:    len(messages),
	}
	ok(w, resp)
}

// loadSessionMessages loads all messages for a session, ordered chronologically.
func (s *Server) loadSessionMessages(sessionID string, limit, offset int) []storage.Doc {
	var messages []storage.Doc
	_ = s.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket([]byte("docs"))
		if bDocs == nil {
			return nil
		}
		// Messages are keyed as: sessionID/timestamp-msgID
		prefix := []byte("doc|" + memoryMessagesCollection + "|" + strings.ToLower(memoryMessagesCollection+"|"+sessionID+"/"))
		c := bDocs.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			d, err := loadDoc(v)
			if err != nil {
				continue
			}
			if d.ExpiresAt > 0 && d.ExpiresAt < time.Now().Unix() {
				continue
			}
			messages = append(messages, *d)
		}
		return nil
	})

	// Sort by addedAt
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].AddedAt < messages[j].AddedAt
	})

	// Apply offset/limit
	if offset > 0 && offset < len(messages) {
		messages = messages[offset:]
	} else if offset >= len(messages) {
		return nil
	}
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
	}

	return messages
}

// countSessionMessages counts messages belonging to a session.
func (s *Server) countSessionMessages(sessionID string) int {
	count := 0
	_ = s.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket([]byte("idxmeta"))
		if bIdx == nil {
			return nil
		}
		prefix := storage.MetaKeyPrefix(memoryMessagesCollection, "sessionId", sessionID)
		c := bIdx.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			count++
		}
		return nil
	})
	return count
}
