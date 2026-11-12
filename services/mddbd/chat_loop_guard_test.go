package main

import (
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"mddb/proto"
)

// #413: a scenario's loop guard — validation, the wire and the loop-stopped
// flag of a logged answer.

func TestChatLoopGuardValidate(t *testing.T) {
	off := false
	for why, ok := range map[string]*ChatLoopGuard{
		"absent":     nil,
		"empty":      {},
		"turned off": {Enabled: &off},
		"full":       {Ngram: 16, Window: 64, Ratio: 0.9, MinOutput: 50, Unit: LoopGuardUnitWords},
		"chars":      {Unit: LoopGuardUnitChars, Ratio: 1},
		"ngram only": {Ngram: 500},
	} {
		if err := ok.validate(); err != nil {
			t.Errorf("%s: %v", why, err)
		}
	}
	for why, bad := range map[string]*ChatLoopGuard{
		"negative ngram":     {Ngram: -1},
		"huge ngram":         {Ngram: maxLoopGuardNgram + 1},
		"huge window":        {Window: maxLoopGuardWindow + 1},
		"negative minOutput": {MinOutput: -1},
		"negative ratio":     {Ratio: -0.1},
		"ratio over one":     {Ratio: 1.5},
		"unknown unit":       {Unit: "tokens"},
		"window under ngram": {Ngram: 64, Window: 32},
	} {
		if bad.validate() == nil {
			t.Errorf("%s was accepted", why)
		}
	}
	sc := &ChatScenario{Name: "s", LLM: ChatLLM{LoopGuard: &ChatLoopGuard{Ratio: 2}}}
	if err := sc.Normalize(anyCollection); err == nil || !strings.Contains(err.Error(), "llm.loopGuard.ratio") {
		t.Errorf("a scenario with a bad guard: %v", err)
	}
}

func TestChatLoopGuardOnTheWire(t *testing.T) {
	if chatLoopGuardToProto(nil) != nil || chatLoopGuardFromProto(nil) != nil {
		t.Fatal("an absent guard must stay absent")
	}
	off := false
	g := &ChatLoopGuard{Enabled: &off, Ngram: 16, Window: 64, Ratio: 0.9, MinOutput: 50, Unit: LoopGuardUnitWords}
	back := chatLoopGuardFromProto(chatLoopGuardToProto(g))
	if back.Enabled == nil || *back.Enabled || back.Ngram != 16 || back.Window != 64 || back.Ratio != 0.9 ||
		back.MinOutput != 50 || back.Unit != LoopGuardUnitWords {
		t.Errorf("round trip: %+v", back)
	}

	s := chatServer(t)
	srv := NewGRPCServer(s)
	sc := chatProtoFixture()
	sc.Llm.LoopGuard = &proto.ChatLoopGuardProto{Ngram: 128, Window: 512, Ratio: 0.95}
	put, err := srv.PutChatScenario(asClaims(root), &proto.PutChatScenarioRequest{Scenario: sc})
	if err != nil {
		t.Fatal(err)
	}
	lg := put.GetScenario().GetLlm().GetLoopGuard()
	if lg.GetNgram() != 128 || lg.GetWindow() != 512 || lg.GetRatio() != 0.95 || lg.Enabled != nil {
		t.Errorf("stored guard: %+v", lg)
	}
	sc.Llm.LoopGuard = &proto.ChatLoopGuardProto{Unit: "tokens"}
	if _, err := srv.PutChatScenario(asClaims(root), &proto.PutChatScenarioRequest{Scenario: sc}); err == nil {
		t.Error("an unknown unit was stored")
	}
}

func TestChatLoopGuardOverREST(t *testing.T) {
	s := chatServer(t)
	body := `{"allowedCollections":["docs"],"llm":{"loopGuard":{"enabled":false,"minOutput":400}}}`
	rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/code", body)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"loopGuard":{"enabled":false,"minOutput":400}`) {
		t.Errorf("stored: %s", rec.Body)
	}
	rec = chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/code", `{"llm":{"loopGuard":{"window":-1}}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a negative window: %d %s", rec.Code, rec.Body)
	}
}

func TestChatLoopStoppedTurn(t *testing.T) {
	s := chatServer(t)
	msgs := chatTurnMsgs("loop?", "Once.\n\n[Response stopped: repetition detected]", ChatOutcomeAnswered, 1000)
	msgs[1].LoopStopped = true
	conv := chatLogged(t, s, "user-1", "support", false, msgs...)
	if !conv.Messages[1].LoopStopped {
		t.Fatalf("stored message: %+v", conv.Messages[1])
	}

	pb := chatConversationToProto(conv.ChatConversationSummary, conv.Messages)
	if !pb.GetMessages()[1].GetLoopStopped() || pb.GetMessages()[0].GetLoopStopped() {
		t.Errorf("on the wire: %+v", pb.GetMessages())
	}
	if !chatMessageFromProto(pb.GetMessages()[1]).LoopStopped {
		t.Error("read back from the wire: the flag is lost")
	}

	page, err := s.chatLog(context.Background(), ChatLogFilter{Scenario: "support"})
	if err != nil || len(page.Turns) != 1 || !page.Turns[0].LoopStopped || !chatLogTurnToProto(page.Turns[0]).GetLoopStopped() {
		t.Fatalf("log: %+v %v", page, err)
	}
	rec := chatREST(t, s, root, http.MethodGet, "/v1/chat/log/export?format=csv", "")
	rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	last := len(chatLogCSVHeader) - 1
	if err != nil || len(rows) != 2 || rows[0][last] != "loopStopped" || rows[1][last] != "true" {
		t.Errorf("csv: %q %v", rows, err)
	}

	_, err = prepareChatMessages([]ChatMessage{{Role: "user", Content: "q", LoopStopped: true}}, 1)
	if err == nil || !strings.Contains(err.Error(), "loop guard") {
		t.Errorf("a user message cannot be loop-stopped: %v", err)
	}
}
