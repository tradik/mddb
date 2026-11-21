package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The remaining branches of the files #439 touched: prompts and completion
// against a failing client and a long collection list, a token request with
// a body that is no form, and an event for a client whose buffer is full.

func TestMCPProtocolFailingClientAndLongLists(t *testing.T) {
	ctx := context.Background()
	if values, _, _ := mcpCompleteCollection(ctx, covBNewAllErr(), ""); values != nil {
		t.Errorf("completion with a failing client = %v", values)
	}
	if p := collectionResponsePrompt(ctx, covBNewAllErr(), "a"); p != "" {
		t.Errorf("response prompt with a failing client = %q", p)
	}

	s := scopeServer(t)
	if err := s.CollectionManager.Set("a", &CollectionConfig{ResponsePrompt: "Cite {{collection}}."}); err != nil {
		t.Fatal(err)
	}
	msgs, _, err := mcpGetPrompt(ctx, NewDirectClient(s), "rag-pipeline", map[string]any{"collection": "a"})
	if err != nil || !strings.Contains(msgs[0].Content["text"].(string), "Cite a.") {
		t.Errorf("rag-pipeline lost the response prompt: %v %v", msgs, err)
	}
	for i := 0; i < 101; i++ {
		addTestDoc(t, s, fmt.Sprintf("c%03d", i), "k", "en", "x", nil)
	}
	values, total, more := mcpCompleteCollection(ctx, NewDirectClient(s), "")
	if len(values) != 100 || total != 103 || !more {
		t.Errorf("long completion = %d of %d, more=%v", len(values), total, more)
	}
}

func TestOAuthTokenBodyIsNoForm(t *testing.T) {
	_, _, h, _ := newTestOAuth(t)
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("a=%zz"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := serve(h, r); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "form body required") {
		t.Errorf("unparseable token body: %d %s", rec.Code, rec.Body)
	}
}

func TestSSEFullBufferSkipsEvent(t *testing.T) {
	hub := NewSSEHub(true, 10, 10)
	client := &sseClient{ch: make(chan []byte), mode: "read"} // unbuffered: always full
	hub.mu.Lock()
	hub.clients[client] = true
	hub.mu.Unlock()
	hub.Broadcast("doc.added", "a", "k", "en") // must not block
	select {
	case <-client.ch:
		t.Error("an event reached a client with no room for it")
	default:
	}
}
