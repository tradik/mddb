package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// The conversation log's model (#406): logged turns, their validation, the
// filters and the paging.

// chatTurnMsgs is one logged turn: a question and its answer.
func chatTurnMsgs(q, a, outcome string, at int64, calls ...ChatToolCall) []ChatMessage {
	return []ChatMessage{{Role: "user", Content: q, CreatedAt: at},
		{Role: "assistant", Content: a, Outcome: outcome, CreatedAt: at + 1, LatencyMs: 120, Model: "m", Provider: "p",
			Tokens: &ChatTokens{Prompt: 10, Completion: 5}, ToolCalls: calls}}
}

// chatLogged creates a conversation of sub and appends turns to it.
func chatLogged(t *testing.T, s *Server, sub, scenario string, redact bool, msgs ...ChatMessage) *ChatConversation {
	t.Helper()
	ctx := context.Background()
	conv, err := s.chatCreate(ctx, ChatCreateRequest{Sub: sub, Scenario: scenario, DisplayName: "Name of " + sub})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.chatAppend(ctx, conv.ID, ChatAppendRequest{Sub: sub, Messages: msgs, Redact: redact}); err != nil {
		t.Fatal(err)
	}
	got, err := s.chatGet(ctx, conv.ID, sub, 0)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestChatLoggingNormalize(t *testing.T) {
	var l ChatLogging
	if err := l.normalize(); err != nil || l.Redact == nil || !*l.Redact || l.Enabled {
		t.Fatalf("defaults: %+v %v", l, err)
	}
	off := false
	l = ChatLogging{Enabled: true, RetentionDays: 7, Redact: &off}
	if err := l.normalize(); err != nil || *l.Redact {
		t.Fatalf("an explicit false stays: %+v %v", l, err)
	}
	for _, days := range []int{-1, chatMaxRetentionDays + 1} {
		l = ChatLogging{RetentionDays: days}
		if err := l.normalize(); !errors.Is(err, errChatInvalid) {
			t.Errorf("retention %d: %v", days, err)
		}
	}
	sc := &ChatScenario{Name: "s", Logging: ChatLogging{RetentionDays: -2}}
	if err := sc.Normalize(nil); err == nil {
		t.Error("a scenario with a negative log retention normalizes")
	}
}

func TestValidChatTurn(t *testing.T) {
	m := ChatMessage{ID: "client-id_1", Role: "assistant", Outcome: ChatOutcomeAnswered, Feedback: &ChatFeedback{Rating: "up"}}
	if err := validChatTurn(0, &m); err != nil || m.ID != "client-id_1" || m.Feedback != nil {
		t.Fatalf("a valid client id stays, an appended rating goes: %+v %v", m, err)
	}
	m = ChatMessage{ID: "not valid!", Role: "user"}
	if err := validChatTurn(0, &m); err != nil || m.ID == "not valid!" || m.ID == "" {
		t.Fatalf("an invalid id is replaced: %+v %v", m, err)
	}
	long := strings.Repeat("q", chatMaxQueryBytes+1)
	keys := make([]string, chatMaxResultKeys+1)
	for name, bad := range map[string]ChatMessage{
		"unknown outcome": {Role: "assistant", Outcome: "great"},
		"user outcome":    {Role: "user", Outcome: ChatOutcomeAnswered},
		"latency":         {Role: "assistant", LatencyMs: -1},
		"calls":           {Role: "assistant", ToolCalls: make([]ChatToolCall, chatMaxToolCallsPerMs+1)},
		"query":           {Role: "assistant", ToolCalls: []ChatToolCall{{Name: "s", Query: long}}},
		"keys":            {Role: "assistant", ToolCalls: []ChatToolCall{{Name: "s", ResultKeys: keys}}},
	} {
		if err := validChatTurn(3, &bad); !errors.Is(err, errChatInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// An error turn may have no content at all.
	if _, err := prepareChatMessages([]ChatMessage{{Role: "assistant", Outcome: ChatOutcomeError}}, 1); err != nil {
		t.Errorf("an error turn without content: %v", err)
	}
	if _, err := prepareChatMessages([]ChatMessage{{Role: "assistant", Outcome: "nope"}}, 1); err == nil {
		t.Error("an unknown outcome is accepted")
	}
}

func TestChatRefusals(t *testing.T) {
	s := chatServer(t)
	if got := (&Server{}).chatRefusalText("x"); got != "" {
		t.Errorf("no scenario store: %q", got)
	}
	for _, sc := range []*ChatScenario{
		{Name: "redir", Enabled: true, OffTopic: ChatOffTopic{Mode: OffTopicRedirect, RedirectTemplate: "  Ask me about MDDB.  "}},
		{Name: "plain", Enabled: true, OffTopic: ChatOffTopic{Mode: OffTopicRedirect}},
		{Name: "refuse", Enabled: true},
	} {
		if _, err := s.PutChatScenario(context.Background(), sc); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{"redir": "Ask me about MDDB.", "plain": defaultRedirectTemplate, "refuse": "", "gone": ""} {
		if got := s.chatRefusalText(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	msgs := []ChatMessage{{Role: "assistant", Content: "Sorry.\n\nask me  ABOUT mddb.", Outcome: ChatOutcomeAnswered},
		{Role: "assistant", Content: "Ask me about MDDB.", Outcome: ChatOutcomeError},
		{Role: "assistant", Content: "Replication works so.", Outcome: ChatOutcomeAnswered}}
	markChatRefusals(msgs, "")
	if msgs[0].Outcome != ChatOutcomeAnswered {
		t.Fatal("no refusal text marks nothing")
	}
	markChatRefusals(msgs, "Ask me about MDDB.")
	if msgs[0].Outcome != ChatOutcomeRefused || msgs[1].Outcome != ChatOutcomeError || msgs[2].Outcome != ChatOutcomeAnswered {
		t.Fatalf("outcomes: %+v", msgs)
	}
	// Through the store: the append marks it.
	conv := chatLogged(t, s, "u", "redir", false, chatTurnMsgs("weather?", "Ask me about MDDB.", ChatOutcomeAnswered, 100)...)
	if conv.Messages[1].Outcome != ChatOutcomeRefused {
		t.Errorf("stored outcome: %q", conv.Messages[1].Outcome)
	}
}

func TestChatExcerptAndAnonymous(t *testing.T) {
	if got := chatExcerpt("  two\nlines "); got != "two lines" {
		t.Errorf("short: %q", got)
	}
	if got := chatExcerpt(strings.Repeat("ż", chatExcerptRunes+5)); got != strings.Repeat("ż", chatExcerptRunes)+"…" {
		t.Errorf("long: %q", got)
	}
	if !chatAnonymous("anon:abc") || chatAnonymous("user-1") {
		t.Error("anonymous")
	}
}

func TestChatLogFilter(t *testing.T) {
	for _, bad := range []ChatLogFilter{{Rating: "meh"}, {Outcome: "x"}, {From: -1}, {To: -1}, {From: 5, To: 4}} {
		if err := bad.validate(); !errors.Is(err, errChatInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	up := ChatLogTurn{At: 10, Outcome: ChatOutcomeAnswered, Rating: ChatRatingUp}
	none := ChatLogTurn{At: 10, Outcome: ChatOutcomeNoResults}
	cases := []struct {
		f    ChatLogFilter
		t    ChatLogTurn
		want bool
	}{
		{ChatLogFilter{}, up, true},
		{ChatLogFilter{From: 11}, up, false},
		{ChatLogFilter{To: 9}, up, false},
		{ChatLogFilter{From: 10, To: 10}, up, true},
		{ChatLogFilter{Outcome: ChatOutcomeNoResults}, up, false},
		{ChatLogFilter{Outcome: ChatOutcomeNoResults}, none, true},
		{ChatLogFilter{Rating: "none"}, none, true},
		{ChatLogFilter{Rating: "none"}, up, false},
		{ChatLogFilter{Rating: "any"}, up, true},
		{ChatLogFilter{Rating: "any"}, none, false},
		{ChatLogFilter{Rating: ChatRatingDown}, up, false},
		{ChatLogFilter{Rating: ChatRatingUp}, up, true},
	}
	for i, c := range cases {
		if got := c.f.matches(&c.t); got != c.want {
			t.Errorf("case %d: %v", i, got)
		}
	}
}

func TestChatLogTurnsAndPaging(t *testing.T) {
	convs := []*ChatConversation{
		{ChatConversationSummary: ChatConversationSummary{ID: "c1", Scenario: "s", Sub: "user-1", DisplayName: "Ada"},
			Messages: append(append(chatTurnMsgs("q1", "a1", ChatOutcomeAnswered, 100),
				ChatMessage{Role: "assistant", Content: "not logged", CreatedAt: 150}),
				chatTurnMsgs("q2", "a2", ChatOutcomeNoResults, 200)...)},
		{ChatConversationSummary: ChatConversationSummary{ID: "c2", Scenario: "s", Sub: "anon:sess", DisplayName: "Guest"},
			Messages: append([]ChatMessage{{Role: "tool", Content: "x"}},
				ChatMessage{Role: "assistant", Content: "no question", Outcome: ChatOutcomeError, CreatedAt: 201})},
	}
	convs[0].Messages[1].ID, convs[0].Messages[1].Feedback = "m1", &ChatFeedback{Rating: ChatRatingDown, Comment: "wrong"}
	turns := chatLogTurns(convs, ChatLogFilter{})
	// Newest first; at the same second the larger conversation id first.
	if len(turns) != 3 || turns[0].ConversationID != "c2" || turns[1].Question != "q2" || turns[2].Question != "q1" {
		t.Fatalf("turns: %+v", turns)
	}
	byQ := map[string]ChatLogTurn{}
	for _, tr := range turns {
		byQ[tr.Answer] = tr
	}
	if a := byQ["no question"]; !a.Anonymous || a.Sub != "" || a.DisplayName != "" || a.Question != "" {
		t.Errorf("anonymous turn: %+v", a)
	}
	if a := byQ["a1"]; a.Sub != "user-1" || a.DisplayName != "Ada" || a.Rating != ChatRatingDown || a.Comment != "wrong" ||
		a.Question != "q1" || a.Tokens.Prompt != 10 || a.LatencyMs != 120 || a.AnswerExcerpt != "a1" {
		t.Errorf("signed turn: %+v", a)
	}
	if got := chatLogTurns(convs, ChatLogFilter{Rating: ChatRatingDown}); len(got) != 1 {
		t.Errorf("down: %d", len(got))
	}

	// Ties on time are ordered by the ids, so the order is total.
	same := []ChatLogTurn{{At: 1, ConversationID: "a", MessageID: "1"}, {At: 1, ConversationID: "a", MessageID: "2"}, {At: 1, ConversationID: "b"}}
	if !chatTurnNewer(same[2], same[0]) || !chatTurnNewer(same[1], same[0]) || chatTurnNewer(same[0], same[1]) {
		t.Error("tie order")
	}

	page, err := pageChatLog(turns, 2, "")
	if err != nil || len(page.Turns) != 2 || page.NextCursor == "" {
		t.Fatalf("page 1: %+v %v", page, err)
	}
	next, err := pageChatLog(turns, 2, page.NextCursor)
	if err != nil || len(next.Turns) != 1 || next.NextCursor != "" || next.Turns[0].MessageID != turns[2].MessageID {
		t.Fatalf("page 2: %+v %v", next, err)
	}
	if all, _ := pageChatLog(turns, 0, ""); len(all.Turns) != 3 {
		t.Error("default limit")
	}
	last := base64.RawURLEncoding.EncodeToString([]byte(chatTurnKey(ChatLogTurn{At: 0})))
	if end, _ := pageChatLog(turns, 2, last); len(end.Turns) != 0 || end.Turns == nil {
		t.Errorf("past the end: %+v", end)
	}
	for _, bad := range []string{"!!", base64.RawURLEncoding.EncodeToString([]byte("1|2")), base64.RawURLEncoding.EncodeToString([]byte("x|a|b"))} {
		if _, err := pageChatLog(turns, 2, bad); !errors.Is(err, errChatInvalid) {
			t.Errorf("cursor %q: %v", bad, err)
		}
	}
}
