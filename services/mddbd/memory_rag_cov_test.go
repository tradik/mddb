package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/storage"
	"mddb/internal/vector"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// covKSession creates a memory session through the handler.
func covKSession(t *testing.T, s *Server, req MemorySessionRequest) MemorySessionResponse {
	t.Helper()
	rec := doRequest(t, s.handleMemorySessionCreate, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session create: %d %s", rec.Code, rec.Body.String())
	}
	var resp MemorySessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// covKMessage adds a message to a session and returns the stored document id.
//
// The session touch the handler starts in a goroutine is waited for here, so
// it cannot land in a database a later step of the test closes or rewrites.
func covKMessage(t *testing.T, s *Server, sessionID, role, content string) string {
	t.Helper()
	rec := doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{SessionID: sessionID, Role: role, Content: content})
	if rec.Code != http.StatusOK {
		t.Fatalf("message add: %d %s", rec.Code, rec.Body.String())
	}
	var resp MemoryMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	covKSettle(s)
	return resp.MessageID
}

// covKSettle waits for the asynchronous session touch: it takes the write
// lock, so one more write transaction is ordered after it.
func covKSettle(s *Server) {
	time.Sleep(20 * time.Millisecond)
	_ = s.DBUpdateC(memorySessionsCollection, func(*bolt.Tx) error { return nil })
}

// covKRecall runs a recall and decodes the response.
func covKRecall(t *testing.T, s *Server, req MemoryRecallRequest) MemoryRecallResponse {
	t.Helper()
	rec := doRequest(t, s.handleMemoryRecall, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall: %d %s", rec.Code, rec.Body.String())
	}
	var resp MemoryRecallResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// covKPutDoc stores an encoded document under its own id.
func covKPutDoc(t *testing.T, s *Server, collection string, doc *storage.Doc) {
	t.Helper()
	data, err := marshalDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	covKPutRaw(t, s, collection, "docs", storage.DocKey(collection, doc.ID), data)
}

// covKDropBucket deletes a bucket of the file holding collection.
func covKDropBucket(t *testing.T, s *Server, collection, bucket string) {
	t.Helper()
	if err := s.DBUpdateC(collection, func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte(bucket)) }); err != nil {
		t.Fatal(err)
	}
}

func TestCovKMemory_BadJSONIsRefused(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	for name, h := range map[string]http.HandlerFunc{
		"session":   s.handleMemorySessionCreate,
		"message":   s.handleMemoryMessageAdd,
		"recall":    s.handleMemoryRecall,
		"summarize": s.handleMemorySummarize,
		"list":      s.handleMemorySessionsList,
		"history":   s.handleMemoryHistory,
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{")))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: bad JSON answered %d, want 400", name, rec.Code)
		}
	}

	for name, h := range map[string]http.HandlerFunc{
		"summarize": s.handleMemorySummarize,
		"history":   s.handleMemoryHistory,
	} {
		rec := doRequest(t, h, map[string]string{})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "missing sessionId") {
			t.Errorf("%s without sessionId: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	rec := doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{SessionID: "x", Role: "user"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "missing sessionId, role, or content") {
		t.Errorf("message without content: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCovKMemorySession_DefaultTitleAndMeta(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	sess := covKSession(t, s, MemorySessionRequest{UserID: "u1", Meta: map[string]string{"topic": "db"}, TTL: 60})
	if sess.Title != "Session "+sess.SessionID[:8] {
		t.Errorf("default title = %q", sess.Title)
	}
	if sess.ExpiresAt == 0 || sess.ExpiresAt > time.Now().Unix()+61 {
		t.Errorf("expiresAt = %d, want about a minute from now", sess.ExpiresAt)
	}
	doc, err := s.loadDocByRef(memorySessionsCollection, sess.SessionID, "en")
	if err != nil {
		t.Fatal(err)
	}
	if metaFirst(doc.Meta, "topic") != "db" || metaFirst(doc.Meta, "userId") != "u1" {
		t.Errorf("session meta = %v", doc.Meta)
	}
}

func TestCovKMemory_SchemaRejectionsSurfaceAs400(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u1"})
	covKMessage(t, s, sess.SessionID, "user", "hello")

	for _, c := range []string{memorySessionsCollection, memoryMessagesCollection, memorySummariesCollection} {
		if err := s.SchemaManager.Set(c, `{"required":["mandatory"]}`); err != nil {
			t.Fatal(err)
		}
	}

	if rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u2"}); rec.Code != http.StatusBadRequest {
		t.Errorf("session against schema: %d, want 400", rec.Code)
	}
	if rec := doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{SessionID: sess.SessionID, Role: "user", Content: "x"}); rec.Code != http.StatusBadRequest {
		t.Errorf("message against schema: %d, want 400", rec.Code)
	}
	if rec := doRequest(t, s.handleMemorySummarize, MemorySummarizeRequest{SessionID: sess.SessionID}); rec.Code != http.StatusBadRequest {
		t.Errorf("summary against schema: %d, want 400", rec.Code)
	}
}

func TestCovKMemoryMessage_NoKeyIndexMeansNoSession(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u1"})
	covKDropBucket(t, s, memorySessionsCollection, "bykey")

	rec := doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{SessionID: sess.SessionID, Role: "user", Content: "x"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "session not found") {
		t.Fatalf("got %d %s, want session not found", rec.Code, rec.Body.String())
	}
}

func TestCovKTouchSession(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u1"})
	docID := genID(memorySessionsCollection, sess.SessionID, "en")
	key := storage.DocKey(memorySessionsCollection, docID)

	// A touch moves updatedAt forward.
	doc, err := s.loadDocByRef(memorySessionsCollection, sess.SessionID, "en")
	if err != nil {
		t.Fatal(err)
	}
	doc.UpdatedAt = 1
	covKPutDoc(t, s, memorySessionsCollection, doc)
	s.touchSession(sess.SessionID)
	doc, err = s.loadDocByRef(memorySessionsCollection, sess.SessionID, "en")
	if err != nil {
		t.Fatal(err)
	}
	if doc.UpdatedAt <= 1 {
		t.Errorf("updatedAt = %d after touch", doc.UpdatedAt)
	}

	// Unknown session: nothing to do, nothing written.
	s.touchSession("no-such-session")

	// Corrupt stored session: left alone rather than overwritten.
	covKPutRaw(t, s, memorySessionsCollection, "docs", key, []byte{0xff, 0x03})
	s.touchSession(sess.SessionID)
	var raw []byte
	_ = s.DBViewC(memorySessionsCollection, func(tx *bolt.Tx) error {
		raw = append([]byte(nil), tx.Bucket([]byte("docs")).Get(key)...)
		return nil
	})
	if !bytes.Equal(raw, []byte{0xff, 0x03}) {
		t.Errorf("corrupt session rewritten to %x", raw)
	}

	// Indexed but the document itself is gone.
	covKDeleteRaw(t, s, memorySessionsCollection, "docs", key)
	s.touchSession(sess.SessionID)

	// No buckets at all.
	covKDropBucket(t, s, memorySessionsCollection, "docs")
	s.touchSession(sess.SessionID)
}

func TestCovKRecall_FiltersAndNoSessionsForUser(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	alice := covKSession(t, s, MemorySessionRequest{UserID: "alice"})
	bob := covKSession(t, s, MemorySessionRequest{UserID: "bob"})
	covKMessage(t, s, alice.SessionID, "user", "alpha databases question")
	covKMessage(t, s, alice.SessionID, "assistant", "alpha databases answer")
	covKMessage(t, s, bob.SessionID, "user", "alpha databases from bob")

	resp := covKRecall(t, s, MemoryRecallRequest{Query: "alpha", UserID: "nobody", Strategy: "keyword"})
	if resp.Total != 0 || resp.Results == nil {
		t.Errorf("user without sessions: %+v, want an empty list", resp)
	}

	resp = covKRecall(t, s, MemoryRecallRequest{Query: "alpha databases", SessionID: bob.SessionID, Strategy: "keyword", IncludeContent: true})
	if resp.Total != 1 || resp.Results[0].SessionID != bob.SessionID || resp.Results[0].Document.ContentMD == "" {
		t.Errorf("session filter: %+v", resp.Results)
	}

	resp = covKRecall(t, s, MemoryRecallRequest{Query: "alpha databases", UserID: "alice", Role: "assistant", Strategy: "keyword"})
	if resp.Total != 1 || resp.Results[0].Role != "assistant" || resp.Results[0].Document.ContentMD != "" {
		t.Errorf("role filter: %+v", resp.Results)
	}

	resp = covKRecall(t, s, MemoryRecallRequest{Query: "alpha databases", FilterMeta: map[string][]string{"role": {"user"}}, Strategy: "keyword"})
	if resp.Total != 2 {
		t.Errorf("filterMeta role=user: %d results, want 2", resp.Total)
	}

	resp = covKRecall(t, s, MemoryRecallRequest{Query: "alpha databases", Strategy: "keyword", TopK: 1})
	if resp.Total != 1 || resp.Results[0].Rank != 1 {
		t.Errorf("topK 1: %+v", resp.Results)
	}
}

func TestCovKGetSessionIDsForUser(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "carol"})

	// An index entry whose id has no separator is skipped.
	covKPutRaw(t, s, memorySessionsCollection, "idxmeta",
		append(storage.MetaKeyPrefix(memorySessionsCollection, "userId", "carol"), []byte("nopipe")...), []byte{})
	if got := s.getSessionIDsForUser("carol"); len(got) != 1 || got[0] != sess.SessionID {
		t.Errorf("ids = %v, want [%s]", got, sess.SessionID)
	}

	covKDropBucket(t, s, memorySessionsCollection, "idxmeta")
	if got := s.getSessionIDsForUser("carol"); got != nil {
		t.Errorf("no index bucket: %v", got)
	}
	if got := s.countSessionMessages(sess.SessionID); got != 0 {
		t.Errorf("message count without index = %d", got)
	}
}

func TestCovKRecall_KeywordSkipsVanishedDocs(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u"})
	gone := covKMessage(t, s, sess.SessionID, "user", "zebra stripes")
	covKMessage(t, s, sess.SessionID, "user", "zebra herd")

	covKDeleteRaw(t, s, memoryMessagesCollection, "docs", storage.DocKey(memoryMessagesCollection, gone))
	resp := covKRecall(t, s, MemoryRecallRequest{Query: "zebra", Strategy: "keyword"})
	if resp.Total != 1 || resp.Results[0].Document.ID == gone {
		t.Errorf("results = %+v, want only the surviving message", resp.Results)
	}
}

// covKSemanticServer has two messages with vectors in the flat index: one
// aligned with the query and one orthogonal to it.
func covKSemanticServer(t *testing.T) (*Server, string, string, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	s.Embedding = &covKEmbedding{vectors: map[string][]float32{"giraffe": {1, 0, 0}}}
	s.VectorIndex.SetReady()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u"})
	near := covKMessage(t, s, sess.SessionID, "user", "giraffe necks are long")
	far := covKMessage(t, s, sess.SessionID, "assistant", "unrelated reply")
	if err := s.VectorIndex.Add(memoryMessagesCollection, near, []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.VectorIndex.Add(memoryMessagesCollection, far, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	return s, near, far, cleanup
}

func TestCovKRecall_Semantic(t *testing.T) {
	s, near, _, cleanup := covKSemanticServer(t)
	defer cleanup()

	resp := covKRecall(t, s, MemoryRecallRequest{Query: "giraffe", Strategy: "semantic", TopK: 1})
	if resp.Total != 1 || resp.Results[0].Document.ID != near || resp.Results[0].MatchStrategy != "semantic" {
		t.Fatalf("semantic recall = %+v", resp.Results)
	}
	if resp.Results[0].Document.ContentMD != "" || resp.Results[0].Role != "user" {
		t.Errorf("result without includeContent = %+v", resp.Results[0])
	}

	resp = covKRecall(t, s, MemoryRecallRequest{Query: "giraffe", Strategy: "semantic", IncludeContent: true})
	if resp.Total != 2 || resp.Results[0].Document.ContentMD != "giraffe necks are long" {
		t.Errorf("semantic with content = %+v", resp.Results)
	}

	// No filter at all searches the whole collection.
	if got := s.memoryRecallSemantic(context.Background(), "giraffe", 5, 0.5, nil, false); len(got) != 1 || got[0].Document.ID != near {
		t.Errorf("unfiltered semantic = %+v", got)
	}
}

func TestCovKRecall_SemanticUnavailable(t *testing.T) {
	s, _, _, cleanup := covKSemanticServer(t)
	defer cleanup()
	filter := map[string][]string{"type": {"message"}}

	s.Embedding = &covKEmbedding{failEmbed: true}
	if got := s.memoryRecallSemantic(context.Background(), "giraffe", 5, 0, filter, false); got != nil {
		t.Errorf("embed failure returned %+v", got)
	}

	s.Embedding = &covKEmbedding{}
	flat := s.VectorSearchers["flat"]
	delete(s.VectorSearchers, "flat")
	if got := s.memoryRecallSemantic(context.Background(), "giraffe", 5, 0, filter, false); got != nil {
		t.Errorf("no flat searcher returned %+v", got)
	}
	s.VectorSearchers["flat"] = flat

	s.Embedding = nil
	if got := s.memoryRecallSemantic(context.Background(), "giraffe", 5, 0, filter, false); got != nil {
		t.Errorf("no embedding returned %+v", got)
	}
}

func TestCovKRecall_HybridFusesBothSides(t *testing.T) {
	s, near, far, cleanup := covKSemanticServer(t)
	defer cleanup()

	// "giraffe" matches near semantically and by keyword; far only
	// semantically, so near carries two RRF terms and ranks first.
	resp := covKRecall(t, s, MemoryRecallRequest{Query: "giraffe", TopK: 1})
	if resp.Strategy != "hybrid" || resp.Total != 1 || resp.Results[0].Document.ID != near {
		t.Fatalf("hybrid = %+v", resp)
	}
	if resp.Results[0].MatchStrategy != "hybrid" || resp.Results[0].Rank != 1 || resp.Results[0].Document.ContentMD != "" {
		t.Errorf("hybrid item = %+v", resp.Results[0])
	}
	want := 1.0/61 + 1.0/61
	if d := resp.Results[0].Score - want; d > 1e-9 || d < -1e-9 {
		t.Errorf("fused score = %v, want %v", resp.Results[0].Score, want)
	}

	resp = covKRecall(t, s, MemoryRecallRequest{Query: "giraffe", TopK: 5, IncludeContent: true})
	if resp.Total != 2 || resp.Results[1].Document.ID != far || resp.Results[1].Document.ContentMD == "" {
		t.Errorf("hybrid with content = %+v", resp.Results)
	}

	// A message found by keyword alone, with no vector, still takes part.
	sessionID := metaFirst(s.loadDocByID(memoryMessagesCollection, near, false).Meta, "sessionId")
	wordOnly := covKMessage(t, s, sessionID, "user", "giraffe")
	resp = covKRecall(t, s, MemoryRecallRequest{Query: "giraffe", TopK: 5})
	found := false
	for _, r := range resp.Results {
		if r.Document.ID == wordOnly {
			found = true
			if r.SessionID != sessionID || r.Role != "user" {
				t.Errorf("keyword-only item = %+v", r)
			}
		}
	}
	if !found || resp.Total != 3 {
		t.Errorf("hybrid missed the keyword-only message: %+v", resp.Results)
	}
}

func TestCovKRecall_KeywordUnavailable(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u"})
	covKMessage(t, s, sess.SessionID, "user", "okapi")

	fts := s.FTSIndex
	s.FTSIndex = nil
	if got := s.memoryRecallKeyword("okapi", 5, nil, false); got != nil {
		t.Errorf("no FTS index: %+v", got)
	}
	s.FTSIndex = fts

	_ = s.collectionDB(memoryMessagesCollection).Close()
	if got := s.memoryRecallKeyword("okapi", 5, nil, false); got != nil {
		t.Errorf("FTS search on a closed database: %+v", got)
	}
}

func TestCovKRecall_ContextBudgetTruncates(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u"})
	covKMessage(t, s, sess.SessionID, "user", "budget words one two three four five six")
	covKMessage(t, s, sess.SessionID, "user", "budget words seven eight nine ten eleven")
	if err := s.CollectionManager.Set(memoryMessagesCollection, &CollectionConfig{Type: "default",
		Retrieval: &RetrievalProfileDef{ContextTokenBudget: 1}}); err != nil {
		t.Fatal(err)
	}

	resp := covKRecall(t, s, MemoryRecallRequest{Query: "budget", Strategy: "keyword", IncludeContent: true})
	if !resp.ContextTruncated || resp.Total >= 2 {
		t.Errorf("budget 1: truncated=%v total=%d", resp.ContextTruncated, resp.Total)
	}
}

func TestCovKLoadRecallResultsAndDocByID(t *testing.T) {
	s, near, far, cleanup := covKSemanticServer(t)
	defer cleanup()

	covKPutRaw(t, s, memoryMessagesCollection, "docs", storage.DocKey(memoryMessagesCollection, far), []byte{0xff, 0x04})
	vr := []vector.VectorResult{{DocID: "absent"}, {DocID: far}, {DocID: near, Score: 0.5}}
	got := s.loadRecallResults(vr, true, "semantic")
	if len(got) != 1 || got[0].Document.ID != near || got[0].Rank != 3 || got[0].Score != 0.5 {
		t.Errorf("loadRecallResults = %+v, want only the readable doc", got)
	}

	if d := s.loadDocByID(memoryMessagesCollection, far, true); d != nil {
		t.Errorf("corrupt doc loaded: %+v", d)
	}
	if d := s.loadDocByID(memoryMessagesCollection, "absent", true); d != nil {
		t.Errorf("absent doc loaded: %+v", d)
	}
	if d := s.loadDocByID(memoryMessagesCollection, near, false); d == nil || d.ContentMD != "" {
		t.Errorf("doc without content = %+v", d)
	}

	covKDropBucket(t, s, memoryMessagesCollection, "docs")
	if got := s.loadRecallResults(vr, true, "semantic"); got != nil {
		t.Errorf("no docs bucket: %+v", got)
	}
	if d := s.loadDocByID(memoryMessagesCollection, near, true); d != nil {
		t.Errorf("no docs bucket: %+v", d)
	}
	if got := s.loadSessionMessages("any", 0, 0); got != nil {
		t.Errorf("no docs bucket: messages %+v", got)
	}
}

func TestCovKSummarize_TruncatesLongMessagesAndRecordsUser(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u"})
	long := strings.Repeat("x", 250)
	covKMessage(t, s, sess.SessionID, "user", long)

	rec := doRequest(t, s.handleMemorySummarize, MemorySummarizeRequest{SessionID: sess.SessionID, UserID: "u"})
	if rec.Code != http.StatusOK {
		t.Fatalf("summarize: %d %s", rec.Code, rec.Body.String())
	}
	var resp MemorySummarizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Summary, strings.Repeat("x", 200)+"...") || strings.Contains(resp.Summary, strings.Repeat("x", 201)) {
		t.Errorf("long message not cut at 200: %q", resp.Summary)
	}
	if resp.Messages != 1 {
		t.Errorf("messages = %d", resp.Messages)
	}
	doc := s.loadDocByID(memorySummariesCollection, resp.SummaryID, false)
	if doc == nil || metaFirst(doc.Meta, "userId") != "u" {
		t.Errorf("summary meta = %+v", doc)
	}
}

// covKRawSession stores a session document directly, with chosen timestamps.
func covKRawSession(t *testing.T, s *Server, key string, added, updated, expires int64) {
	t.Helper()
	covKPutDoc(t, s, memorySessionsCollection, &storage.Doc{
		ID: genID(memorySessionsCollection, key, "en"), Key: key, Lang: "en",
		Meta:    map[string][]string{"type": {"session"}, "userId": {"raw"}, "title": {"T-" + key}},
		AddedAt: added, UpdatedAt: updated, ExpiresAt: expires,
	})
}

func covKListSessions(t *testing.T, s *Server, req MemorySessionsListRequest) MemorySessionsListResponse {
	t.Helper()
	rec := doRequest(t, s.handleMemorySessionsList, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var resp MemorySessionsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func covKSessionKeys(list []MemorySessionDetail) string {
	keys := make([]string, len(list))
	for i, d := range list {
		keys[i] = d.SessionID
	}
	return strings.Join(keys, ",")
}

func TestCovKSessionsList_SortPageAndSkip(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	covKRawSession(t, s, "a", 100, 300, 0)
	covKRawSession(t, s, "b", 200, 100, 0)
	covKRawSession(t, s, "c", 300, 200, 0)
	covKRawSession(t, s, "old", 50, 50, time.Now().Add(-time.Hour).Unix())
	covKPutRaw(t, s, memorySessionsCollection, "docs", storage.DocKey(memorySessionsCollection, "memory_sessions|bad|en"), []byte{0xff, 0x05})

	for _, c := range []struct {
		req  MemorySessionsListRequest
		want string
	}{
		{MemorySessionsListRequest{}, "c,b,a"},
		{MemorySessionsListRequest{Asc: true}, "a,b,c"},
		{MemorySessionsListRequest{Sort: "updatedAt"}, "a,c,b"},
		{MemorySessionsListRequest{Sort: "updatedAt", Asc: true}, "b,c,a"},
		{MemorySessionsListRequest{Offset: 1, Limit: 1}, "b"},
		{MemorySessionsListRequest{Offset: 3}, ""},
	} {
		resp := covKListSessions(t, s, c.req)
		if got := covKSessionKeys(resp.Sessions); got != c.want {
			t.Errorf("%+v: sessions %q, want %q", c.req, got, c.want)
		}
		if resp.Total != 3 {
			t.Errorf("%+v: total %d, want 3 (expired and corrupt skipped)", c.req, resp.Total)
		}
	}

	resp := covKListSessions(t, s, MemorySessionsListRequest{})
	if resp.Sessions[0].Title != "T-c" || resp.Sessions[0].UserID != "raw" {
		t.Errorf("detail = %+v", resp.Sessions[0])
	}
}

func TestCovKSessionsList_FilteredIndexAndFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "dave", Scenario: "triage"})
	covKSession(t, s, MemorySessionRequest{UserID: "dave", Scenario: "other"})
	covKMessage(t, s, sess.SessionID, "user", "one")
	covKMessage(t, s, sess.SessionID, "user", "two")

	// An index entry for a document that does not exist is skipped.
	for _, kv := range [][2]string{{"userId", "dave"}, {"type", "session"}} {
		covKPutRaw(t, s, memorySessionsCollection, "idxmeta",
			append(storage.MetaKeyPrefix(memorySessionsCollection, kv[0], kv[1]), []byte("memory_sessions|ghost|en")...), []byte{})
	}

	resp := covKListSessions(t, s, MemorySessionsListRequest{UserID: "dave", Scenario: "triage"})
	if resp.Total != 1 || resp.Sessions[0].SessionID != sess.SessionID || resp.Sessions[0].MessageCount != 2 {
		t.Fatalf("filtered list = %+v", resp)
	}
	if resp := covKListSessions(t, s, MemorySessionsListRequest{UserID: "dave"}); resp.Total != 2 {
		t.Errorf("user filter: %d sessions, want 2", resp.Total)
	}

	covKDropBucket(t, s, memorySessionsCollection, "idxmeta")
	if resp := covKListSessions(t, s, MemorySessionsListRequest{}); resp.Total != 0 || resp.Sessions == nil {
		t.Errorf("no index bucket: %+v, want an empty list", resp)
	}

	_ = s.collectionDB(memorySessionsCollection).Close()
	if rec := doRequest(t, s.handleMemorySessionsList, MemorySessionsListRequest{}); rec.Code != http.StatusBadRequest {
		t.Errorf("closed database: %d, want 400", rec.Code)
	}
}

func TestCovKHistory_SkipsUnreadableAndPages(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	sess := covKSession(t, s, MemorySessionRequest{UserID: "u"})
	covKMessage(t, s, sess.SessionID, "user", "kept")

	prefix := sess.SessionID + "/"
	covKPutRaw(t, s, memoryMessagesCollection, "docs",
		storage.DocKey(memoryMessagesCollection, genID(memoryMessagesCollection, prefix+"corrupt", "en")), []byte{0xff, 0x06})
	covKPutDoc(t, s, memoryMessagesCollection, &storage.Doc{
		ID: genID(memoryMessagesCollection, prefix+"expired", "en"), Key: prefix + "expired", Lang: "en",
		ContentMD: "expired", AddedAt: 1, ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	})

	rec := doRequest(t, s.handleMemoryHistory, MemoryHistoryRequest{SessionID: sess.SessionID})
	var resp MemoryHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || resp.Messages[0].ContentMD != "kept" {
		t.Fatalf("history = %+v, want only the readable live message", resp)
	}

	rec = doRequest(t, s.handleMemoryHistory, MemoryHistoryRequest{SessionID: sess.SessionID, Offset: 1})
	resp = MemoryHistoryResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 0 {
		t.Errorf("offset past the end: %+v", resp)
	}

	// The offset skips the earliest messages.
	covKPutDoc(t, s, memoryMessagesCollection, &storage.Doc{
		ID: genID(memoryMessagesCollection, prefix+"later", "en"), Key: prefix + "later", Lang: "en",
		ContentMD: "later", AddedAt: time.Now().Add(time.Hour).Unix(),
	})
	if got := s.loadSessionMessages(sess.SessionID, 0, 1); len(got) != 1 || got[0].ContentMD != "later" {
		t.Errorf("offset 1 = %+v, want only the later message", got)
	}
}
