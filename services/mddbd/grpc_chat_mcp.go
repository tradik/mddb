package main

import (
	"context"
	"errors"

	json "mddb/internal/jsonx"
	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// External MCP servers of the chat over gRPC (MCP-005). Each method calls
// what the REST handler calls (chat_mcp_handlers.go), and the access gate
// applies the REST permissions (access_rules_grpc.go).

// ListChatMcpSecrets lists the secrets, as GET /v1/chat/mcp-secrets.
func (g *GRPCServer) ListChatMcpSecrets(ctx context.Context, _ *proto.ListChatMcpSecretsRequest) (*proto.ListChatMcpSecretsResponse, error) {
	if err := g.requireAdminRPC(ctx); err != nil {
		return nil, err
	}
	list := g.server.ListChatMcpSecrets()
	out := &proto.ListChatMcpSecretsResponse{Secrets: make([]*proto.ChatMcpSecretProto, 0, len(list.Secrets)),
		EncryptionReady: list.EncryptionReady}
	for _, v := range list.Secrets {
		out.Secrets = append(out.Secrets, &proto.ChatMcpSecretProto{Name: v.Name, Header: v.Header, Set: v.Set,
			UpdatedAt: chatUnix(v.UpdatedAt), UsedBy: v.UsedBy})
	}
	return out, nil
}

// PutChatMcpSecret sets a secret, as PUT /v1/chat/mcp-secrets/{name}.
func (g *GRPCServer) PutChatMcpSecret(ctx context.Context, req *proto.PutChatMcpSecretRequest) (*proto.PutChatMcpSecretResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil { // a writable server and an administrator
		return nil, err
	}
	created, err := g.server.PutChatMcpSecret(req.GetName(), req.GetHeader(), req.GetValue())
	if err != nil {
		return nil, chatMcpGRPCError(err)
	}
	return &proto.PutChatMcpSecretResponse{Secret: &proto.ChatMcpSecretProto{Name: req.GetName(), Header: req.GetHeader(), Set: true},
		Created: created, Version: g.server.ChatScenariosVersion()}, nil
}

// DeleteChatMcpSecret removes a secret, as DELETE /v1/chat/mcp-secrets/{name}.
func (g *GRPCServer) DeleteChatMcpSecret(ctx context.Context, req *proto.DeleteChatMcpSecretRequest) (*proto.DeleteChatMcpSecretResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil {
		return nil, err
	}
	usedBy, err := g.server.DeleteChatMcpSecret(req.GetName())
	if errors.Is(err, errChatMcpSecretInUse) {
		return nil, status.Error(codes.FailedPrecondition, chatMcpSecretInUseMessage(req.GetName(), usedBy))
	}
	if err != nil {
		return nil, chatMcpGRPCError(err)
	}
	return &proto.DeleteChatMcpSecretResponse{Deleted: true, Version: g.server.ChatScenariosVersion()}, nil
}

// InspectChatMcpServer lists a server's tools, as POST /v1/chat/mcp/inspect.
func (g *GRPCServer) InspectChatMcpServer(ctx context.Context, req *proto.InspectChatMcpServerRequest) (*proto.InspectChatMcpServerResponse, error) {
	if err := g.requireAdminRPC(ctx); err != nil {
		return nil, err
	}
	in, err := g.server.InspectChatMcpServer(ctx, req.GetUrl(), req.GetSecretRef())
	if err != nil {
		return nil, chatMcpGRPCError(err)
	}
	out := &proto.InspectChatMcpServerResponse{Url: in.URL, ServerName: in.ServerName, ServerVersion: in.ServerVersion,
		ProtocolVersion: in.ProtocolVersion, Tools: make([]*proto.ChatMcpToolProto, 0, len(in.Tools))}
	for _, t := range in.Tools {
		a := t.Annotations
		out.Tools = append(out.Tools, &proto.ChatMcpToolProto{Name: t.Name, Title: t.Title, Description: t.Description,
			ReadOnlyHint: a.ReadOnlyHint != nil && *a.ReadOnlyHint, ReadOnlyHintSet: a.ReadOnlyHint != nil,
			DestructiveHint: a.DestructiveHint != nil && *a.DestructiveHint, DestructiveHintSet: a.DestructiveHint != nil,
			Allowable: t.Allowable, Reason: t.Reason})
	}
	return out, nil
}

// CallChatMcpTool calls an allowed tool, as POST /v1/chat/mcp/call.
func (g *GRPCServer) CallChatMcpTool(ctx context.Context, req *proto.CallChatMcpToolRequest) (*proto.CallChatMcpToolResponse, error) {
	call := &chatMcpCallRequest{Scenario: req.GetScenario(), Server: req.GetServer(), Tool: req.GetTool()}
	if raw := req.GetArgumentsJson(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &call.Arguments); err != nil {
			return nil, status.Error(codes.InvalidArgument, "arguments_json must be a JSON object: "+err.Error())
		}
	}
	res, err := g.server.CallChatMcpTool(ctx, call)
	if err != nil {
		return nil, chatMcpGRPCError(err)
	}
	return &proto.CallChatMcpToolResponse{Scenario: res.Scenario, Server: res.Server, Tool: res.Tool, Content: res.Content,
		IsError: res.IsError, Truncated: res.Truncated, DurationMs: res.DurationMs}, nil
}

// chatMcpGRPCError maps a failure to its status, as chatMcpAnswer maps it to
// an HTTP one.
func chatMcpGRPCError(err error) error {
	var code codes.Code
	switch {
	case errors.As(err, new(featureDisabledError)), errors.Is(err, errChatMcpNotAllowed),
		errors.Is(err, errChatMcpSecretsNeedKey), errors.Is(err, errChatMcpSecretUnsealed):
		code = codes.FailedPrecondition
	case errors.Is(err, errChatMcpSecretNotFound), errors.Is(err, errChatMcpServerNotFound):
		code = codes.NotFound
	case errors.Is(err, errChatMcpUnreachable):
		code = codes.Unavailable
	default:
		return chatScenarioGRPCError(err)
	}
	return status.Error(code, err.Error())
}

// chatMcpServersToProto and chatMcpServersFromProto carry a scenario's
// servers on the wire.
func chatMcpServersToProto(servers []ChatMcpServer) []*proto.ChatMcpServerProto {
	out := make([]*proto.ChatMcpServerProto, 0, len(servers))
	for _, m := range servers {
		out = append(out, &proto.ChatMcpServerProto{Name: m.Name, Url: m.URL, Enabled: m.Enabled, SecretRef: m.SecretRef,
			AllowedTools: m.AllowedTools, TimeoutMs: safeInt32(m.TimeoutMs), ResultMaxChars: safeInt32(m.ResultMaxChars)})
	}
	return out
}

func chatMcpServersFromProto(servers []*proto.ChatMcpServerProto) []ChatMcpServer {
	var out []ChatMcpServer
	for _, p := range servers {
		out = append(out, ChatMcpServer{Name: p.GetName(), URL: p.GetUrl(), Enabled: p.GetEnabled(), SecretRef: p.GetSecretRef(),
			AllowedTools: p.GetAllowedTools(), TimeoutMs: int(p.GetTimeoutMs()), ResultMaxChars: int(p.GetResultMaxChars())})
	}
	return out
}

// chatMcpStatesToProto carries the servers' states of a composed prompt.
func chatMcpStatesToProto(states []chatMcpServerState) []*proto.ChatMcpServerStateProto {
	out := make([]*proto.ChatMcpServerStateProto, 0, len(states))
	for _, st := range states {
		p := &proto.ChatMcpServerStateProto{Name: st.Name, Url: st.URL, Enabled: st.Enabled, Active: st.Active, Reason: st.Reason}
		for _, t := range st.Tools {
			p.Tools = append(p.Tools, &proto.ChatMcpToolStateProto{Name: t.Name, Active: t.Active, Reason: t.Reason})
		}
		out = append(out, p)
	}
	return out
}
