package main

import (
	"errors"
	"strings"
	"testing"

	"mddb/proto"

	"google.golang.org/grpc/codes"
)

// MCP-005: external MCP servers of the chat over gRPC.

func TestChatMcpGRPCLifecycle(t *testing.T) {
	s := chatServer(t)
	g, admin, user := NewGRPCServer(s), asClaims(root), asClaims(alice)
	if _, err := g.InspectChatMcpServer(admin, &proto.InspectChatMcpServerRequest{Url: "https://a"}); grpcCode(err) != codes.FailedPrecondition {
		t.Fatalf("inspect with the module off: %v", err)
	}
	f := newMcpFixture(t)
	externalMcpOn(t, s)
	f.token = "tok"

	put, err := g.PutChatMcpSecret(admin, &proto.PutChatMcpSecretRequest{Name: "wiki", Value: "tok"})
	if err != nil || !put.GetCreated() || !put.GetSecret().GetSet() || put.GetVersion() != 1 {
		t.Fatalf("put: %+v %v", put, err)
	}
	if _, err := g.PutChatMcpSecret(admin, &proto.PutChatMcpSecretRequest{Name: "bad"}); grpcCode(err) != codes.InvalidArgument {
		t.Errorf("an empty value: %v", err)
	}
	list, err := g.ListChatMcpSecrets(admin, &proto.ListChatMcpSecretsRequest{})
	if err != nil || len(list.GetSecrets()) != 1 || list.GetSecrets()[0].GetUpdatedAt() == 0 || strings.Contains(list.String(), "tok") {
		t.Fatalf("list: %+v %v", list, err)
	}
	in, err := g.InspectChatMcpServer(admin, &proto.InspectChatMcpServerRequest{Url: f.URL, SecretRef: "wiki"})
	if err != nil || in.GetServerName() != "wiki" || len(in.GetTools()) != 10 {
		t.Fatalf("inspect: %+v %v", in, err)
	}
	if w := in.GetTools()[7]; w.GetName() != "wipe" || !w.GetReadOnlyHint() || !w.GetDestructiveHintSet() || !w.GetDestructiveHint() || w.GetAllowable() {
		t.Errorf("wipe: %+v", w)
	}
	if p := in.GetTools()[9]; p.GetName() != "plain" || p.GetReadOnlyHintSet() || p.GetReason() == "" {
		t.Errorf("plain: %+v", p)
	}
	for req, code := range map[*proto.InspectChatMcpServerRequest]codes.Code{
		{Url: f.URL}:                     codes.Unavailable,
		{Url: f.URL, SecretRef: "ghost"}: codes.NotFound,
		{Url: "http://wiki.example.com"}: codes.InvalidArgument,
	} {
		if _, err := g.InspectChatMcpServer(admin, req); grpcCode(err) != code {
			t.Errorf("%v: %v", req, err)
		}
	}

	// A scenario with the server, read back with its servers' states.
	sc := chatProtoFixture()
	sc.AllowedCollections = []string{"docs"}
	sc.McpServers = []*proto.ChatMcpServerProto{{Name: "w", Url: f.URL, Enabled: true, SecretRef: "wiki", AllowedTools: []string{"search"}}}
	if _, err := g.PutChatScenario(admin, &proto.PutChatScenarioRequest{Scenario: sc}); err != nil {
		t.Fatal(err)
	}
	got, err := g.GetChatScenario(admin, &proto.GetChatScenarioRequest{Name: sc.GetName()})
	if m := got.GetMcpServers(); err != nil || len(m) != 1 || m[0].GetTimeoutMs() != 8000 || m[0].GetResultMaxChars() != 4000 || m[0].GetSecretRef() != "wiki" {
		t.Fatalf("get: %+v %v", got, err)
	}
	p, err := g.GetChatScenarioPrompt(admin, &proto.GetChatScenarioPromptRequest{Name: sc.GetName()})
	if st := p.GetMcpServers(); err != nil || len(st) != 1 || !st[0].GetActive() || !st[0].GetTools()[0].GetActive() {
		t.Fatalf("prompt: %+v %v", p, err)
	}

	call := &proto.CallChatMcpToolRequest{Scenario: sc.GetName(), Server: "w", Tool: "search", ArgumentsJson: `{"q":"grpc"}`}
	res, err := g.CallChatMcpTool(user, call)
	if err != nil || res.GetContent() != "found grpc" || res.GetIsError() || res.GetServer() != "w" {
		t.Fatalf("call: %+v %v", res, err)
	}
	if res, err := g.CallChatMcpTool(user, &proto.CallChatMcpToolRequest{Scenario: sc.GetName(), Server: "w", Tool: "search"}); err != nil ||
		res.GetContent() != "found <nil>" {
		t.Errorf("no arguments: %+v %v", res, err)
	}
	for req, code := range map[*proto.CallChatMcpToolRequest]codes.Code{
		{Scenario: sc.GetName(), Server: "w", Tool: "search", ArgumentsJson: "[1]"}: codes.InvalidArgument,
		{Scenario: sc.GetName(), Server: "w", Tool: "wipe"}:                         codes.FailedPrecondition,
		{Scenario: sc.GetName(), Server: "ghost", Tool: "search"}:                   codes.NotFound,
		{Scenario: "ghost", Server: "w", Tool: "search"}:                            codes.NotFound,
	} {
		if _, err := g.CallChatMcpTool(user, req); grpcCode(err) != code {
			t.Errorf("%v: %v", req, err)
		}
	}
	if _, err := g.CallChatMcpTool(asClaims(nobby), call); grpcCode(err) != codes.PermissionDenied {
		t.Errorf("a caller who may not read the scenario: %v", err)
	}

	if _, err := g.DeleteChatMcpSecret(admin, &proto.DeleteChatMcpSecretRequest{Name: "wiki"}); grpcCode(err) != codes.FailedPrecondition ||
		!strings.Contains(err.Error(), "support") {
		t.Errorf("delete in use: %v", err)
	}
	if _, err := g.DeleteChatScenario(admin, &proto.DeleteChatScenarioRequest{Name: sc.GetName()}); err != nil {
		t.Fatal(err)
	}
	if res, err := g.DeleteChatMcpSecret(admin, &proto.DeleteChatMcpSecretRequest{Name: "wiki"}); err != nil || !res.GetDeleted() {
		t.Errorf("delete: %+v %v", res, err)
	}
	if _, err := g.DeleteChatMcpSecret(admin, &proto.DeleteChatMcpSecretRequest{Name: "wiki"}); grpcCode(err) != codes.NotFound {
		t.Errorf("delete again: %v", err)
	}

	// Every method needs an administrator, except the call.
	for name, err := range map[string]error{
		"list": func() error { _, err := g.ListChatMcpSecrets(user, &proto.ListChatMcpSecretsRequest{}); return err }(),
		"put": func() error {
			_, err := g.PutChatMcpSecret(user, &proto.PutChatMcpSecretRequest{Name: "x", Value: "v"})
			return err
		}(),
		"delete": func() error {
			_, err := g.DeleteChatMcpSecret(user, &proto.DeleteChatMcpSecretRequest{Name: "x"})
			return err
		}(),
		"inspect": func() error {
			_, err := g.InspectChatMcpServer(user, &proto.InspectChatMcpServerRequest{Url: f.URL})
			return err
		}(),
	} {
		if grpcCode(err) != codes.PermissionDenied {
			t.Errorf("%s as a non-admin: %v", name, err)
		}
	}
}

func TestChatMcpGRPCErrorFallsBackToScenarioCodes(t *testing.T) {
	if grpcCode(chatMcpGRPCError(errChatScenarioNotFound)) != codes.NotFound || grpcCode(chatMcpGRPCError(errors.New("x"))) != codes.InvalidArgument {
		t.Error("chatMcpGRPCError")
	}
}
