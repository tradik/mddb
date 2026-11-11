package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Chat conversations over gRPC (#405): the REST behaviour on the same store.

// chatExportStream collects what ExportChatConversations sends.
type chatExportStream struct {
	grpc.ServerStream
	ctx     context.Context
	sent    []*proto.ChatConversationProto
	sendErr error
}

func (c *chatExportStream) Context() context.Context { return c.ctx }

func (c *chatExportStream) Send(m *proto.ChatConversationProto) error {
	if c.sendErr != nil {
		return c.sendErr
	}
	c.sent = append(c.sent, m)
	return nil
}

func chatGRPC(t *testing.T) (*GRPCServer, *Server) {
	t.Helper()
	s := chatServer(t)
	return NewGRPCServer(s), s
}

func TestGRPCChatConversationLifecycle(t *testing.T) {
	g, _ := chatGRPC(t)
	ctx := context.Background()
	conv, err := g.CreateChatConversation(ctx, &proto.CreateChatConversationRequest{Sub: "user-1", Scenario: "support", DisplayName: "Ada", RetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if conv.GetId() == "" || conv.GetSub() != "user-1" || conv.GetScenario() != "support" || conv.GetDisplayName() != "Ada" ||
		conv.GetRetentionDays() != 7 || conv.GetExpiresAt() != conv.GetCreatedAt()+7*secondsPerDay {
		t.Errorf("created: %v", conv)
	}
	added, err := g.AppendChatMessages(ctx, &proto.AppendChatMessagesRequest{Id: conv.GetId(), Sub: "user-1", Messages: []*proto.ChatMessageProto{
		{Role: "user", Content: "How do I rotate the key?", CreatedAt: 1000},
		{Role: "assistant", Content: "Call rotate.", Model: "m-1", Provider: "p-1", PromptTokens: 120, CompletionTokens: 15, CreatedAt: 1001,
			ToolCalls: []*proto.ChatToolCallProto{{Name: "search", Collection: "docs", DurationMs: 42}}},
	}})
	if err != nil || added.GetMessageCount() != 2 || len(added.GetMessages()) != 2 || added.GetTitle() != "How do I rotate the key?" || added.GetLastMessageAt() != 1001 {
		t.Fatalf("append: %v %v", added, err)
	}
	got, err := g.GetChatConversation(ctx, &proto.GetChatConversationRequest{Id: conv.GetId(), Sub: "user-1", Limit: 1})
	if err != nil || len(got.GetMessages()) != 1 || got.GetMessageCount() != 2 {
		t.Fatalf("get limit 1: %v %v", got, err)
	}
	m := got.GetMessages()[0]
	if m.GetId() == "" || m.GetRole() != "assistant" || m.GetModel() != "m-1" || m.GetProvider() != "p-1" || m.GetPromptTokens() != 120 ||
		m.GetCompletionTokens() != 15 || m.GetToolCalls()[0].GetName() != "search" || m.GetToolCalls()[0].GetCollection() != "docs" ||
		m.GetToolCalls()[0].GetDurationMs() != 42 {
		t.Errorf("stored message: %v", m)
	}
	list, err := g.ListChatConversations(ctx, &proto.ListChatConversationsRequest{Sub: "user-1", Scenario: "support", Limit: 10})
	if err != nil || len(list.GetConversations()) != 1 || len(list.GetConversations()[0].GetMessages()) != 0 || list.GetConversations()[0].GetMessageCount() != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	stream := &chatExportStream{ctx: ctx}
	if err := g.ExportChatConversations(&proto.ExportChatConversationsRequest{Sub: "user-1"}, stream); err != nil || len(stream.sent) != 1 || len(stream.sent[0].GetMessages()) != 2 {
		t.Fatalf("export: %v %v", stream.sent, err)
	}
	if err := g.ExportChatConversations(&proto.ExportChatConversationsRequest{Sub: "user-1"}, &chatExportStream{ctx: ctx, sendErr: errors.New("client went away")}); err == nil {
		t.Error("export ignored a failed send")
	}
	ended, err := g.EndChatConversation(ctx, &proto.EndChatConversationRequest{Id: conv.GetId(), Sub: "user-1"})
	if err != nil || !ended.GetEnded() {
		t.Fatalf("end: %v %v", ended, err)
	}
	_, err = g.AppendChatMessages(ctx, &proto.AppendChatMessagesRequest{Id: conv.GetId(), Sub: "user-1", Messages: []*proto.ChatMessageProto{{Role: "user", Content: "more"}}})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("append to an ended conversation: %v, want FAILED_PRECONDITION", err)
	}
	del, err := g.DeleteChatConversation(ctx, &proto.DeleteChatConversationRequest{Id: conv.GetId(), Sub: "user-1"})
	if err != nil || !del.GetDeleted() || del.GetId() != conv.GetId() {
		t.Fatalf("delete: %v %v", del, err)
	}
	second, _ := g.CreateChatConversation(ctx, &proto.CreateChatConversationRequest{Sub: "user-1", Scenario: "sales"})
	wiped, err := g.DeleteChatConversationsForUser(ctx, &proto.DeleteChatConversationsForUserRequest{Sub: "user-1"})
	if err != nil || wiped.GetDeleted() != 1 || wiped.GetSub() != "user-1" {
		t.Fatalf("delete for user: %v %v", wiped, err)
	}
	if _, err := g.GetChatConversation(ctx, &proto.GetChatConversationRequest{Id: second.GetId(), Sub: "user-1"}); status.Code(err) != codes.NotFound {
		t.Errorf("get after erasure: %v", err)
	}
}

// Every RPC answers NOT_FOUND for another sub's conversation and
// INVALID_ARGUMENT without a sub.
func TestGRPCChatConversationErrors(t *testing.T) {
	g, _ := chatGRPC(t)
	ctx := context.Background()
	conv, _ := g.CreateChatConversation(ctx, &proto.CreateChatConversationRequest{Sub: "user-1", Scenario: "support"})
	id := conv.GetId()
	msg := []*proto.ChatMessageProto{{Role: "user", Content: "x"}}
	call := func(sub string) map[string]error {
		out := map[string]error{}
		_, out["Get"] = g.GetChatConversation(ctx, &proto.GetChatConversationRequest{Id: id, Sub: sub})
		_, out["Append"] = g.AppendChatMessages(ctx, &proto.AppendChatMessagesRequest{Id: id, Sub: sub, Messages: msg})
		_, out["End"] = g.EndChatConversation(ctx, &proto.EndChatConversationRequest{Id: id, Sub: sub})
		_, out["Delete"] = g.DeleteChatConversation(ctx, &proto.DeleteChatConversationRequest{Id: id, Sub: sub})
		return out
	}
	for name, err := range call("user-2") {
		if status.Code(err) != codes.NotFound {
			t.Errorf("%s as another sub: %v, want NOT_FOUND", name, err)
		}
	}
	missing := call("")
	_, missing["Create"] = g.CreateChatConversation(ctx, &proto.CreateChatConversationRequest{Scenario: "support"})
	_, missing["List"] = g.ListChatConversations(ctx, &proto.ListChatConversationsRequest{})
	_, missing["DeleteForUser"] = g.DeleteChatConversationsForUser(ctx, &proto.DeleteChatConversationsForUserRequest{})
	missing["Export"] = g.ExportChatConversations(&proto.ExportChatConversationsRequest{}, &chatExportStream{ctx: ctx})
	for name, err := range missing {
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s without a sub: %v, want INVALID_ARGUMENT", name, err)
		}
	}
	if code := status.Code(chatGRPCError(errors.New("disk on fire"))); code != codes.Internal {
		t.Errorf("an unknown failure: %v, want INTERNAL", code)
	}
	if code := status.Code(chatGRPCError(&ReadOnlyError{Collection: chatConversationsCollection})); code != codes.PermissionDenied {
		t.Errorf("a read-only collection: %v, want PERMISSION_DENIED", code)
	}
}

// The permission is the REST one: the chat collection of the caller's
// tenant, read for the reads and write for the rest; a read-only server
// refuses the writes.
func TestGRPCChatConversationPermissions(t *testing.T) {
	s := accessServer(t)
	g := NewGRPCServer(s)
	if err := s.AuthManager.SetPermission(&Permission{Username: "alice", Collection: chatConversationsCollection, Read: true}); err != nil {
		t.Fatal(err)
	}
	as := func(c *JWTClaims) context.Context { return context.WithValue(context.Background(), authContextKey, c) }
	conv, err := g.CreateChatConversation(as(root), &proto.CreateChatConversationRequest{Sub: "user-1", Scenario: "support"})
	if err != nil {
		t.Fatal(err)
	}
	id := conv.GetId()

	writes := func(ctx context.Context) map[string]error {
		out := map[string]error{}
		_, out["Create"] = g.CreateChatConversation(ctx, &proto.CreateChatConversationRequest{Sub: "user-1", Scenario: "support"})
		_, out["Append"] = g.AppendChatMessages(ctx, &proto.AppendChatMessagesRequest{Id: id, Sub: "user-1", Messages: []*proto.ChatMessageProto{{Role: "user", Content: "x"}}})
		_, out["End"] = g.EndChatConversation(ctx, &proto.EndChatConversationRequest{Id: id, Sub: "user-1"})
		_, out["Delete"] = g.DeleteChatConversation(ctx, &proto.DeleteChatConversationRequest{Id: id, Sub: "user-1"})
		_, out["DeleteForUser"] = g.DeleteChatConversationsForUser(ctx, &proto.DeleteChatConversationsForUserRequest{Sub: "user-1"})
		return out
	}
	reads := func(ctx context.Context) map[string]error {
		out := map[string]error{}
		_, out["List"] = g.ListChatConversations(ctx, &proto.ListChatConversationsRequest{Sub: "user-1"})
		_, out["Get"] = g.GetChatConversation(ctx, &proto.GetChatConversationRequest{Id: id, Sub: "user-1"})
		out["Export"] = g.ExportChatConversations(&proto.ExportChatConversationsRequest{Sub: "user-1"}, &chatExportStream{ctx: ctx})
		return out
	}
	for name, err := range writes(as(alice)) { // alice reads the chat collection only
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s as a reader: %v, want PERMISSION_DENIED", name, err)
		}
	}
	for name, err := range reads(as(alice)) {
		if err != nil {
			t.Errorf("%s as a reader: %v", name, err)
		}
	}
	for name, err := range reads(context.Background()) {
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s without credentials: %v, want UNAUTHENTICATED", name, err)
		}
	}
	// A tenant has its own store: the global conversation is not there.
	if _, err := g.GetChatConversation(as(tina), &proto.GetChatConversationRequest{Id: id, Sub: "user-1"}); status.Code(err) != codes.NotFound {
		t.Errorf("tenant reads a global conversation: %v, want NOT_FOUND", err)
	}
	s.Mode = ModeRead
	for name, err := range writes(as(root)) {
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s on a read-only server: %v, want PERMISSION_DENIED", name, err)
		}
	}
	for name, err := range reads(as(root)) {
		if err != nil {
			t.Errorf("%s on a read-only server: %v", name, err)
		}
	}
}

// Parity (#405): a conversation written over one transport is the same
// conversation over the other, field for field.
func TestGRPCChatConversationParityWithREST(t *testing.T) {
	g, s := chatGRPC(t)
	ctx := context.Background()

	// REST writes, gRPC reads.
	id := chatNew(t, s, nil, "user-1", "support")
	body := `{"sub":"user-1","messages":[{"role":"user","content":"Cześć, zażółć gęślą jaźń","createdAt":2000},
		{"role":"assistant","content":"ok","model":"m","provider":"p","createdAt":2001,"tokens":{"prompt":3,"completion":4},
		 "toolCalls":[{"name":"fts","collection":"docs","durationMs":5}]}]}`
	if code, out := chatCall(t, s, nil, http.MethodPost, chatConversationsPath+"/"+id+"/messages", body); code != http.StatusOK {
		t.Fatalf("REST append: %d %v", code, out)
	}
	overGRPC, err := g.GetChatConversation(ctx, &proto.GetChatConversationRequest{Id: id, Sub: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, overREST := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+id+"?sub=user-1", "")
	restMsgs := overREST["messages"].([]any)
	if overGRPC.GetTitle() != overREST["title"] || float64(overGRPC.GetLastMessageAt()) != overREST["lastMessageAt"] ||
		float64(overGRPC.GetExpiresAt()) != overREST["expiresAt"] || float64(overGRPC.GetMessageCount()) != overREST["messageCount"] ||
		len(overGRPC.GetMessages()) != len(restMsgs) {
		t.Fatalf("summary differs:\n gRPC %v\n REST %v", overGRPC, overREST)
	}
	for i, m := range overGRPC.GetMessages() {
		r := restMsgs[i].(map[string]any)
		if m.GetId() != r["id"] || m.GetRole() != r["role"] || m.GetContent() != r["content"] || float64(m.GetCreatedAt()) != r["createdAt"] {
			t.Errorf("message %d differs:\n gRPC %v\n REST %v", i, m, r)
		}
	}
	a := overGRPC.GetMessages()[1]
	if a.GetPromptTokens() != 3 || a.GetCompletionTokens() != 4 || a.GetToolCalls()[0].GetName() != "fts" {
		t.Errorf("assistant message over gRPC: %v", a)
	}

	// gRPC writes, REST reads.
	conv, err := g.CreateChatConversation(ctx, &proto.CreateChatConversationRequest{Sub: "user-9", Scenario: "sales", RetentionDays: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AppendChatMessages(ctx, &proto.AppendChatMessagesRequest{Id: conv.GetId(), Sub: "user-9", Messages: []*proto.ChatMessageProto{
		{Role: "assistant", ToolCalls: []*proto.ChatToolCallProto{{Name: "search"}}, CompletionTokens: 9, CreatedAt: 3000}}}); err != nil {
		t.Fatal(err)
	}
	code, got := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"/"+conv.GetId()+"?sub=user-9", "")
	if code != http.StatusOK || got["scenario"] != "sales" || got["retentionDays"] != float64(3) || got["lastMessageAt"] != float64(3000) {
		t.Fatalf("REST read of a gRPC conversation: %d %v", code, got)
	}
	m := got["messages"].([]any)[0].(map[string]any)
	if m["tokens"].(map[string]any)["completion"] != float64(9) || m["toolCalls"].([]any)[0].(map[string]any)["name"] != "search" {
		t.Errorf("REST read of a gRPC message: %v", m)
	}
	// Both list the same thing, and the cursor of one works on the other.
	chatNew(t, s, nil, "user-9", "sales")
	page, err := g.ListChatConversations(ctx, &proto.ListChatConversationsRequest{Sub: "user-9", Limit: 1})
	if err != nil || page.GetNextCursor() == "" {
		t.Fatalf("gRPC page: %v %v", page, err)
	}
	_, restPage := chatCall(t, s, nil, http.MethodGet, chatConversationsPath+"?sub=user-9&limit=1", "")
	if restPage["nextCursor"] != page.GetNextCursor() || restPage["conversations"].([]any)[0].(map[string]any)["id"] != page.GetConversations()[0].GetId() {
		t.Errorf("pages differ:\n gRPC %v\n REST %v", page, restPage)
	}
}
