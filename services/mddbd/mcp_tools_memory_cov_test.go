package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCovBMemoryToolsRequireArguments(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"memory_start_session", map[string]interface{}{}, "user_id is required"},
		{"memory_add_message", map[string]interface{}{"session_id": "s", "role": "user"}, "session_id, role, and content are required"},
		{"memory_add_message", map[string]interface{}{"session_id": "s", "role": "boss", "content": "c"}, "invalid role"},
		{"memory_recall", map[string]interface{}{}, "query is required"},
		{"memory_summarize", map[string]interface{}{}, "session_id is required"},
		{"memory_session_history", map[string]interface{}{}, "session_id is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			fake := covBNewFake()
			_, err := covBRun(t, fake, tc.name, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(fake.calls) != 0 {
				t.Errorf("invalid input reached the client: %v", fake.calls)
			}
		})
	}
}

// TestCovBMemoryToolsWrapClientErrors checks each memory tool wraps the
// client's error with its own context and keeps it inspectable.
func TestCovBMemoryToolsWrapClientErrors(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"memory_start_session", map[string]interface{}{"user_id": "u"}, "failed to create session"},
		{"memory_add_message", map[string]interface{}{"session_id": "s", "role": "user", "content": "c"}, "failed to add message"},
		{"memory_recall", map[string]interface{}{"query": "q", "strategy": "semantic"}, "semantic recall failed"},
		{"memory_recall", map[string]interface{}{"query": "q", "strategy": "keyword"}, "keyword recall failed"},
		{"memory_recall", map[string]interface{}{"query": "q"}, "hybrid recall failed"},
		{"memory_summarize", map[string]interface{}{"session_id": "session-1"}, "failed to fetch messages"},
		{"memory_list_sessions", map[string]interface{}{}, "failed to list sessions"},
		{"memory_session_history", map[string]interface{}{"session_id": "s"}, "failed to fetch history"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			fake := covBNewFake()
			fake.err = errCovBClient
			_, err := covBRun(t, fake, tc.name, tc.args)
			if !errors.Is(err, errCovBClient) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q wrapping errCovBClient", err, tc.want)
			}
		})
	}
}

func TestCovBMemoryStartSession(t *testing.T) {
	fake := covBNewFake()
	out, err := covBRun(t, fake, "memory_start_session", map[string]interface{}{
		"user_id": "alice", "scenario": "support",
		"meta": map[string]interface{}{"channel": "web", "status": "pinned"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Memory session created:\n") {
		t.Errorf("out = %q", out)
	}
	res := covBUnmarshal(t, out)
	sid, _ := res["sessionId"].(string)
	if len(sid) < 8 || res["userId"] != "alice" || res["scenario"] != "support" || res["title"] != "Session "+sid[:8] {
		t.Errorf("result = %v", res)
	}
	r := fake.covBCall(t, "Add").(*MCPAddRequest)
	if r.Collection != memorySessionsCollection || r.Key != sid || r.Lang != "en" {
		t.Errorf("request = %+v", r)
	}
	// Extra meta is merged and may override the defaults.
	if r.Meta["userId"][0] != "alice" || r.Meta["type"][0] != "session" || r.Meta["channel"][0] != "web" || r.Meta["status"][0] != "pinned" {
		t.Errorf("meta = %v", r.Meta)
	}
	if !strings.Contains(r.ContentMD, "User: alice") || !strings.Contains(r.ContentMD, "Scenario: support") {
		t.Errorf("content = %q", r.ContentMD)
	}

	// An explicit title is kept.
	out, err = covBRun(t, fake, "memory_start_session", map[string]interface{}{"user_id": "bob", "title": "Onboarding"})
	if err != nil {
		t.Fatal(err)
	}
	if covBUnmarshal(t, out)["title"] != "Onboarding" {
		t.Errorf("title lost: %s", out)
	}
}

func TestCovBMemoryAddMessage(t *testing.T) {
	for _, role := range []string{"user", "assistant", "system", "tool"} {
		t.Run(role, func(t *testing.T) {
			fake := covBNewFake()
			out, err := covBRun(t, fake, "memory_add_message", map[string]interface{}{
				"session_id": "sess", "role": role, "content": "hello",
				"meta": map[string]interface{}{"lang": "pl"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(out, "Message added to session:\n") {
				t.Errorf("out = %q", out)
			}
			r := fake.covBCall(t, "Add").(*MCPAddRequest)
			if r.Collection != memoryMessagesCollection || !strings.HasPrefix(r.Key, "sess/") || r.ContentMD != "hello" {
				t.Errorf("request = %+v", r)
			}
			if r.Meta["sessionId"][0] != "sess" || r.Meta["role"][0] != role || r.Meta["type"][0] != "message" || r.Meta["lang"][0] != "pl" {
				t.Errorf("meta = %v", r.Meta)
			}
			res := covBUnmarshal(t, out)
			if res["messageId"] != "doc-"+r.Key || res["role"] != role || res["sessionId"] != "sess" {
				t.Errorf("result = %v", res)
			}
		})
	}
}

func covBMsgDoc(key, session, role, content string) MCPDocument {
	return MCPDocument{
		ID: "id-" + key, Key: key, ContentMD: content,
		Meta: map[string][]string{"sessionId": {session}, "role": {role}, "type": {"message"}},
	}
}

func TestCovBMemoryRecallSemantic(t *testing.T) {
	fake := covBNewFake()
	fake.vecResp = &MCPVectorSearchResponse{Results: []MCPVectorSearchResult{
		{Score: 0.9, Document: covBMsgDoc("s1/1", "s1", "user", "first")},
		{Score: 0.5, Document: covBMsgDoc("s1/2", "s1", "assistant", "second")},
	}}
	out, err := covBRun(t, fake, "memory_recall", map[string]interface{}{
		"query": "q", "strategy": "semantic", "top_k": 2.0, "threshold": 0.3,
		"user_id": "u", "session_id": "s1", "role": "user", "include_content": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "VectorSearch").(*MCPVectorSearchRequest)
	if r.Collection != memoryMessagesCollection || r.TopK != 2 || r.Threshold != 0.3 || !r.IncludeContent ||
		r.FilterMeta["userId"][0] != "u" || r.FilterMeta["sessionId"][0] != "s1" || r.FilterMeta["role"][0] != "user" || r.FilterMeta["type"][0] != "message" {
		t.Errorf("request = %+v", r)
	}
	res := covBUnmarshal(t, out)
	results := res["results"].([]interface{})
	if res["strategy"] != "semantic" || res["total"].(float64) != 2 || len(results) != 2 {
		t.Fatalf("result = %v", res)
	}
	first := results[0].(map[string]interface{})
	if first["rank"].(float64) != 1 || first["content"] != "first" || first["sessionId"] != "s1" || first["role"] != "user" || first["key"] != "s1/1" {
		t.Errorf("first = %v", first)
	}

	// Without include_content the body is left out; top_k defaults.
	out, err = covBRun(t, fake, "memory_recall", map[string]interface{}{"query": "q", "strategy": "semantic"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"content"`) {
		t.Errorf("content returned without include_content: %s", out)
	}
	if r := fake.covBCall(t, "VectorSearch").(*MCPVectorSearchRequest); r.TopK != defaultRecallTopK {
		t.Errorf("default top_k = %d", r.TopK)
	}
}

// TestCovBMemoryRecallKeywordFiltersAndCaps checks the keyword strategy,
// which filters by meta itself: non-matching hits are dropped and the result
// stops at top_k.
func TestCovBMemoryRecallKeywordFiltersAndCaps(t *testing.T) {
	fake := covBNewFake()
	fake.ftsResp = &MCPFTSSearchResponse{Results: []MCPFTSResult{
		{Score: 3, Document: covBMsgDoc("other/1", "other", "user", "skip me")},
		{Score: 2, Document: covBMsgDoc("s1/1", "s1", "user", "keep one")},
		{Score: 1, Document: covBMsgDoc("s1/2", "s1", "user", "keep two")},
		{Score: 0.5, Document: covBMsgDoc("s1/3", "s1", "user", "over the cap")},
	}}
	out, err := covBRun(t, fake, "memory_recall", map[string]interface{}{
		"query": "keep", "strategy": "keyword", "session_id": "s1", "top_k": 2.0, "include_content": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "FTSSearch").(*MCPFTSSearchRequest)
	if r.Collection != memoryMessagesCollection || r.Limit != 2 || r.Algorithm != "bm25" {
		t.Errorf("request = %+v", r)
	}
	res := covBUnmarshal(t, out)
	if res["strategy"] != "keyword" || res["total"].(float64) != 2 {
		t.Fatalf("result = %v", res)
	}
	if strings.Contains(out, "skip me") || strings.Contains(out, "over the cap") || !strings.Contains(out, "keep two") {
		t.Errorf("filter/cap wrong: %s", out)
	}

	out, err = covBRun(t, fake, "memory_recall", map[string]interface{}{"query": "keep", "strategy": "keyword", "session_id": "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"content"`) {
		t.Errorf("content returned without include_content: %s", out)
	}
}

func TestCovBMemoryRecallHybrid(t *testing.T) {
	fake := covBNewFake()
	fake.hybridResp = &MCPHybridSearchResponse{Results: []MCPHybridSearchResult{
		{CombinedScore: 0.8, FTSScore: 0.6, VectorScore: 0.7, Document: covBMsgDoc("s1/1", "s1", "assistant", "answer")},
	}}
	out, err := covBRun(t, fake, "memory_recall", map[string]interface{}{"query": "q", "include_content": true, "threshold": 0.1})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "HybridSearch").(*MCPHybridSearchRequest)
	if r.Strategy != "rrf" || r.RRFK != 60 || r.Threshold != 0.1 || r.TopK != defaultRecallTopK {
		t.Errorf("request = %+v", r)
	}
	res := covBUnmarshal(t, out)
	item := res["results"].([]interface{})[0].(map[string]interface{})
	if res["strategy"] != "hybrid" || item["score"].(float64) != 0.8 || item["ftsScore"].(float64) != 0.6 ||
		item["vecScore"].(float64) != 0.7 || item["content"] != "answer" || item["role"] != "assistant" {
		t.Errorf("result = %v", res)
	}

	out, err = covBRun(t, fake, "memory_recall", map[string]interface{}{"query": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"content"`) {
		t.Errorf("content returned without include_content: %s", out)
	}
}

func TestCovBMemorySummarize(t *testing.T) {
	long := strings.Repeat("x", 250)
	fake := covBNewFake()
	fake.searchResp = &MCPSearchResponse{Total: 2, Documents: []MCPDocument{
		covBMsgDoc("session-123/1", "session-123", "user", "short question"),
		covBMsgDoc("session-123/2", "session-123", "assistant", long),
	}}
	out, err := covBRun(t, fake, "memory_summarize", map[string]interface{}{"session_id": "session-123", "user_id": "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Session summarized:\n") {
		t.Errorf("out = %q", out)
	}
	s := fake.covBCall(t, "Search").(*MCPSearchRequest)
	if s.Collection != memoryMessagesCollection || s.FilterMeta["sessionId"][0] != "session-123" || !s.Asc || s.Sort != "addedAt" {
		t.Errorf("search = %+v", s)
	}
	a := fake.covBCall(t, "Add").(*MCPAddRequest)
	if a.Collection != memorySummariesCollection || !strings.HasPrefix(a.Key, "session-123/") ||
		a.Meta["userId"][0] != "alice" || a.Meta["messages"][0] != "2" || a.Meta["type"][0] != "summary" {
		t.Errorf("add = %+v", a)
	}
	if !strings.Contains(a.ContentMD, "**user**: short question") || !strings.Contains(a.ContentMD, strings.Repeat("x", 200)+"...") ||
		strings.Contains(a.ContentMD, strings.Repeat("x", 201)) {
		t.Errorf("summary = %q", a.ContentMD)
	}
	res := covBUnmarshal(t, out)
	if res["sessionId"] != "session-123" || res["messages"].(float64) != 2 || res["summaryId"] != "doc-"+a.Key {
		t.Errorf("result = %v", res)
	}
}

// TestCovBMemorySummarizeShortSessionID is the regression test for a panic:
// the summary heading sliced session_id[:8], so any session id shorter than
// eight characters crashed the MCP call.
func TestCovBMemorySummarizeShortSessionID(t *testing.T) {
	fake := covBNewFake()
	fake.searchResp = &MCPSearchResponse{Total: 1, Documents: []MCPDocument{covBMsgDoc("abc/1", "abc", "user", "hi")}}
	out, err := covBRun(t, fake, "memory_summarize", map[string]interface{}{"session_id": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if a := fake.covBCall(t, "Add").(*MCPAddRequest); !strings.HasPrefix(a.ContentMD, "# Session Summary: abc\n") {
		t.Errorf("summary = %q", a.ContentMD)
	}
	if _, ok := fake.calls["Add"].(*MCPAddRequest).Meta["userId"]; ok {
		t.Error("userId set without user_id")
	}
	if covBUnmarshal(t, out)["sessionId"] != "abc" {
		t.Errorf("out = %s", out)
	}
}

func TestCovBMemorySummarizeEmptyAndStoreFailure(t *testing.T) {
	fake := covBNewFake()
	if _, err := covBRun(t, fake, "memory_summarize", map[string]interface{}{"session_id": "nothing-here"}); err == nil ||
		err.Error() != "no messages found for session nothing-here" {
		t.Errorf("empty session: err = %v", err)
	}

	fake = covBNewFake()
	fake.searchResp = &MCPSearchResponse{Total: 1, Documents: []MCPDocument{covBMsgDoc("s/1", "s", "user", "hi")}}
	fake.addErrAfter = 1
	if _, err := covBRun(t, fake, "memory_summarize", map[string]interface{}{"session_id": "session-x"}); !errors.Is(err, errCovBClient) ||
		!strings.Contains(err.Error(), "failed to store summary") {
		t.Errorf("store failure: err = %v", err)
	}
}

func TestCovBMemoryListSessions(t *testing.T) {
	fake := covBNewFake()
	fake.searchResp = &MCPSearchResponse{Total: 1, Documents: []MCPDocument{{
		Key: "sess-1", Meta: map[string][]string{"userId": {"alice"}, "scenario": {"support"}, "status": {"active"}},
	}}}
	out, err := covBRun(t, fake, "memory_list_sessions", map[string]interface{}{
		"user_id": "alice", "scenario": "support", "limit": 5.0, "offset": 10.0, "sort": "updatedAt",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "Search").(*MCPSearchRequest)
	if r.Collection != memorySessionsCollection || r.Limit != 5 || r.Offset != 10 || r.Sort != "updatedAt" ||
		r.FilterMeta["userId"][0] != "alice" || r.FilterMeta["scenario"][0] != "support" || r.FilterMeta["type"][0] != "session" {
		t.Errorf("request = %+v", r)
	}
	res := covBUnmarshal(t, out)
	sess := res["sessions"].([]interface{})[0].(map[string]interface{})
	if res["total"].(float64) != 1 || sess["sessionId"] != "sess-1" || sess["userId"] != "alice" || sess["status"] != "active" {
		t.Errorf("result = %v", res)
	}

	if _, err := covBRun(t, fake, "memory_list_sessions", nil); err != nil {
		t.Fatal(err)
	}
	r = fake.covBCall(t, "Search").(*MCPSearchRequest)
	if r.Limit != 50 || r.Sort != "addedAt" || len(r.FilterMeta) != 1 {
		t.Errorf("defaults = %+v", r)
	}
}

func TestCovBMemorySessionHistory(t *testing.T) {
	fake := covBNewFake()
	fake.searchResp = &MCPSearchResponse{Total: 2, Documents: []MCPDocument{
		covBMsgDoc("s/1", "s", "user", "q"), covBMsgDoc("s/2", "s", "assistant", "a"),
	}}
	out, err := covBRun(t, fake, "memory_session_history", map[string]interface{}{"session_id": "s", "limit": 3.0, "offset": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "Search").(*MCPSearchRequest)
	if r.Limit != 3 || r.Offset != 1 || !r.Asc || r.FilterMeta["sessionId"][0] != "s" {
		t.Errorf("request = %+v", r)
	}
	res := covBUnmarshal(t, out)
	msgs := res["messages"].([]interface{})
	if res["sessionId"] != "s" || res["total"].(float64) != 2 || len(msgs) != 2 ||
		msgs[1].(map[string]interface{})["content"] != "a" || msgs[1].(map[string]interface{})["role"] != "assistant" {
		t.Errorf("result = %v", res)
	}

	if _, err := covBRun(t, fake, "memory_session_history", map[string]interface{}{"session_id": "s"}); err != nil {
		t.Fatal(err)
	}
	if r := fake.covBCall(t, "Search").(*MCPSearchRequest); r.Limit != 100 {
		t.Errorf("default limit = %d", r.Limit)
	}
}

func TestCovBMatchesMeta(t *testing.T) {
	doc := map[string][]string{"role": {"user", "tool"}, "sessionId": {"s"}}
	cases := []struct {
		filter map[string][]string
		want   bool
	}{
		{nil, true},
		{map[string][]string{"role": {"tool"}}, true},
		{map[string][]string{"role": {"x", "user"}, "sessionId": {"s"}}, true},
		{map[string][]string{"role": {"assistant"}}, false},
		{map[string][]string{"userId": {"u"}}, false},
	}
	for i, tc := range cases {
		if got := matchesMeta(doc, tc.filter); got != tc.want {
			t.Errorf("case %d: matchesMeta = %v, want %v", i, got, tc.want)
		}
	}
}

// TestCovBMemoryToolsAgainstStore runs a whole conversation through the MCP
// memory tools against a real store: start, two messages, history in order,
// the session listed for its user, and a stored summary.
func TestCovBMemoryToolsAgainstStore(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	ctx := context.Background()

	out, err := ts.mcpCallTool(ctx, "memory_start_session", map[string]interface{}{"user_id": "covb-user", "scenario": "demo"})
	if err != nil {
		t.Fatal(err)
	}
	sid := covBUnmarshal(t, out)["sessionId"].(string)

	for _, m := range []struct{ role, content string }{{"user", "what is mddb"}, {"assistant", "a markdown database"}} {
		if _, err := ts.mcpCallTool(ctx, "memory_add_message", map[string]interface{}{"session_id": sid, "role": m.role, "content": m.content}); err != nil {
			t.Fatal(err)
		}
	}

	out, err = ts.mcpCallTool(ctx, "memory_session_history", map[string]interface{}{"session_id": sid})
	if err != nil {
		t.Fatal(err)
	}
	hist := covBUnmarshal(t, out)
	if hist["total"].(float64) != 2 || !strings.Contains(out, "what is mddb") || !strings.Contains(out, "a markdown database") {
		t.Errorf("history = %s", out)
	}

	out, err = ts.mcpCallTool(ctx, "memory_list_sessions", map[string]interface{}{"user_id": "covb-user"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, sid) {
		t.Errorf("session %s not listed: %s", sid, out)
	}

	out, err = ts.mcpCallTool(ctx, "memory_summarize", map[string]interface{}{"session_id": sid})
	if err != nil {
		t.Fatal(err)
	}
	if res := covBUnmarshal(t, out); res["messages"].(float64) != 2 || !strings.Contains(res["summary"].(string), "**assistant**: a markdown database") {
		t.Errorf("summary = %s", out)
	}
	sums, err := ts.mcpCallTool(ctx, "search_documents", map[string]interface{}{"collection": memorySummariesCollection})
	if err != nil {
		t.Fatal(err)
	}
	if covBUnmarshal(t, sums)["total"].(float64) != 1 {
		t.Errorf("summary not stored: %s", sums)
	}
}
