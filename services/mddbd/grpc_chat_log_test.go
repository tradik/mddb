package main

import (
	"testing"

	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Feedback and the conversation log over gRPC (#406): the REST behaviour.

func TestGRPCChatLog(t *testing.T) {
	s := chatServer(t)
	g, ctx := NewGRPCServer(s), asClaims(root)
	signed, _ := chatLogSeed(t, s)

	// The #406 fields of a message travel both ways.
	added, err := g.AppendChatMessages(ctx, &proto.AppendChatMessagesRequest{Id: signed.ID, Sub: "user-1", Redact: true, Messages: []*proto.ChatMessageProto{
		{Role: "user", Content: "mail a@b.io"},
		{Id: "ans-g", Role: "assistant", Content: "ok", Outcome: ChatOutcomeAnswered, LatencyMs: 33,
			ToolCalls: []*proto.ChatToolCallProto{{Name: "search_docs", Query: "q", ResultKeys: []string{"k1"}, TopScore: 0.5},
				{Name: "lookup", Server: "crm"}}},
	}})
	if err != nil || !added.GetRedact() || added.GetMessages()[0].GetContent() != "mail [redacted]" || added.GetMessages()[1].GetId() != "ans-g" ||
		added.GetMessages()[1].GetLatencyMs() != 33 || added.GetMessages()[1].GetToolCalls()[0].GetTopScore() != 0.5 ||
		added.GetMessages()[1].GetToolCalls()[1].GetServer() != "crm" {
		t.Fatalf("append: %v %v", added, err)
	}
	rated, err := g.RateChatMessage(ctx, &proto.RateChatMessageRequest{Id: signed.ID, Sub: "user-1", MessageId: "ans-g", Rating: "up", Comment: "nice"})
	if err != nil || rated.GetFeedback().GetRating() != "up" || rated.GetMessageId() != "ans-g" || rated.GetConversationId() != signed.ID {
		t.Fatalf("rate: %v %v", rated, err)
	}
	conv, err := g.GetChatConversation(ctx, &proto.GetChatConversationRequest{Id: signed.ID, Sub: "user-1"})
	if err != nil || conv.GetMessages()[3].GetFeedback().GetComment() != "nice" || conv.GetMessages()[3].GetOutcome() != ChatOutcomeAnswered {
		t.Fatalf("get: %v %v", conv, err)
	}
	if _, err := g.RateChatMessage(ctx, &proto.RateChatMessageRequest{Id: signed.ID, Sub: "user-1", MessageId: "ans-g", Rating: "x"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad rating: %v", err)
	}
	if _, err := g.RateChatMessage(ctx, &proto.RateChatMessageRequest{Id: signed.ID, Sub: "user-1", MessageId: "nope", Rating: "up"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown message: %v", err)
	}
	s.Mode = ModeRead
	if _, err := g.RateChatMessage(ctx, &proto.RateChatMessageRequest{Id: signed.ID, Sub: "user-1", MessageId: "ans-g", Rating: "up"}); err == nil {
		t.Error("a read-only server stores a rating")
	}

	page, err := g.ListChatLog(ctx, &proto.ListChatLogRequest{Scenario: "support", Limit: 1})
	if err != nil || len(page.GetTurns()) != 1 || page.GetNextCursor() == "" {
		t.Fatalf("log: %v %v", page, err)
	}
	top := page.GetTurns()[0]
	if top.GetMessageId() != "ans-g" || top.GetRating() != "up" || top.GetQuestion() != "mail [redacted]" || top.GetPromptTokens() != 0 {
		t.Errorf("turn: %v", top)
	}
	page, err = g.ListChatLog(ctx, &proto.ListChatLogRequest{Rating: "down"})
	if err != nil || len(page.GetTurns()) != 1 || page.GetTurns()[0].GetSub() != "user-1" || page.GetTurns()[0].GetPromptTokens() != 10 {
		t.Fatalf("down: %v %v", page, err)
	}
	if _, err := g.ListChatLog(ctx, &proto.ListChatLogRequest{Outcome: "x"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad outcome: %v", err)
	}

	gaps, err := g.ListChatGaps(ctx, &proto.ListChatGapsRequest{Threshold: 0.9, MinCount: 1})
	if err != nil || gaps.GetThreshold() != 0.9 || len(gaps.GetGaps()) == 0 || gaps.GetGaps()[0].GetCount() < 1 {
		t.Fatalf("gaps: %v %v", gaps, err)
	}
	if _, err := g.ListChatGaps(ctx, &proto.ListChatGapsRequest{Limit: -1}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad limit: %v", err)
	}
}

// The scenario's logging settings survive the gRPC round trip; absent, the
// log is off and redaction on.
func TestGRPCChatScenarioLogging(t *testing.T) {
	off := false
	p := chatScenarioToProto(&ChatScenario{Name: "s", Logging: ChatLogging{Enabled: true, RetentionDays: 9, Redact: &off}})
	if !p.GetLogging().GetEnabled() || p.GetLogging().GetRetentionDays() != 9 || p.GetLogging().Redact == nil || p.GetLogging().GetRedact() {
		t.Fatalf("to proto: %v", p.GetLogging())
	}
	back, err := chatScenarioFromProto(p)
	if err != nil || !back.Logging.Enabled || back.Logging.RetentionDays != 9 || *back.Logging.Redact {
		t.Fatalf("from proto: %+v %v", back.Logging, err)
	}
	bare, err := chatScenarioFromProto(&proto.ChatScenarioProto{Name: "b"})
	if err != nil || bare.Logging.Enabled || bare.Logging.Redact != nil || bare.Normalize(nil) != nil || !*bare.Logging.Redact {
		t.Fatalf("bare: %+v %v", bare.Logging, err)
	}
}
