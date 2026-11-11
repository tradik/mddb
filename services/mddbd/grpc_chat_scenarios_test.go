package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"mddb/proto"

	"google.golang.org/grpc/codes"
)

// #403: chat scenarios over gRPC, through the functions REST uses.

func asClaims(c *JWTClaims) context.Context {
	return context.WithValue(context.Background(), authContextKey, c)
}

func chatProtoFixture() *proto.ChatScenarioProto {
	return &proto.ChatScenarioProto{
		Name: "support", Description: "Support desk", Enabled: true, OperatorPrompt: "You are Ada.", Guardrails: "No legal advice.",
		OffTopic:           &proto.ChatOffTopicProto{Mode: OffTopicRedirect, RedirectTemplate: "Ask me about MDDB."},
		AllowedCollections: []string{"docs"}, Skills: []string{"triage"},
		Style:    &proto.ChatStyleProto{Emoji: EmojiSparingly, Verbosity: VerbosityShort, Markdown: true, CitationTemplate: "[{{key}}]"},
		Llm:      &proto.ChatLLMProto{Model: "gpt-x", TemperatureSet: true, MaxTokens: 800, ExtraBodyJson: `{"reasoning_effort":"low"}`},
		MaxTurns: 12, TokenBudgetPerSession: 50000, RateLimit: &proto.ChatRateLimitProto{MessagesPerMinute: 20},
	}
}

func TestChatScenarioGRPCLifecycle(t *testing.T) {
	s := chatServer(t)
	g := NewGRPCServer(s)
	admin := asClaims(root)

	put, err := g.PutChatScenario(admin, &proto.PutChatScenarioRequest{Scenario: chatProtoFixture()})
	if err != nil || !put.GetCreated() || put.GetVersion() != 1 {
		t.Fatalf("create: %+v, %v", put, err)
	}
	got := put.GetScenario()
	if got.GetLlm().GetExtraBodyJson() != `{"reasoning_effort":"low"}` || !got.GetLlm().GetTemperatureSet() || got.GetLlm().GetTemperature() != 0 ||
		got.GetCreatedAt() == 0 || got.GetUpdatedAt() == 0 || got.GetOffTopic().GetRedirectTemplate() != "Ask me about MDDB." ||
		got.GetStyle().GetCitationTemplate() != "[{{key}}]" || got.GetRateLimit().GetMessagesPerMinute() != 20 ||
		got.GetTokenBudgetPerSession() != 50000 || got.GetMaxTurns() != 12 || got.GetSkills()[0] != "triage" {
		t.Errorf("on the wire: %+v", got)
	}
	plain := &proto.ChatScenarioProto{Name: "support", AllowedCollections: []string{"docs"}}
	put, err = g.PutChatScenario(admin, &proto.PutChatScenarioRequest{Scenario: plain})
	if err != nil || put.GetCreated() || put.GetScenario().GetLlm().GetTemperatureSet() || put.GetScenario().GetLlm().GetExtraBodyJson() != "" ||
		put.GetScenario().GetOffTopic().GetMode() != OffTopicRefuse {
		t.Fatalf("replace: %+v, %v", put, err)
	}
	list, err := g.ListChatScenarios(admin, &proto.ListChatScenariosRequest{})
	if err != nil || len(list.GetScenarios()) != 1 || list.GetVersion() != 2 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	one, err := g.GetChatScenario(asClaims(alice), &proto.GetChatScenarioRequest{Name: "support"})
	if err != nil || one.GetName() != "support" {
		t.Fatalf("get as a reader of docs: %v", err)
	}
	prompt, err := g.GetChatScenarioPrompt(admin, &proto.GetChatScenarioPromptRequest{Name: "support"})
	if err != nil || prompt.GetName() != "support" || prompt.GetVersion() != 2 || !strings.Contains(prompt.GetPrompt(), "decline politely") {
		t.Fatalf("prompt: %+v, %v", prompt, err)
	}

	for why, req := range map[string]*proto.PutChatScenarioRequest{
		"no scenario":        {},
		"bad extra body":     {Scenario: &proto.ChatScenarioProto{Name: "x", Llm: &proto.ChatLLMProto{ExtraBodyJson: "["}}},
		"unknown collection": {Scenario: &proto.ChatScenarioProto{Name: "x", AllowedCollections: []string{"ghost"}}},
	} {
		if _, err := g.PutChatScenario(admin, req); grpcCode(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", why, err)
		}
	}

	// A scenario over a collection alice may not read, and no credentials.
	private := &proto.ChatScenarioProto{Name: "private-bot", AllowedCollections: []string{"private"}}
	if _, err := g.PutChatScenario(admin, &proto.PutChatScenarioRequest{Scenario: private}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GetChatScenario(asClaims(alice), &proto.GetChatScenarioRequest{Name: "private-bot"}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("get without the permission: %v", err)
	}
	if _, err := g.GetChatScenarioPrompt(asClaims(alice), &proto.GetChatScenarioPromptRequest{Name: "private-bot"}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("prompt without the permission: %v", err)
	}
	if _, err := g.GetChatScenario(context.Background(), &proto.GetChatScenarioRequest{Name: "private-bot"}); grpcCode(err) != codes.Unauthenticated {
		t.Errorf("get without credentials: %v", err)
	}
	if list, _ := g.ListChatScenarios(asClaims(alice), &proto.ListChatScenariosRequest{}); len(list.GetScenarios()) != 1 {
		t.Errorf("alice lists %d scenarios, want the one over docs", len(list.GetScenarios()))
	}
	for _, who := range []*JWTClaims{alice, tina} {
		if _, err := g.PutChatScenario(asClaims(who), &proto.PutChatScenarioRequest{Scenario: plain}); grpcCode(err) != codes.PermissionDenied {
			t.Errorf("%s put: %v", who.Username, err)
		}
		if _, err := g.DeleteChatScenario(asClaims(who), &proto.DeleteChatScenarioRequest{Name: "support"}); grpcCode(err) != codes.PermissionDenied {
			t.Errorf("%s delete: %v", who.Username, err)
		}
	}

	del, err := g.DeleteChatScenario(admin, &proto.DeleteChatScenarioRequest{Name: "support"})
	if err != nil || !del.GetDeleted() || del.GetVersion() != 4 {
		t.Fatalf("delete: %+v, %v", del, err)
	}
	if _, err := g.DeleteChatScenario(admin, &proto.DeleteChatScenarioRequest{Name: "support"}); grpcCode(err) != codes.NotFound {
		t.Errorf("delete twice: %v", err)
	}
	if _, err := g.GetChatScenario(admin, &proto.GetChatScenarioRequest{Name: "support"}); grpcCode(err) != codes.NotFound {
		t.Errorf("get after delete: %v", err)
	}
	if _, err := g.GetChatScenarioPrompt(admin, &proto.GetChatScenarioPromptRequest{Name: "support"}); grpcCode(err) != codes.NotFound {
		t.Errorf("prompt after delete: %v", err)
	}
	s.Mode = ModeRead
	if _, err := g.PutChatScenario(admin, &proto.PutChatScenarioRequest{Scenario: plain}); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("put on a read-only server: %v", err)
	}
}

func TestChatScenarioProtoZeroTime(t *testing.T) {
	if p := chatScenarioToProto(&ChatScenario{Name: "s"}); p.GetCreatedAt() != 0 || p.GetUpdatedAt() != 0 {
		t.Errorf("zero times on the wire: %d %d", p.GetCreatedAt(), p.GetUpdatedAt())
	}
	at := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if p := chatScenarioToProto(&ChatScenario{Name: "s", CreatedAt: at}); p.GetCreatedAt() != at.Unix() {
		t.Errorf("created_at = %d", p.GetCreatedAt())
	}
}
