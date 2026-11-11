package main

import (
	"context"
	"errors"

	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Chat conversations over gRPC (#405). Each method calls what the REST
// handler calls (chat_conversations_store.go), after the same permission
// check on the chat collection of the caller's tenant: read for List, Get
// and Export, write for the rest. The gate lists them as authorized here
// (access_rules_grpc.go), because only the method knows that collection.

// chatAccess admits a chat RPC: a writable server for a write, and the
// permission on the caller's chat collection.
func (g *GRPCServer) chatAccess(ctx context.Context, write bool) error {
	if write {
		if err := g.checkWritable(); err != nil {
			return err
		}
	}
	err := g.server.authorize(ctx, chatRule(write), []string{chatCollection(ctx)})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnauthorized):
		return status.Error(codes.Unauthenticated, "unauthorized")
	default:
		return status.Error(codes.PermissionDenied, "forbidden")
	}
}

// chatGRPCError maps a chat failure to its status, as chatFail maps it to an
// HTTP one.
func chatGRPCError(err error) error {
	code := codes.Internal
	switch {
	case errors.Is(err, errNotFound):
		code = codes.NotFound
	case errors.Is(err, errChatInvalid):
		code = codes.InvalidArgument
	case errors.Is(err, errChatEnded):
		code = codes.FailedPrecondition
	}
	return grpcError(code, err) // a quota or a read-only collection keeps its own code
}

// chatConversationReply is a conversation as its message, or the error's
// status.
func chatConversationReply(conv *ChatConversation, err error) (*proto.ChatConversationProto, error) {
	if err != nil {
		return nil, chatGRPCError(err)
	}
	return chatConversationToProto(conv.ChatConversationSummary, conv.Messages), nil
}

// CreateChatConversation creates a conversation, as POST /v1/chat/conversations.
func (g *GRPCServer) CreateChatConversation(ctx context.Context, req *proto.CreateChatConversationRequest) (*proto.ChatConversationProto, error) {
	if err := g.chatAccess(ctx, true); err != nil {
		return nil, err
	}
	return chatConversationReply(g.server.chatCreate(ctx, ChatCreateRequest{Sub: req.GetSub(), Scenario: req.GetScenario(),
		DisplayName: req.GetDisplayName(), RetentionDays: int(req.GetRetentionDays())}))
}

// ListChatConversations lists a user's conversations, as GET /v1/chat/conversations.
func (g *GRPCServer) ListChatConversations(ctx context.Context, req *proto.ListChatConversationsRequest) (*proto.ListChatConversationsResponse, error) {
	if err := g.chatAccess(ctx, false); err != nil {
		return nil, err
	}
	page, err := g.server.chatList(ctx, req.GetSub(), req.GetScenario(), int(req.GetLimit()), req.GetCursor())
	if err != nil {
		return nil, chatGRPCError(err)
	}
	out := &proto.ListChatConversationsResponse{NextCursor: page.NextCursor,
		Conversations: make([]*proto.ChatConversationProto, 0, len(page.Conversations))}
	for _, c := range page.Conversations {
		out.Conversations = append(out.Conversations, chatConversationToProto(c, nil))
	}
	return out, nil
}

// GetChatConversation reads a conversation, as GET /v1/chat/conversations/{id}.
func (g *GRPCServer) GetChatConversation(ctx context.Context, req *proto.GetChatConversationRequest) (*proto.ChatConversationProto, error) {
	if err := g.chatAccess(ctx, false); err != nil {
		return nil, err
	}
	return chatConversationReply(g.server.chatGet(ctx, req.GetId(), req.GetSub(), int(req.GetLimit())))
}

// AppendChatMessages appends messages, as POST /v1/chat/conversations/{id}/messages.
func (g *GRPCServer) AppendChatMessages(ctx context.Context, req *proto.AppendChatMessagesRequest) (*proto.ChatConversationProto, error) {
	if err := g.chatAccess(ctx, true); err != nil {
		return nil, err
	}
	msgs := make([]ChatMessage, 0, len(req.GetMessages()))
	for _, m := range req.GetMessages() {
		msgs = append(msgs, chatMessageFromProto(m))
	}
	return chatConversationReply(g.server.chatAppend(ctx, req.GetId(), ChatAppendRequest{Sub: req.GetSub(), Messages: msgs, Redact: req.GetRedact()}))
}

// EndChatConversation ends a conversation, as POST /v1/chat/conversations/{id}/end.
func (g *GRPCServer) EndChatConversation(ctx context.Context, req *proto.EndChatConversationRequest) (*proto.ChatConversationProto, error) {
	if err := g.chatAccess(ctx, true); err != nil {
		return nil, err
	}
	return chatConversationReply(g.server.chatEnd(ctx, req.GetId(), req.GetSub()))
}

// DeleteChatConversation deletes a conversation, as DELETE /v1/chat/conversations/{id}.
func (g *GRPCServer) DeleteChatConversation(ctx context.Context, req *proto.DeleteChatConversationRequest) (*proto.DeleteChatConversationResponse, error) {
	if err := g.chatAccess(ctx, true); err != nil {
		return nil, err
	}
	if err := g.server.chatDelete(ctx, req.GetId(), req.GetSub()); err != nil {
		return nil, chatGRPCError(err)
	}
	return &proto.DeleteChatConversationResponse{Deleted: true, Id: req.GetId()}, nil
}

// DeleteChatConversationsForUser deletes everything of a user, as
// DELETE /v1/chat/conversations?sub=.
func (g *GRPCServer) DeleteChatConversationsForUser(ctx context.Context, req *proto.DeleteChatConversationsForUserRequest) (*proto.DeleteChatConversationsForUserResponse, error) {
	if err := g.chatAccess(ctx, true); err != nil {
		return nil, err
	}
	n, err := g.server.chatDeleteForUser(ctx, req.GetSub())
	if err != nil {
		return nil, chatGRPCError(err)
	}
	return &proto.DeleteChatConversationsForUserResponse{Deleted: safeInt32(n), Sub: req.GetSub()}, nil
}

// ExportChatConversations streams everything of a user, one conversation per
// message, as GET /v1/chat/conversations/export.
func (g *GRPCServer) ExportChatConversations(req *proto.ExportChatConversationsRequest, stream proto.MDDB_ExportChatConversationsServer) error {
	ctx := stream.Context()
	if err := g.chatAccess(ctx, false); err != nil {
		return err
	}
	export, err := g.server.chatExport(ctx, req.GetSub())
	if err != nil {
		return chatGRPCError(err)
	}
	for _, c := range export.Conversations {
		if err := stream.Send(chatConversationToProto(c.ChatConversationSummary, c.Messages)); err != nil {
			return err
		}
	}
	return nil
}

// chatConversationToProto is a conversation and the messages to send with it.
func chatConversationToProto(c ChatConversationSummary, msgs []ChatMessage) *proto.ChatConversationProto {
	out := &proto.ChatConversationProto{
		Id: c.ID, Scenario: c.Scenario, Sub: c.Sub, DisplayName: c.DisplayName, Title: c.Title,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, LastMessageAt: c.LastMessageAt, Ended: c.Ended,
		RetentionDays: safeInt32(c.RetentionDays), ExpiresAt: c.ExpiresAt, MessageCount: safeInt32(c.MessageCount),
		Messages: make([]*proto.ChatMessageProto, 0, len(msgs)), Redact: c.Redact,
	}
	for _, m := range msgs {
		pm := &proto.ChatMessageProto{Id: m.ID, Role: m.Role, Content: m.Content, Model: m.Model, Provider: m.Provider,
			CreatedAt: m.CreatedAt, Outcome: m.Outcome, LatencyMs: m.LatencyMs, ToolCalls: chatToolCallsToProto(m.ToolCalls)}
		if m.Tokens != nil {
			pm.PromptTokens, pm.CompletionTokens = m.Tokens.Prompt, m.Tokens.Completion
		}
		if f := m.Feedback; f != nil {
			pm.Feedback = &proto.ChatFeedbackProto{Rating: f.Rating, Comment: f.Comment, At: f.At}
		}
		out.Messages = append(out.Messages, pm)
	}
	return out
}

// chatToolCallsToProto is the tool calls of a message as their messages.
func chatToolCallsToProto(calls []ChatToolCall) []*proto.ChatToolCallProto {
	out := make([]*proto.ChatToolCallProto, 0, len(calls))
	for _, tc := range calls {
		out = append(out, &proto.ChatToolCallProto{Name: tc.Name, Collection: tc.Collection, DurationMs: tc.DurationMs,
			Query: tc.Query, ResultKeys: tc.ResultKeys, TopScore: tc.TopScore})
	}
	return out
}

// chatMessageFromProto is the REST body of one appended message.
func chatMessageFromProto(m *proto.ChatMessageProto) ChatMessage {
	out := ChatMessage{ID: m.GetId(), Role: m.GetRole(), Content: m.GetContent(), Model: m.GetModel(), Provider: m.GetProvider(),
		CreatedAt: m.GetCreatedAt(), Outcome: m.GetOutcome(), LatencyMs: m.GetLatencyMs()}
	if m.GetPromptTokens() != 0 || m.GetCompletionTokens() != 0 {
		out.Tokens = &ChatTokens{Prompt: m.GetPromptTokens(), Completion: m.GetCompletionTokens()}
	}
	for _, tc := range m.GetToolCalls() {
		out.ToolCalls = append(out.ToolCalls, ChatToolCall{Name: tc.GetName(), Collection: tc.GetCollection(), DurationMs: tc.GetDurationMs(),
			Query: tc.GetQuery(), ResultKeys: tc.GetResultKeys(), TopScore: tc.GetTopScore()})
	}
	return out
}
