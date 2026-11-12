package main

import (
	"context"

	"mddb/proto"
)

// Answer feedback and the conversation log over gRPC (#406), calling what
// the REST handlers call. RateChatMessage authorizes itself as an append
// does (write on the caller's chat collection); ListChatLog and
// ListChatGaps are global admin, enforced by the gate
// (access_rules_grpc.go).

// RateChatMessage rates an answer, as POST /v1/chat/conversations/{id}/feedback.
func (g *GRPCServer) RateChatMessage(ctx context.Context, req *proto.RateChatMessageRequest) (*proto.RateChatMessageResponse, error) {
	if err := g.chatAccess(ctx, true); err != nil {
		return nil, err
	}
	res, err := g.server.chatFeedback(ctx, req.GetId(), ChatFeedbackRequest{Sub: req.GetSub(), MessageID: req.GetMessageId(),
		Rating: req.GetRating(), Comment: req.GetComment()})
	if err != nil {
		return nil, chatGRPCError(err)
	}
	return &proto.RateChatMessageResponse{ConversationId: res.ConversationID, MessageId: res.MessageID,
		Feedback: &proto.ChatFeedbackProto{Rating: res.Feedback.Rating, Comment: res.Feedback.Comment, At: res.Feedback.At}}, nil
}

// ListChatLog is one page of the log, as GET /v1/chat/log.
func (g *GRPCServer) ListChatLog(ctx context.Context, req *proto.ListChatLogRequest) (*proto.ListChatLogResponse, error) {
	page, err := g.server.chatLog(ctx, ChatLogFilter{Scenario: req.GetScenario(), From: req.GetFrom(), To: req.GetTo(),
		Rating: req.GetRating(), Outcome: req.GetOutcome(), Cursor: req.GetCursor(), Limit: int(req.GetLimit())})
	if err != nil {
		return nil, chatGRPCError(err)
	}
	out := &proto.ListChatLogResponse{NextCursor: page.NextCursor, Turns: make([]*proto.ChatLogTurnProto, 0, len(page.Turns))}
	for _, t := range page.Turns {
		out.Turns = append(out.Turns, chatLogTurnToProto(t))
	}
	return out, nil
}

// ListChatGaps is the docs gaps, as GET /v1/chat/gaps.
func (g *GRPCServer) ListChatGaps(ctx context.Context, req *proto.ListChatGapsRequest) (*proto.ListChatGapsResponse, error) {
	res, err := g.server.chatGapsOf(ctx, ChatGapsFilter{Scenario: req.GetScenario(), From: req.GetFrom(), To: req.GetTo(),
		MinCount: int(req.GetMinCount()), Threshold: req.GetThreshold(), Limit: int(req.GetLimit())})
	if err != nil {
		return nil, chatGRPCError(err)
	}
	out := &proto.ListChatGapsResponse{Threshold: res.Threshold, Gaps: make([]*proto.ChatGapProto, 0, len(res.Gaps))}
	for _, gp := range res.Gaps {
		out.Gaps = append(out.Gaps, &proto.ChatGapProto{Question: gp.Question, Example: gp.Example, Count: safeInt32(gp.Count),
			NoResults: safeInt32(gp.NoResults), LastAskedAt: gp.LastAskedAt, Scenarios: gp.Scenarios})
	}
	return out, nil
}

// chatLogTurnToProto is a turn as its message.
func chatLogTurnToProto(t ChatLogTurn) *proto.ChatLogTurnProto {
	out := &proto.ChatLogTurnProto{ConversationId: t.ConversationID, MessageId: t.MessageID, Scenario: t.Scenario,
		Sub: t.Sub, DisplayName: t.DisplayName, Anonymous: t.Anonymous, At: t.At, Question: t.Question,
		Answer: t.Answer, AnswerExcerpt: t.AnswerExcerpt, Outcome: t.Outcome, Rating: t.Rating, Comment: t.Comment,
		Model: t.Model, Provider: t.Provider, LatencyMs: t.LatencyMs, ToolCalls: chatToolCallsToProto(t.ToolCalls),
		LoopStopped: t.LoopStopped}
	if t.Tokens != nil {
		out.PromptTokens, out.CompletionTokens = t.Tokens.Prompt, t.Tokens.Completion
	}
	return out
}
