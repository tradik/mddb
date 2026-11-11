package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Answer feedback (#406): a rating stored with the answer it rates.

func TestChatFeedbackREST(t *testing.T) {
	s := chatConvServer(t)
	id := chatNew(t, s, nil, "user-1", "support")
	body := `{"sub":"user-1","messages":[{"role":"user","content":"q"},{"id":"ans-1","role":"assistant","content":"a","outcome":"answered"}]}`
	if code, out := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/messages", body); code != http.StatusOK {
		t.Fatalf("append: %d %v", code, out)
	}
	target := chatConversationsPath + "/" + id + "/feedback"
	code, out := chatCall(t, s, nil, http.MethodPost, target, `{"sub":"user-1","messageId":"ans-1","rating":"down","comment":"  Wrong port.  "}`)
	fb, _ := out["feedback"].(map[string]any)
	if code != http.StatusOK || out["conversationId"] != id || out["messageId"] != "ans-1" || fb["rating"] != "down" ||
		fb["comment"] != "Wrong port." || fb["at"] == nil {
		t.Fatalf("rate: %d %v", code, out)
	}
	// Rating again replaces; an ended conversation can still be rated.
	if code, _ := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/end", `{"sub":"user-1"}`); code != http.StatusOK {
		t.Fatal("end")
	}
	if code, _ := chatCall(t, s, nil, http.MethodPost, target, `{"sub":"user-1","messageId":"ans-1","rating":"up"}`); code != http.StatusOK {
		t.Fatalf("re-rate: %d", code)
	}
	conv, err := s.chatGet(context.Background(), id, "user-1", 0)
	if err != nil || conv.Messages[1].Feedback == nil || conv.Messages[1].Feedback.Rating != "up" || conv.Messages[1].Feedback.Comment != "" {
		t.Fatalf("stored: %+v %v", conv, err)
	}
	userID := conv.Messages[0].ID
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"bad json":        {`{`, http.StatusBadRequest},
		"no message":      {`{"sub":"user-1","rating":"up"}`, http.StatusBadRequest},
		"bad rating":      {`{"sub":"user-1","messageId":"ans-1","rating":"meh"}`, http.StatusBadRequest},
		"long comment":    {`{"sub":"user-1","messageId":"ans-1","rating":"up","comment":"` + strings.Repeat("ż", chatMaxCommentRunes+1) + `"}`, http.StatusBadRequest},
		"control":         {`{"sub":"user-1","messageId":"ans-1","rating":"up","comment":"a\u0007b"}`, http.StatusBadRequest},
		"unknown message": {`{"sub":"user-1","messageId":"nope","rating":"up"}`, http.StatusNotFound},
		"a question":      {fmt.Sprintf(`{"sub":"user-1","messageId":%q,"rating":"up"}`, userID), http.StatusNotFound},
		"another user":    {`{"sub":"user-2","messageId":"ans-1","rating":"up"}`, http.StatusNotFound},
	} {
		if code, out := chatCall(t, s, nil, http.MethodPost, target, c.body); code != c.want {
			t.Errorf("%s: %d %v, want %d", name, code, out, c.want)
		}
	}
	// A comment with line breaks is kept; exactly the limit is accepted.
	ok := `{"sub":"user-1","messageId":"ans-1","rating":"down","comment":"line 1\nline 2` + strings.Repeat("x", chatMaxCommentRunes-13) + `"}`
	if code, out := chatCall(t, s, nil, http.MethodPost, target, ok); code != http.StatusOK {
		t.Errorf("limit: %d %v", code, out)
	}
	if code, _ := chatCall(t, s, nil, http.MethodGet, target, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET feedback: %d", code)
	}
}

func TestChatFeedbackInvalidUTF8(t *testing.T) {
	if _, err := validChatFeedback(ChatFeedbackRequest{MessageID: "m", Rating: "up", Comment: "\xff"}); !errors.Is(err, errChatInvalid) {
		t.Errorf("invalid UTF-8: %v", err)
	}
}

// Redaction is sticky: once an append asked for it, later messages and
// feedback comments of the conversation are redacted too.
func TestChatRedactionIsSticky(t *testing.T) {
	s := chatConvServer(t)
	ctx := context.Background()
	conv := chatLogged(t, s, "anon:s1", "support", true,
		chatTurnMsgs("I am jan@example.com, card 4111 1111 1111 1111", "Hello jan@example.com", ChatOutcomeAnswered, 10,
			ChatToolCall{Name: "search_docs", Query: "jan@example.com", ResultKeys: []string{"k"}, TopScore: 0.9})...)
	if !conv.Redact || conv.Messages[0].Content != "I am [redacted], card [redacted]" || conv.Messages[1].Content != "Hello [redacted]" ||
		conv.Messages[1].ToolCalls[0].Query != "[redacted]" {
		t.Fatalf("redacted: %+v", conv)
	}
	if _, err := s.chatAppend(ctx, conv.ID, ChatAppendRequest{Sub: "anon:s1", Messages: []ChatMessage{{ID: "a2", Role: "assistant", Content: "token=abc"}}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.chatFeedback(ctx, conv.ID, ChatFeedbackRequest{Sub: "anon:s1", MessageID: "a2", Rating: "down", Comment: "mail me: x@y.io"})
	if err != nil || res.Feedback.Comment != "mail me: [redacted]" {
		t.Fatalf("comment: %+v %v", res, err)
	}
	got, _ := s.chatGet(ctx, conv.ID, "anon:s1", 0)
	if got.Messages[2].Content != "token=[redacted]" {
		t.Errorf("a later append: %q", got.Messages[2].Content)
	}
	// Without redaction the text is stored as sent.
	plain := chatLogged(t, s, "user-1", "support", false, chatTurnMsgs("a@b.io", "ok", ChatOutcomeAnswered, 10)...)
	if plain.Redact || plain.Messages[0].Content != "a@b.io" {
		t.Errorf("plain: %+v", plain)
	}
}
