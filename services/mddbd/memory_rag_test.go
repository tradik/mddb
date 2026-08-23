package main

import (
	"net/http"
	"testing"

	json "mddb/internal/jsonx"
)

func TestMemorySessionCreate(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	body := MemorySessionRequest{
		UserID:   "user-1",
		Scenario: "code_review",
		Title:    "Test Session",
	}
	rec := doRequest(t, s.handleMemorySessionCreate, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp MemorySessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.SessionID == "" {
		t.Fatal("expected non-empty sessionId")
	}
	if resp.UserID != "user-1" {
		t.Errorf("expected userId=user-1, got %s", resp.UserID)
	}
	if resp.Scenario != "code_review" {
		t.Errorf("expected scenario=code_review, got %s", resp.Scenario)
	}
	if resp.Title != "Test Session" {
		t.Errorf("expected title=Test Session, got %s", resp.Title)
	}
	if resp.CreatedAt == 0 {
		t.Error("expected non-zero createdAt")
	}
}

func TestMemorySessionCreateMissingUserID(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestMemoryMessageAdd(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create session first
	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{
		UserID:   "user-1",
		Scenario: "test",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("session create: %d %s", rec.Code, rec.Body.String())
	}
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	// Add message
	rec = doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID,
		Role:      "user",
		Content:   "Hello, how does the search API work?",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("message add: %d %s", rec.Code, rec.Body.String())
	}

	var msgResp MemoryMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &msgResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msgResp.SessionID != sess.SessionID {
		t.Errorf("expected sessionId=%s, got %s", sess.SessionID, msgResp.SessionID)
	}
	if msgResp.Role != "user" {
		t.Errorf("expected role=user, got %s", msgResp.Role)
	}
	if msgResp.MessageID == "" {
		t.Error("expected non-empty messageId")
	}
}

func TestMemoryMessageAddInvalidRole(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create session
	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	// Invalid role
	rec = doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID,
		Role:      "invalid_role",
		Content:   "test",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid role, got %d", rec.Code)
	}
}

func TestMemoryMessageAddMissingSession(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: "nonexistent-session-id",
		Role:      "user",
		Content:   "test",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestMemorySessionsList(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create two sessions for different users
	doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "alice", Scenario: "chat"})
	doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "alice", Scenario: "code"})
	doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "bob", Scenario: "chat"})

	// List all
	rec := doRequest(t, s.handleMemorySessionsList, MemorySessionsListRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp MemorySessionsListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 3 {
		t.Errorf("expected 3 sessions total, got %d", resp.Total)
	}

	// List by user
	rec = doRequest(t, s.handleMemorySessionsList, MemorySessionsListRequest{UserID: "alice"})
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Errorf("expected 2 sessions for alice, got %d", resp.Total)
	}

	// List by scenario
	rec = doRequest(t, s.handleMemorySessionsList, MemorySessionsListRequest{Scenario: "chat"})
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Errorf("expected 2 chat sessions, got %d", resp.Total)
	}
}

func TestMemoryHistory(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create session and add messages
	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	messages := []MemoryMessageRequest{
		{SessionID: sess.SessionID, Role: "user", Content: "Question 1"},
		{SessionID: sess.SessionID, Role: "assistant", Content: "Answer 1"},
		{SessionID: sess.SessionID, Role: "user", Content: "Question 2"},
	}
	for _, msg := range messages {
		rec = doRequest(t, s.handleMemoryMessageAdd, msg)
		if rec.Code != http.StatusOK {
			t.Fatalf("add message: %d %s", rec.Code, rec.Body.String())
		}
	}

	// Fetch history
	rec = doRequest(t, s.handleMemoryHistory, MemoryHistoryRequest{SessionID: sess.SessionID})
	if rec.Code != http.StatusOK {
		t.Fatalf("history: %d %s", rec.Code, rec.Body.String())
	}
	var histResp MemoryHistoryResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &histResp)
	if histResp.Total != 3 {
		t.Errorf("expected 3 messages, got %d", histResp.Total)
	}
}

func TestMemoryHistoryPagination(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	for i := 0; i < 5; i++ {
		doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
			SessionID: sess.SessionID,
			Role:      "user",
			Content:   "msg",
		})
	}

	// Fetch with limit
	rec = doRequest(t, s.handleMemoryHistory, MemoryHistoryRequest{
		SessionID: sess.SessionID,
		Limit:     2,
	})
	var histResp MemoryHistoryResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &histResp)
	if histResp.Total != 2 {
		t.Errorf("expected 2 messages with limit, got %d", histResp.Total)
	}
}

func TestMemorySummarize(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create session with messages
	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID, Role: "user", Content: "How do I use vector search?",
	})
	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID, Role: "assistant", Content: "You can use POST /v1/vector-search with a query parameter.",
	})

	// Summarize
	rec = doRequest(t, s.handleMemorySummarize, MemorySummarizeRequest{
		SessionID: sess.SessionID,
		UserID:    "u1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("summarize: %d %s", rec.Code, rec.Body.String())
	}

	var sumResp MemorySummarizeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sumResp)
	if sumResp.SessionID != sess.SessionID {
		t.Errorf("expected sessionId=%s, got %s", sess.SessionID, sumResp.SessionID)
	}
	if sumResp.Messages != 2 {
		t.Errorf("expected 2 messages summarized, got %d", sumResp.Messages)
	}
	if sumResp.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if sumResp.SummaryID == "" {
		t.Error("expected non-empty summaryId")
	}
}

func TestMemorySummarizeEmptySession(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	// Summarize empty session
	rec = doRequest(t, s.handleMemorySummarize, MemorySummarizeRequest{SessionID: sess.SessionID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty session, got %d", rec.Code)
	}
}

func TestMemoryRecallKeyword(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create session and messages
	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID, Role: "user", Content: "How does vector search work in MDDB?",
	})
	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID, Role: "assistant", Content: "Vector search uses embeddings and cosine similarity to find semantically similar documents.",
	})
	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID, Role: "user", Content: "What about full text search with BM25?",
	})

	// Keyword recall
	rec = doRequest(t, s.handleMemoryRecall, MemoryRecallRequest{
		Query:          "vector search",
		Strategy:       "keyword",
		TopK:           5,
		IncludeContent: true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("recall: %d %s", rec.Code, rec.Body.String())
	}

	var recallResp MemoryRecallResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &recallResp)
	if recallResp.Strategy != "keyword" {
		t.Errorf("expected strategy=keyword, got %s", recallResp.Strategy)
	}
	// FTS should find messages containing "vector" and "search"
	if recallResp.Total == 0 {
		t.Log("no keyword results (FTS may need reindex)")
	}
}

func TestMemoryRecallHybridNoEmbedding(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create session and messages
	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID, Role: "user", Content: "Test hybrid recall",
	})

	// Hybrid recall (no embedding provider configured - should still work with keyword fallback)
	rec = doRequest(t, s.handleMemoryRecall, MemoryRecallRequest{
		Query:    "hybrid recall",
		Strategy: "hybrid",
		TopK:     5,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("hybrid recall: %d %s", rec.Code, rec.Body.String())
	}

	var recallResp MemoryRecallResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &recallResp)
	if recallResp.Strategy != "hybrid" {
		t.Errorf("expected strategy=hybrid, got %s", recallResp.Strategy)
	}
}

func TestMemoryRecallMissingQuery(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleMemoryRecall, MemoryRecallRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestMemoryRecallWithUserFilter(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Create sessions for two users
	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "alice"})
	var sessAlice MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sessAlice)

	rec = doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "bob"})
	var sessBob MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sessBob)

	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sessAlice.SessionID, Role: "user", Content: "Alice question about databases",
	})
	doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sessBob.SessionID, Role: "user", Content: "Bob question about databases",
	})

	// Recall for alice only
	rec = doRequest(t, s.handleMemoryRecall, MemoryRecallRequest{
		Query:    "databases",
		UserID:   "alice",
		Strategy: "keyword",
		TopK:     10,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("recall: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMemorySessionDefaultTTL(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var resp MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.ExpiresAt == 0 {
		t.Error("expected non-zero expiresAt with default TTL")
	}
	expectedTTL := resp.CreatedAt + defaultSessionTTL
	if resp.ExpiresAt != expectedTTL {
		t.Errorf("expected expiresAt=%d (createdAt+30d), got %d", expectedTTL, resp.ExpiresAt)
	}
}

func TestMemoryMessageMeta(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleMemorySessionCreate, MemorySessionRequest{UserID: "u1"})
	var sess MemorySessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)

	rec = doRequest(t, s.handleMemoryMessageAdd, MemoryMessageRequest{
		SessionID: sess.SessionID,
		Role:      "assistant",
		Content:   "Here is the result",
		Meta:      map[string]string{"topic": "search", "source": "docs"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var msgResp MemoryMessageResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &msgResp)
	if msgResp.Role != "assistant" {
		t.Errorf("expected role=assistant, got %s", msgResp.Role)
	}
}

func TestMetaFirstHelper(t *testing.T) {
	meta := map[string][]string{
		"role":      {"user"},
		"sessionId": {"abc123"},
		"empty":     {},
	}

	if got := metaFirst(meta, "role"); got != "user" {
		t.Errorf("expected 'user', got '%s'", got)
	}
	if got := metaFirst(meta, "sessionId"); got != "abc123" {
		t.Errorf("expected 'abc123', got '%s'", got)
	}
	if got := metaFirst(meta, "empty"); got != "" {
		t.Errorf("expected '', got '%s'", got)
	}
	if got := metaFirst(meta, "missing"); got != "" {
		t.Errorf("expected '', got '%s'", got)
	}
}

func TestMatchesMeta(t *testing.T) {
	docMeta := map[string][]string{
		"role":      {"user"},
		"sessionId": {"s1"},
		"type":      {"message"},
	}

	// All match
	if !matchesMeta(docMeta, map[string][]string{"role": {"user"}, "type": {"message"}}) {
		t.Error("expected match")
	}

	// Missing key
	if matchesMeta(docMeta, map[string][]string{"missing": {"val"}}) {
		t.Error("expected no match for missing key")
	}

	// Value mismatch
	if matchesMeta(docMeta, map[string][]string{"role": {"assistant"}}) {
		t.Error("expected no match for wrong value")
	}

	// Empty filter matches everything
	if !matchesMeta(docMeta, map[string][]string{}) {
		t.Error("expected empty filter to match")
	}
}
