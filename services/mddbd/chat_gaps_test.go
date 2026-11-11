package main

import (
	"context"
	"errors"
	"testing"
)

// Docs gaps (#406): grouping, thresholds and what counts as a gap.

func TestNormalizeChatQuestion(t *testing.T) {
	for in, want := range map[string]string{
		"How do I back up?":          "how do i back up",
		"  how   do I   BACK-UP!!  ": "how do i backup",
		"Jak zrobić kopię?":          "jak zrobić kopię",
		"what's the $price + tax ©?": "whats the price tax",
		"...":                        "",
	} {
		if got := normalizeChatQuestion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestChatGapThreshold(t *testing.T) {
	t.Setenv(chatGapScoreEnv, "")
	if got := chatGapThreshold(0); got != chatDefaultGapThreshold {
		t.Errorf("default: %v", got)
	}
	t.Setenv(chatGapScoreEnv, "0.55")
	if got := chatGapThreshold(0); got != 0.55 {
		t.Errorf("env: %v", got)
	}
	if got := chatGapThreshold(0.9); got != 0.9 {
		t.Errorf("request: %v", got)
	}
	t.Setenv(chatGapScoreEnv, "-1")
	if got := chatGapThreshold(0); got != chatDefaultGapThreshold {
		t.Errorf("a negative env value: %v", got)
	}
}

func TestChatGapOf(t *testing.T) {
	search := func(score float64, keys ...string) ChatToolCall {
		return ChatToolCall{Name: "search_docs", Query: "q", ResultKeys: keys, TopScore: score}
	}
	cases := []struct {
		name         string
		turn         ChatLogTurn
		gap, nothing bool
	}{
		{"noResults outcome", ChatLogTurn{Outcome: ChatOutcomeNoResults}, true, true},
		{"error", ChatLogTurn{Outcome: ChatOutcomeError, ToolCalls: []ChatToolCall{search(0)}}, false, false},
		{"refused", ChatLogTurn{Outcome: ChatOutcomeRefused}, false, false},
		{"no search", ChatLogTurn{Outcome: ChatOutcomeAnswered, ToolCalls: []ChatToolCall{{Name: "x"}}}, false, false},
		{"found nothing", ChatLogTurn{Outcome: ChatOutcomeAnswered, ToolCalls: []ChatToolCall{search(0), search(0)}}, true, true},
		{"low score", ChatLogTurn{Outcome: ChatOutcomeAnswered, ToolCalls: []ChatToolCall{search(0.1, "a"), search(0)}}, true, false},
		{"one good search", ChatLogTurn{Outcome: ChatOutcomeAnswered, ToolCalls: []ChatToolCall{search(0.1, "a"), search(0.8, "b")}}, false, false},
	}
	for _, c := range cases {
		if gap, nothing := chatGapOf(&c.turn, 0.3); gap != c.gap || nothing != c.nothing {
			t.Errorf("%s: %v %v", c.name, gap, nothing)
		}
	}
}

func TestChatGapsGrouping(t *testing.T) {
	none := []ChatToolCall{{Name: "search_docs", Query: "backup"}}
	low := []ChatToolCall{{Name: "search_docs", Query: "x", ResultKeys: []string{"k"}, TopScore: 0.1}}
	turns := []ChatLogTurn{ // newest first, as chatLogTurns returns them
		{Question: "How do I back up?", Scenario: "b", At: 50, Outcome: ChatOutcomeAnswered, ToolCalls: none},
		{Question: "how do i back up", Scenario: "a", At: 40, Outcome: ChatOutcomeNoResults},
		{Question: "Pricing?", Scenario: "a", At: 30, Outcome: ChatOutcomeAnswered, ToolCalls: low},
		{Question: "HOW do I back up", Scenario: "a", At: 20, Outcome: ChatOutcomeAnswered, ToolCalls: low},
		{Question: "Licence?", Scenario: "a", At: 35, Outcome: ChatOutcomeAnswered, ToolCalls: low},
		{Question: "?!", Scenario: "a", At: 10, Outcome: ChatOutcomeNoResults},
		{Question: "Fine", Scenario: "a", At: 5, Outcome: ChatOutcomeError},
	}
	res := chatGaps(turns, ChatGapsFilter{})
	if res.Threshold != chatDefaultGapThreshold || len(res.Gaps) != 3 {
		t.Fatalf("gaps: %+v", res)
	}
	top := res.Gaps[0]
	if top.Question != "how do i back up" || top.Example != "How do I back up?" || top.Count != 3 || top.NoResults != 2 ||
		top.LastAskedAt != 50 || len(top.Scenarios) != 2 || top.Scenarios[0] != "a" {
		t.Errorf("top: %+v", top)
	}
	// Equal counts: the latest asked first.
	if res.Gaps[1].Question != "licence" || res.Gaps[2].Question != "pricing" {
		t.Errorf("order: %+v", res.Gaps)
	}
	if got := chatGaps(turns, ChatGapsFilter{MinCount: 2}); len(got.Gaps) != 1 {
		t.Errorf("minCount: %+v", got.Gaps)
	}
	if got := chatGaps(turns, ChatGapsFilter{Limit: 1}); len(got.Gaps) != 1 {
		t.Errorf("limit: %+v", got.Gaps)
	}
	if got := chatGaps(turns, ChatGapsFilter{Threshold: 0.05}); len(got.Gaps) != 1 || got.Threshold != 0.05 {
		t.Errorf("a lower threshold leaves only what found nothing: %+v", got)
	}
	// Same count and time: alphabetical.
	tie := []ChatLogTurn{{Question: "b", At: 1, Outcome: ChatOutcomeNoResults}, {Question: "a", At: 1, Outcome: ChatOutcomeNoResults}}
	if got := chatGaps(tie, ChatGapsFilter{}); got.Gaps[0].Question != "a" {
		t.Errorf("tie: %+v", got.Gaps)
	}
}

func TestChatGapsOfStore(t *testing.T) {
	s := chatServer(t)
	ctx := context.Background()
	chatLogged(t, s, "anon:s1", "support", true, chatTurnMsgs("Where is the backup guide?", "I found nothing.", ChatOutcomeNoResults, 100)...)
	chatLogged(t, s, "user-1", "support", false, chatTurnMsgs("where is the backup guide", "Nothing.", ChatOutcomeNoResults, 200)...)
	chatLogged(t, s, "user-1", "sales", false, chatTurnMsgs("where is the backup guide", "Nothing.", ChatOutcomeNoResults, 300)...)
	res, err := s.chatGapsOf(ctx, ChatGapsFilter{Scenario: "support"})
	if err != nil || len(res.Gaps) != 1 || res.Gaps[0].Count != 2 {
		t.Fatalf("gaps: %+v %v", res, err)
	}
	res, err = s.chatGapsOf(ctx, ChatGapsFilter{From: 250})
	if err != nil || len(res.Gaps) != 1 || res.Gaps[0].Count != 1 || res.Gaps[0].Scenarios[0] != "sales" {
		t.Fatalf("window: %+v %v", res, err)
	}
	for _, bad := range []ChatGapsFilter{{MinCount: -1}, {Threshold: -1}, {Limit: -1}, {From: 9, To: 1}} {
		if _, err := s.chatGapsOf(ctx, bad); !errors.Is(err, errChatInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}
