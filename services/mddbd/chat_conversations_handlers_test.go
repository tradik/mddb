package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// The chat conversation host API (#405) through the registered routes.

// chatCall sends one request through the routes as c (nil: no credentials)
// and decodes the answer.
func chatCall(t *testing.T, s *Server, c *JWTClaims, method, target, body string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux, false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	var h http.Handler = mux
	if c != nil {
		h = asUser(c, mux)
	}
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// chatNew creates a conversation and returns its id.
func chatNew(t *testing.T, s *Server, c *JWTClaims, sub, scenario string) string {
	t.Helper()
	code, out := chatCall(t, s, c, http.MethodPost, chatConversationsPath, fmt.Sprintf(`{"sub":%q,"scenario":%q}`, sub, scenario))
	id, _ := out["id"].(string)
	if code != http.StatusOK || id == "" {
		t.Fatalf("create for %s: %d %v", sub, code, out)
	}
	return id
}

// chatSay appends one user message with a given time.
func chatSay(t *testing.T, s *Server, id, sub, content string, at int64) {
	t.Helper()
	body := fmt.Sprintf(`{"sub":%q,"messages":[{"role":"user","content":%q,"createdAt":%d}]}`, sub, content, at)
	if code, out := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/messages", body); code != http.StatusOK {
		t.Fatalf("append: %d %v", code, out)
	}
}

func chatConvServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	return s
}

func TestChatConversationLifecycle(t *testing.T) {
	s := chatConvServer(t)
	code, conv := chatCall(t, s, nil, http.MethodPost, chatConversationsPath,
		`{"sub":"user-1","scenario":"support","displayName":"Ada","retentionDays":7}`)
	if code != http.StatusOK {
		t.Fatalf("create: %d %v", code, conv)
	}
	id := conv["id"].(string)
	created, expires := int64(conv["createdAt"].(float64)), int64(conv["expiresAt"].(float64))
	if conv["sub"] != "user-1" || conv["scenario"] != "support" || conv["displayName"] != "Ada" ||
		conv["ended"] != false || conv["retentionDays"] != float64(7) || expires != created+7*secondsPerDay {
		t.Errorf("created: %v", conv)
	}
	if msgs, isList := conv["messages"].([]any); !isList || len(msgs) != 0 {
		t.Errorf("a new conversation has an empty message list: %v", conv["messages"])
	}

	code, appended := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/messages", `{"sub":"user-1","messages":[
		{"role":"user","content":"How do I rotate the key?","createdAt":1000},
		{"role":"assistant","content":"Call /v1/encryption/rotate.","model":"m-1","provider":"p-1","createdAt":1001,
		 "tokens":{"prompt":120,"completion":15},"toolCalls":[{"name":"search","collection":"docs","durationMs":42}]}]}`)
	if code != http.StatusOK || appended["messageCount"] != float64(2) || appended["title"] != "How do I rotate the key?" ||
		appended["lastMessageAt"] != float64(1001) || len(appended["messages"].([]any)) != 2 {
		t.Fatalf("append: %d %v", code, appended)
	}

	code, got := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1", "")
	msgs := got["messages"].([]any)
	if code != http.StatusOK || len(msgs) != 2 {
		t.Fatalf("get: %d %v", code, got)
	}
	second := msgs[1].(map[string]any)
	call := second["toolCalls"].([]any)[0].(map[string]any)
	tokens := second["tokens"].(map[string]any)
	if second["id"] == "" || second["role"] != "assistant" || second["model"] != "m-1" || second["provider"] != "p-1" ||
		call["name"] != "search" || call["collection"] != "docs" || call["durationMs"] != float64(42) ||
		tokens["prompt"] != float64(120) || tokens["completion"] != float64(15) {
		t.Errorf("stored message: %v", second)
	}

	// ?limit= is the chat's max_history_length: the last N, the count stays whole.
	_, last := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1&limit=1", "")
	if l := last["messages"].([]any); len(l) != 1 || l[0].(map[string]any)["role"] != "assistant" || last["messageCount"] != float64(2) {
		t.Errorf("limit=1: %v", last)
	}

	code, ended := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/end", `{"sub":"user-1"}`)
	if code != http.StatusOK || ended["ended"] != true || len(ended["messages"].([]any)) != 0 || ended["messageCount"] != float64(2) {
		t.Fatalf("end: %d %v", code, ended)
	}
	if code, _ := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/end", `{"sub":"user-1"}`); code != http.StatusOK {
		t.Errorf("ending twice: %d", code)
	}
	body := `{"sub":"user-1","messages":[{"role":"user","content":"more"}]}`
	if code, _ := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/messages", body); code != http.StatusConflict {
		t.Errorf("append to an ended conversation: %d, want 409", code)
	}

	code, del := chatCall(t, s, nil, http.MethodDelete, chatConversationsPath+"/"+id+"?sub=user-1", "")
	if code != http.StatusOK || del["deleted"] != true {
		t.Fatalf("delete: %d %v", code, del)
	}
	if code, _ := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1", ""); code != http.StatusNotFound {
		t.Errorf("get after delete: %d", code)
	}
}

// A conversation of another sub is not found on every read and write: the
// answer is the one a missing id gets.
func TestChatConversationOfAnotherSubIsNotFound(t *testing.T) {
	s := chatConvServer(t)
	id := chatNew(t, s, nil, "user-1", "support")
	base := chatConversationsPath + "/" + id
	append2 := `{"sub":"user-2","messages":[{"role":"user","content":"x"}]}`
	cases := []struct{ method, target, body string }{
		{http.MethodGet, base + "?sub=user-2", ""},
		{http.MethodPost, base + "/messages", append2},
		{http.MethodPost, base + "/end", `{"sub":"user-2"}`},
		{http.MethodDelete, base + "?sub=user-2", ""},
		{http.MethodGet, chatConversationsPath + "/0000?sub=user-1", ""},      // no such id
		{http.MethodGet, chatConversationsPath + "/bad%20id!?sub=user-1", ""}, // not an id at all
	}
	for _, c := range cases {
		code, out := chatCall(t, s, nil, c.method, c.target, c.body)
		if code != http.StatusNotFound || out["error"] != "conversation not found" {
			t.Errorf("%s %s: %d %v, want 404", c.method, c.target, code, out)
		}
	}
	// Without a sub the request is malformed: there is nobody to answer for.
	for _, c := range []struct{ method, target, body string }{
		{http.MethodGet, base, ""},
		{http.MethodDelete, base, ""},
		{http.MethodPost, base + "/end", ""},
		{http.MethodPost, base + "/messages", `{"messages":[{"role":"user","content":"x"}]}`},
		{http.MethodGet, chatConversationsPath, ""},
		{http.MethodDelete, chatConversationsPath, ""},
		{http.MethodGet, chatConversationsPath + "/export", ""},
		{http.MethodPost, chatConversationsPath, `{"scenario":"support"}`},
		{http.MethodPost, chatConversationsPath, `{"sub":"user-1"}`},
		{http.MethodPost, chatConversationsPath, `{"sub":"user-1","scenario":"s","displayName":"a\u0000b"}`},
		{http.MethodPost, chatConversationsPath, `{"sub":"user-1","scenario":"s","retentionDays":-3}`},
	} {
		if code, _ := chatCall(t, s, nil, c.method, c.target, c.body); code != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d, want 400", c.method, c.target, c.body, code)
		}
	}
	// user-1 still has it, untouched.
	if code, got := chatCall(t, s, nil, http.MethodGet, base+"?sub=user-1", ""); code != http.StatusOK || got["ended"] != false || got["messageCount"] != float64(0) {
		t.Errorf("owner's conversation changed: %d %v", code, got)
	}
}

func TestChatConversationListNewestFirst(t *testing.T) {
	s := chatConvServer(t)
	a := chatNew(t, s, nil, "user-1", "support")
	b := chatNew(t, s, nil, "user-1", "sales")
	c := chatNew(t, s, nil, "user-1", "support")
	other := chatNew(t, s, nil, "user-1|x", "support") // the index is a prefix scan: this is another user
	chatNew(t, s, nil, "user-2", "support")
	future := chatNow() + 1000
	chatSay(t, s, a, "user-1", "oldest", future+1)
	chatSay(t, s, b, "user-1", "middle", future+2)
	chatSay(t, s, c, "user-1", "newest", future+3)
	chatSay(t, s, other, "user-1|x", "not mine", future+9)

	ids := func(out map[string]any) []string {
		var got []string
		for _, e := range out["conversations"].([]any) {
			got = append(got, e.(map[string]any)["id"].(string))
		}
		return got
	}
	code, all := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-1", "")
	if got := ids(all); code != http.StatusOK || fmt.Sprint(got) != fmt.Sprint([]string{c, b, a}) {
		t.Fatalf("list: %d %v, want %v", code, got, []string{c, b, a})
	}
	if first := all["conversations"].([]any)[0].(map[string]any); first["messages"] != nil || first["title"] != "newest" || first["messageCount"] != float64(1) {
		t.Errorf("a listing carries summaries: %v", first)
	}
	_, support := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-1&scenario=support", "")
	if got := ids(support); fmt.Sprint(got) != fmt.Sprint([]string{c, a}) {
		t.Errorf("scenario filter: %v", got)
	}
	_, page1 := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-1&limit=2", "")
	cursor, _ := page1["nextCursor"].(string)
	if got := ids(page1); len(got) != 2 || cursor == "" {
		t.Fatalf("page 1: %v cursor %q", got, cursor)
	}
	_, page2 := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-1&limit=2&cursor="+cursor, "")
	if got := ids(page2); fmt.Sprint(got) != fmt.Sprint([]string{a}) || page2["nextCursor"] != nil {
		t.Errorf("page 2: %v", page2)
	}
	if code, _ := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-1&cursor=!!", ""); code != http.StatusBadRequest {
		t.Errorf("bad cursor: %d", code)
	}
	_, none := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=nobody", "")
	if l, isList := none["conversations"].([]any); !isList || len(l) != 0 {
		t.Errorf("a user without conversations gets an empty list: %v", none)
	}
}

func TestChatExportAndDeleteForUser(t *testing.T) {
	s := chatConvServer(t)
	a := chatNew(t, s, nil, "user-1", "support")
	chatNew(t, s, nil, "user-1", "sales")
	keep := chatNew(t, s, nil, "user-2", "support")
	chatSay(t, s, a, "user-1", "export me", chatNow()+50)

	code, export := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/export?sub=user-1", "")
	convs := export["conversations"].([]any)
	if code != http.StatusOK || export["sub"] != "user-1" || export["total"] != float64(2) || len(convs) != 2 || export["exportedAt"] == nil {
		t.Fatalf("export: %d %v", code, export)
	}
	first := convs[0].(map[string]any)
	if first["id"] != a || first["messages"].([]any)[0].(map[string]any)["content"] != "export me" {
		t.Errorf("export carries the messages, newest conversation first: %v", first)
	}

	code, del := chatCall(t, s, nil, http.MethodDelete, chatConversationsPath+"?sub=user-1", "")
	if code != http.StatusOK || del["deleted"] != float64(2) || del["sub"] != "user-1" {
		t.Fatalf("delete for user: %d %v", code, del)
	}
	if _, after := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/export?sub=user-1", ""); after["total"] != float64(0) {
		t.Errorf("after erasure: %v", after)
	}
	if code, _ := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+keep+"?sub=user-2", ""); code != http.StatusOK {
		t.Errorf("another user's conversation was deleted: %d", code)
	}
	if _, again := chatCall(t, s, nil, http.MethodDelete, chatConversationsPath+"?sub=user-1", ""); again["deleted"] != float64(0) {
		t.Errorf("deleting nothing: %v", again)
	}
}

// Retention is the document's TTL: an expired conversation is gone for every
// read before the reaper removes it, and the reaper does remove it.
func TestChatRetentionExpires(t *testing.T) {
	s := chatConvServer(t)
	_, conv := chatCall(t, s, nil, http.MethodPost, chatConversationsPath, `{"sub":"user-1","scenario":"support","retentionDays":1}`)
	id := conv["id"].(string)
	real := chatNow
	t.Cleanup(func() { chatNow = real })
	chatNow = func() int64 { return real() + 2*secondsPerDay }

	if code, _ := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1", ""); code != http.StatusNotFound {
		t.Errorf("expired get: %d", code)
	}
	if _, list := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-1", ""); len(list["conversations"].([]any)) != 0 {
		t.Errorf("expired conversation is listed: %v", list)
	}
	// The TTL reaper removes it like any expired document.
	if err := (serverTTLReaper{s: s}).DeleteDocument(chatConversationsCollection, id, chatLang); err != nil {
		t.Errorf("reaping: %v", err)
	}
}

// A write refreshes the retention, and the transcript stays out of the
// full-text index.
func TestChatConversationIsNotIndexed(t *testing.T) {
	s := chatConvServer(t)
	id := chatNew(t, s, nil, "user-1", "support")
	chatSay(t, s, id, "user-1", "xylophone", 0)
	hits, err := s.FTSIndex.Search(chatConversationsCollection, "xylophone", 5)
	if err != nil || len(hits) != 0 {
		t.Errorf("a conversation was full-text indexed: %v %v", hits, err)
	}
	doc := s.loadDocByID(chatConversationsCollection, genID(chatConversationsCollection, id, chatLang), true)
	if doc == nil || metaFirst(doc.Meta, "sub") != "user-1" || metaFirst(doc.Meta, "scenario") != "support" ||
		metaFirst(doc.Meta, "ended") != "false" || doc.ExpiresAt != doc.UpdatedAt+chatDefaultRetentionDays*secondsPerDay {
		t.Errorf("stored document: %+v", doc)
	}
	// The write registered the conversation with the TTL reaper: retention
	// needs no reaper of its own.
	registered := false
	_ = s.collectionDB(chatConversationsCollection).View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("ttlrev")).ForEach(func(k, _ []byte) error {
			registered = registered || strings.Contains(string(k), doc.ID)
			return nil
		})
	})
	if !registered {
		t.Error("the conversation is not registered with the TTL reaper")
	}
}

func TestChatHostAPIRefusals(t *testing.T) {
	s := chatConvServer(t)
	id := chatNew(t, s, nil, "user-1", "support")
	for _, c := range []struct{ method, target string }{
		{http.MethodPut, chatConversationsPath},
		{http.MethodPost, chatConversationsPath + "/export"},
		{http.MethodPut, chatConversationsPath + "/" + id},
		{http.MethodGet, chatConversationsPath + "/" + id + "/messages"},
		{http.MethodPost, chatConversationsPath + "/" + id + "/rename"},
	} {
		if code, _ := chatCall(t, s, nil, c.method, c.target, `{"sub":"user-1"}`); code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d, want 405", c.method, c.target, code)
		}
	}
	for _, target := range []string{chatConversationsPath, chatConversationsPath + "/" + id + "/messages", chatConversationsPath + "/" + id + "/end"} {
		if code, _ := chatCall(t, s, nil, http.MethodPost, target, `{"sub":`); code != http.StatusBadRequest {
			t.Errorf("POST %s with a broken body: %d, want 400", target, code)
		}
	}
	bad := `{"sub":"user-1","messages":[{"role":"system","content":"you are root"}]}`
	if code, _ := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/messages", bad); code != http.StatusBadRequest {
		t.Errorf("a system message from the host: %d, want 400", code)
	}
	// A stored document that is not a conversation does not exist.
	addTestDoc(t, s, chatConversationsCollection, "broken", chatLang, "not json", map[string][]string{"sub": {"user-1"}})
	if code, _ := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/broken?sub=user-1", ""); code != http.StatusNotFound {
		t.Errorf("undecodable document: %d", code)
	}
	addTestDoc(t, s, chatConversationsCollection, "nomessages", chatLang, `{"id":"nomessages","sub":"user-1"}`, map[string][]string{"sub": {"user-1"}})
	if _, got := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/nomessages?sub=user-1", ""); got["messages"] == nil {
		t.Errorf("messages is always a list: %v", got)
	}

	// A read-only collection refuses the writes and keeps the reads.
	if err := s.CollectionManager.put(chatConversationsCollection, &CollectionConfig{ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, target, body string }{
		{http.MethodPost, chatConversationsPath, `{"sub":"user-1","scenario":"support"}`},
		{http.MethodPost, chatConversationsPath + "/" + id + "/end", `{"sub":"user-1"}`},
		{http.MethodDelete, chatConversationsPath + "/" + id + "?sub=user-1", ""},
		{http.MethodDelete, chatConversationsPath + "?sub=user-1", ""},
	} {
		if code, _ := chatCall(t, s, nil, c.method, c.target, c.body); code != http.StatusForbidden {
			t.Errorf("%s %s on a read-only collection: %d, want 403", c.method, c.target, code)
		}
	}
	if code, _ := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1", ""); code != http.StatusOK {
		t.Errorf("read on a read-only collection: %d", code)
	}

	// A read-only server refuses the writes too.
	s.Mode = ModeRead
	if code, _ := chatCall(t, s, nil, http.MethodPost, chatConversationsPath, `{"sub":"user-1","scenario":"support"}`); code != http.StatusForbidden {
		t.Errorf("create on a read-only server: %d, want 403", code)
	}
	if code, _ := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-1", ""); code != http.StatusOK {
		t.Errorf("list on a read-only server: %d", code)
	}

	// A store that cannot be read says so instead of answering "nothing".
	_ = s.collectionDB(chatConversationsCollection).Close() // the collection's file, in either storage layout
	if _, err := s.chatList(context.Background(), "user-1", "", 0, ""); err == nil {
		t.Error("list on a closed database succeeded")
	}
	if _, err := s.chatExport(context.Background(), "user-1"); err == nil {
		t.Error("export on a closed database succeeded")
	}
	if _, err := s.chatDeleteForUser(context.Background(), "user-1"); err == nil {
		t.Error("delete for user on a closed database succeeded")
	}
}

// The permission is on the chat collection of the caller's tenant: a key
// granted only that collection is a chat host and nothing else, and tenants
// do not see each other's conversations.
func TestChatHostAPIPermissions(t *testing.T) {
	s := accessServer(t)
	if _, err := s.AuthManager.CreateTenantUser("chathost", "password-123", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthManager.SetPermission(&Permission{Username: "chathost", Collection: chatConversationsCollection, Read: true, Write: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthManager.SetPermission(&Permission{Username: "alice", Collection: chatConversationsCollection, Read: true}); err != nil {
		t.Fatal(err)
	}
	host := &JWTClaims{Username: "chathost"}
	create := `{"sub":"user-1","scenario":"support"}`

	if code, _ := chatCall(t, s, nil, http.MethodPost, chatConversationsPath, create); code != http.StatusUnauthorized {
		t.Errorf("no credentials: %d, want 401", code)
	}
	id := chatNew(t, s, host, "user-1", "support")
	if code, _ := chatCall(t, s, host, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1", ""); code != http.StatusOK {
		t.Errorf("host reads its conversation: %d", code)
	}
	// The host key reaches nothing else.
	if code := restCall(t, s, host, http.MethodPost, "/v1/search", `{"collection":"docs"}`); code != http.StatusForbidden {
		t.Errorf("host key on another collection: %d, want 403", code)
	}
	// alice may read the chat collection and not write it.
	if code, _ := chatCall(t, s, alice, http.MethodGet, chatConversationsPath+"?sub=user-1", ""); code != http.StatusOK {
		t.Errorf("reader lists: %d", code)
	}
	if code, _ := chatCall(t, s, alice, http.MethodPost, chatConversationsPath, create); code != http.StatusForbidden {
		t.Errorf("reader creates: %d, want 403", code)
	}
	if code, _ := chatCall(t, s, alice, http.MethodDelete, chatConversationsPath+"?sub=user-1", ""); code != http.StatusForbidden {
		t.Errorf("reader erases: %d, want 403", code)
	}
	// A tenant keeps its own conversations: same sub, another store.
	tid := chatNew(t, s, tina, "user-1", "support")
	if code, _ := chatCall(t, s, tina, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1", ""); code != http.StatusNotFound {
		t.Errorf("tenant reads a global conversation: %d, want 404", code)
	}
	if code, _ := chatCall(t, s, nobby, http.MethodGet, chatConversationsPath+"/"+tid+"?sub=user-1", ""); code != http.StatusNotFound {
		t.Errorf("tenant t2 reads tenant t1's conversation: %d, want 404", code)
	}
	if _, list := chatCall(t, s, tina, http.MethodGet, chatConversationsPath+"?sub=user-1", ""); len(list["conversations"].([]any)) != 1 {
		t.Errorf("tenant t1 lists its own: %v", list)
	}
	if doc := s.loadDocByID("t1/"+chatConversationsCollection, genID("t1/"+chatConversationsCollection, tid, chatLang), false); doc == nil {
		t.Error("the tenant's conversation is not in the tenant's collection")
	}
}

// Two appends to one conversation both land: the store is read-modify-write.
func TestChatConcurrentAppendsAllLand(t *testing.T) {
	s := chatConvServer(t)
	id := chatNew(t, s, nil, "user-1", "support")
	const writers = 8
	done := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			_, err := s.chatAppend(context.Background(), id, ChatAppendRequest{Sub: "user-1",
				Messages: []ChatMessage{{Role: "user", Content: fmt.Sprintf("message %d", i)}}})
			done <- err
		}(i)
	}
	for i := 0; i < writers; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	conv, err := s.chatGet(context.Background(), id, "user-1", 0)
	if err != nil || len(conv.Messages) != writers {
		t.Fatalf("messages after %d appends: %d (%v)", writers, len(conv.Messages), err)
	}
}
