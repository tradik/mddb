package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"mddb/internal/pending"
	"mddb/internal/storage"
)

// Indexing status and read-your-writes (GO-042).
//
// A write returns once the document and its full-text and metadata entries
// are committed; its embedding is computed afterwards by the embedding
// worker, and a batch update indexes metadata on a queue. Until then a
// semantic search does not see the document. Before this, an agent that
// added a document and searched for it at once found nothing and had no way
// to learn why or to wait. Now it can ask (indexing-status), wait
// (waitForIndex / waitForKeys on a search) or include what is still queued
// (readLevel "indexedPlusPending").

// Read levels for vector and hybrid search.
const (
	ReadLevelIndexed            = "indexed"
	ReadLevelIndexedPlusPending = "indexedPlusPending"
)

// maxWaitForIndex caps waitForIndex: a search is not a place to block for
// minutes, and a queue that has not drained in this long will not drain
// because the caller waited longer.
const maxWaitForIndex = 30 * time.Second

// IndexingStatus is what GET /v1/indexing-status and get_indexing_status return.
type IndexingStatus struct {
	// Collection is empty for the server-wide aggregate.
	Collection string `json:"collection,omitempty"`
	// PendingEmbeddings is how many embedding jobs are queued or running.
	PendingEmbeddings int `json:"pendingEmbeddings"`
	// FailedEmbeddings counts jobs since start that left a document without
	// its vectors — a failed provider call or a job the full queue dropped.
	// Such a document stays unsearchable by meaning until reindexed.
	FailedEmbeddings uint64 `json:"failedEmbeddings"`
	// CompletedEmbeddings counts jobs since start that finished.
	CompletedEmbeddings uint64 `json:"completedEmbeddings"`
	// PendingMetadata is how many queued metadata-index updates (batch
	// updates) have not been written yet. Single writes index metadata and
	// full text before they return, so they never appear here.
	PendingMetadata int `json:"pendingMetadata"`
	// Progress is completed / (completed + pending) over jobs since start;
	// 1 when nothing is pending.
	Progress float64 `json:"progress"`
	// OldestPendingAt is when the longest-waiting job was queued.
	OldestPendingAt *time.Time `json:"oldestPendingAt,omitempty"`
	// Provider is the embedding model in use; empty when none is configured,
	// in which case nothing is ever embedded.
	Provider string `json:"provider,omitempty"`
	// Indexed is true when nothing is pending: a search now sees every
	// document written so far.
	Indexed bool `json:"indexed"`
}

// IndexWait is the read-your-writes part of a search request.
type IndexWait struct {
	// ReadLevel "indexedPlusPending" also returns documents whose embedding
	// is still queued (vector: matched by full text; hybrid: already
	// included), each marked pendingEmbedding. Default "indexed".
	ReadLevel string `json:"readLevel,omitempty"`
	// WaitForIndex waits up to this many milliseconds (max 30000) for the
	// collection's embeddings — or only WaitForKeys' — to finish before
	// searching. Running out of time is not an error: the search runs and
	// the response says timedOut.
	WaitForIndex int `json:"waitForIndex,omitempty"`
	// WaitForKeys narrows the wait to these document keys.
	WaitForKeys []string `json:"waitForKeys,omitempty"`
}

// ValidateIndexWait rejects a read level or wait that cannot be honoured.
func (w IndexWait) ValidateIndexWait() error {
	switch w.ReadLevel {
	case "", ReadLevelIndexed, ReadLevelIndexedPlusPending:
	default:
		return errors.New("readLevel must be indexed or indexedPlusPending")
	}
	if w.WaitForIndex < 0 {
		return errors.New("waitForIndex must be a number of milliseconds, 0 or more")
	}
	return nil
}

// IndexWaitResult is reported on a search that asked to wait.
type IndexWaitResult struct {
	// WaitedMs is how long the search waited.
	WaitedMs int64 `json:"waitedMs"`
	// TimedOut is true when the wait ran out before the work drained; the
	// results may then miss the newest documents.
	TimedOut bool `json:"timedOut"`
}

func (s *Server) embeddingTracker() *pending.Tracker {
	if s.EmbeddingWorker == nil {
		return nil
	}
	return s.EmbeddingWorker.Pending()
}

func (s *Server) metadataTracker() *pending.Tracker {
	if s.IndexQueue == nil {
		return nil
	}
	return s.IndexQueue.Pending()
}

// IndexingStatus reports one collection, or every collection for "".
func (s *Server) IndexingStatus(collection string) IndexingStatus {
	emb := s.embeddingTracker().Stats(collection)
	meta := s.metadataTracker().Stats(collection)
	st := IndexingStatus{
		Collection:          collection,
		PendingEmbeddings:   emb.Pending,
		FailedEmbeddings:    emb.Failed,
		CompletedEmbeddings: emb.Completed,
		PendingMetadata:     meta.Pending,
		Progress:            1,
		Indexed:             emb.Pending == 0 && meta.Pending == 0,
	}
	if emb.Pending > 0 {
		done := float64(emb.Completed)
		st.Progress = done / (done + float64(emb.Pending))
	}
	oldest := emb.OldestPendingAt
	if !meta.OldestPendingAt.IsZero() && (oldest.IsZero() || meta.OldestPendingAt.Before(oldest)) {
		oldest = meta.OldestPendingAt
	}
	if !oldest.IsZero() {
		st.OldestPendingAt = &oldest
	}
	if s.Embedding != nil {
		st.Provider = s.Embedding.Model()
	}
	return st
}

// handleIndexingStatus serves GET /v1/indexing-status[?collection=c].
func (s *Server) handleIndexingStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	collection := r.URL.Query().Get("collection")
	scope := collection
	if scope == "" {
		scope = "*"
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), scope, PermRead); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	ok(w, s.IndexingStatus(collection))
}

// waitForIndex blocks for up to w.WaitForIndex ms until the collection's
// embedding work — or that of w.WaitForKeys — is done. It returns nil when
// the request did not ask to wait.
func (s *Server) waitForIndex(ctx context.Context, collection string, w IndexWait) *IndexWaitResult {
	return s.waitForIndexAll(ctx, []string{collection}, w)
}

// waitForIndexAll waits on several collections within one time budget, for
// cross-collection search.
func (s *Server) waitForIndexAll(ctx context.Context, collections []string, w IndexWait) *IndexWaitResult {
	if w.WaitForIndex <= 0 {
		return nil
	}
	timeout := min(time.Duration(w.WaitForIndex)*time.Millisecond, maxWaitForIndex)
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	drained := true
	for _, coll := range collections {
		if !s.embeddingTracker().Wait(wctx, coll, keyMatcher(coll, w.WaitForKeys)) {
			drained = false
			break
		}
	}
	return &IndexWaitResult{WaitedMs: time.Since(start).Milliseconds(), TimedOut: !drained}
}

// keyMatcher matches the document IDs of the given keys in any language
// (IDs are "<collection>|<key>|<lang>"); nil keys match every document.
func keyMatcher(collection string, keys []string) func(string) bool {
	if len(keys) == 0 {
		return nil
	}
	prefixes := make([]string, len(keys))
	for i, k := range keys {
		prefixes[i] = collection + "|" + normalizeDocumentKey(k) + "|"
	}
	return func(docID string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(docID, p) {
				return true
			}
		}
		return false
	}
}

// pendingEmbedding reports whether a document's embedding is still queued.
func (s *Server) pendingEmbedding(collection, docID string) bool {
	return s.embeddingTracker().IsPending(collection, docID)
}

// pendingMatches finds documents whose embedding is still queued and whose
// text matches query — what readLevel "indexedPlusPending" adds to a vector
// search, which cannot see them otherwise. seen excludes documents the
// search already returned. Best full-text match first.
func (s *Server) pendingMatches(collection, query string, filterMeta map[string][]string, filter MetaFilter, limit int, seen map[string]bool) []string {
	if query == "" || limit <= 0 || s.FTSIndex == nil {
		return nil
	}
	only := map[string]bool{}
	for _, id := range s.embeddingTracker().PendingDocs(collection) {
		if !seen[id] {
			only[id] = true
		}
	}
	if len(only) == 0 {
		return nil
	}
	// A search that cannot run finds nothing pending, which is the answer.
	results, _ := s.runFTSSearch(HybridSearchRequest{
		Collection: collection, Query: query, TopK: limit,
		FilterMeta: filterMeta, Filter: filter, onlyIDs: only,
	})
	ids := make([]string, 0, min(len(results), limit))
	for _, r := range results {
		if len(ids) == limit {
			break
		}
		ids = append(ids, r.DocID)
	}
	return ids
}

// pendingDocsFor loads the documents pendingMatches finds, skipping those
// already in items. Shared by the REST and MCP vector paths, which differ
// only in the item type they build.
func pendingDocsFor[T any](s *Server, collection, query string, filterMeta map[string][]string, filter MetaFilter, limit int, items []T, id func(T) string) []storage.Doc {
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[id(it)] = true
	}
	ids := s.pendingMatches(collection, query, filterMeta, filter, limit, seen)
	docs := make([]storage.Doc, 0, len(ids))
	for _, docID := range ids {
		if doc, err := loadDoc(s.docBytes(collection, docID)); err == nil && doc != nil {
			docs = append(docs, *doc)
		}
	}
	return docs
}

// SSEEventCollectionIndexed is broadcast when a collection's queued
// embeddings have all finished (GO-042).
const SSEEventCollectionIndexed = "collection.indexed"

// announceIndexed broadcasts collection.indexed to the SSE listeners.
func (s *Server) announceIndexed(collection string) {
	if s.SSEHub != nil {
		s.SSEHub.BroadcastWithAuth(SSEEventCollectionIndexed, collection, "", "", s.AuthManager)
	}
}

// WriteIndexing tells a writer what indexing still owes it (GO-042).
type WriteIndexing struct {
	// EmbeddingQueued is true when the write queued an embedding: the
	// document is searchable by its text now and by its meaning once the
	// job finishes (waitForKeys, indexing-status, or collection.indexed).
	EmbeddingQueued bool `json:"embeddingQueued"`
}

// writeIndexing reports the indexing a saved document is waiting for, or nil
// when it waits for nothing.
func (s *Server) writeIndexing(saved storage.Doc) *WriteIndexing {
	if s.EmbeddingWorker == nil || saved.ContentMD == "" {
		return nil
	}
	return &WriteIndexing{EmbeddingQueued: true}
}

// AddResponse is the document POST /v1/add saved, plus what indexing it is
// still waiting for. The document's fields stay at the top level, so the
// response is the one clients already parse with one field more.
type AddResponse struct {
	storage.Doc
	Indexing *WriteIndexing `json:"indexing,omitempty"`
}
