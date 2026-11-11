package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// The rules of the chat conversation model (#405), without a server.

func TestChatTitleCutsBetweenCharacters(t *testing.T) {
	if got := chatTitle("  Jak   zresetować\nhasło? "); got != "Jak zresetować hasło?" {
		t.Errorf("whitespace: %q", got)
	}
	long := strings.Repeat("ż", chatTitleRunes+20)
	got := chatTitle(long)
	if utf8.RuneCountInString(got) != chatTitleRunes || !utf8.ValidString(got) {
		t.Errorf("long title: %d runes, valid=%v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
}

func TestChatRetentionDays(t *testing.T) {
	for _, bad := range []int{-1, chatMaxRetentionDays + 1} {
		if _, err := chatRetentionDays(bad); !errors.Is(err, errChatInvalid) {
			t.Errorf("retention %d: %v", bad, err)
		}
	}
	if n, _ := chatRetentionDays(7); n != 7 {
		t.Errorf("explicit: %d", n)
	}
	if n, _ := chatRetentionDays(0); n != chatDefaultRetentionDays {
		t.Errorf("default: %d", n)
	}
	t.Setenv(chatRetentionEnv, "90")
	if n, _ := chatRetentionDays(0); n != 90 {
		t.Errorf("env: %d", n)
	}
	for _, v := range []string{"soon", "0", "99999"} {
		t.Setenv(chatRetentionEnv, v)
		if n, _ := chatRetentionDays(0); n != chatDefaultRetentionDays {
			t.Errorf("env %q: %d", v, n)
		}
	}
}

func TestValidChatName(t *testing.T) {
	cases := []struct {
		value    string
		required bool
		ok       bool
	}{
		{"user-1", true, true},
		{"", true, false},
		{"", false, true},
		{strings.Repeat("x", chatMaxNameBytes+1), true, false},
		{"a\x00b", true, false},
		{"a\xffb", true, false},
		{"Zażółć|gęślą", true, true},
	}
	for _, c := range cases {
		if err := validChatName("sub", c.value, c.required); (err == nil) != c.ok {
			t.Errorf("%q required=%v: %v", c.value, c.required, err)
		}
	}
}

func TestPrepareChatMessages(t *testing.T) {
	bad := map[string][]ChatMessage{
		"none":         {},
		"too many":     make([]ChatMessage, chatMaxAppend+1),
		"role":         {{Role: "system", Content: "x"}},
		"no content":   {{Role: "user"}},
		"long content": {{Role: "user", Content: strings.Repeat("x", chatMaxContentBytes+1)}},
		"negative at":  {{Role: "user", Content: "x", CreatedAt: -1}},
	}
	for name, msgs := range bad {
		if _, err := prepareChatMessages(msgs, 100); !errors.Is(err, errChatInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	out, err := prepareChatMessages([]ChatMessage{
		{ID: "client chosen!", Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ChatToolCall{{Name: "search", Collection: "docs", DurationMs: 12}}, CreatedAt: 50},
		{Role: "tool", Content: "3 hits"},
	}, 100)
	if err != nil {
		t.Fatal(err)
	}
	// #406: a client id that looks like one is kept (a rating names the
	// answer by it); anything else is replaced by the server's.
	if out[0].ID == "client chosen!" || out[0].ID == "" || out[0].ID == out[1].ID {
		t.Errorf("ids are the server's: %q %q", out[0].ID, out[1].ID)
	}
	if out[0].CreatedAt != 100 || out[1].CreatedAt != 50 {
		t.Errorf("times: %d %d", out[0].CreatedAt, out[1].CreatedAt)
	}
}

func TestAppendChatMessages(t *testing.T) {
	conv := &ChatConversation{}
	msgs := []ChatMessage{{Role: "assistant", Content: "Hello", CreatedAt: 9}, {Role: "user", Content: "First question", CreatedAt: 7},
		{Role: "user", Content: "Second", CreatedAt: 8}}
	if err := appendChatMessages(conv, msgs); err != nil {
		t.Fatal(err)
	}
	if conv.Title != "First question" || conv.LastMessageAt != 9 || conv.MessageCount != 3 {
		t.Errorf("after append: %+v", conv.ChatConversationSummary)
	}
	conv.Messages = make([]ChatMessage, chatMaxMessages)
	if err := appendChatMessages(conv, msgs[:1]); !errors.Is(err, errChatInvalid) {
		t.Errorf("full conversation: %v", err)
	}
	conv.Ended = true
	if err := appendChatMessages(conv, msgs[:1]); !errors.Is(err, errChatEnded) {
		t.Errorf("ended conversation: %v", err)
	}
}

func TestLastChatMessages(t *testing.T) {
	conv := &ChatConversation{Messages: []ChatMessage{{ID: "1"}, {ID: "2"}, {ID: "3"}}}
	if got := lastChatMessages(conv, 0); len(got.Messages) != 3 {
		t.Errorf("no limit: %d", len(got.Messages))
	}
	if got := lastChatMessages(conv, 2); len(got.Messages) != 2 || got.Messages[0].ID != "2" {
		t.Errorf("last two: %+v", got.Messages)
	}
}

func chatSummaries(n int) []ChatConversationSummary {
	out := make([]ChatConversationSummary, 0, n)
	for i := n; i > 0; i-- { // newest first
		out = append(out, ChatConversationSummary{ID: fmt.Sprintf("c%d", i), CreatedAt: int64(i)})
	}
	return out
}

func TestPageChatSummaries(t *testing.T) {
	all := chatSummaries(5)
	page, err := pageChatSummaries(all, 2, "")
	if err != nil || len(page.Conversations) != 2 || page.NextCursor == "" || page.Conversations[0].CreatedAt != 5 {
		t.Fatalf("first page: %+v %v", page, err)
	}
	var seen []int64
	for cursor := ""; ; {
		p, err := pageChatSummaries(all, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range p.Conversations {
			seen = append(seen, c.CreatedAt)
		}
		if cursor = p.NextCursor; cursor == "" {
			break
		}
	}
	if len(seen) != 5 || seen[0] != 5 || seen[4] != 1 {
		t.Errorf("walk: %v", seen)
	}
	// A cursor past the end is an empty page, and the page is never null.
	last, err := pageChatSummaries(all, 0, chatCursor(all[4]))
	if err != nil || last.Conversations == nil || len(last.Conversations) != 0 || last.NextCursor != "" {
		t.Errorf("past the end: %+v %v", last, err)
	}
	big := make([]ChatConversationSummary, chatMaxListLimit+10)
	if p, _ := pageChatSummaries(big, 100000, ""); len(p.Conversations) != chatMaxListLimit {
		t.Errorf("limit cap: %d", len(p.Conversations))
	}
	if p, _ := pageChatSummaries(big, 0, ""); len(p.Conversations) != chatDefaultListLimit {
		t.Errorf("default limit: %d", len(p.Conversations))
	}
}

func TestChatCursorRefusesGarbage(t *testing.T) {
	enc := base64.RawURLEncoding.EncodeToString
	for _, cursor := range []string{"***", enc([]byte("no-separator")), enc([]byte("abc|id"))} {
		if _, err := pageChatSummaries(nil, 1, cursor); !errors.Is(err, errChatInvalid) {
			t.Errorf("cursor %q: %v", cursor, err)
		}
	}
	// Same time: the id decides, so the order is total.
	a, b := ChatConversationSummary{ID: "b", CreatedAt: 1}, ChatConversationSummary{ID: "a", CreatedAt: 1}
	if !chatNewer(a, b) || chatNewer(b, a) {
		t.Error("tie is not broken by id")
	}
}

func TestChatCollectionIsPerTenant(t *testing.T) {
	if got := chatCollection(context.Background()); got != "chat_conversations" {
		t.Errorf("global: %q", got)
	}
	ctx := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "tina", Tenant: "t1"})
	if got := chatCollection(ctx); got != "t1/chat_conversations" {
		t.Errorf("tenant: %q", got)
	}
}
