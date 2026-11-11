package main

import (
	"context"
	"errors"
	"time"

	json "mddb/internal/jsonx"
	"mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Chat scenarios over gRPC (#403). Each method calls what the REST handler
// calls — ListChatScenarios, GetChatScenario, PutChatScenario,
// DeleteChatScenario, ChatScenarioPrompt — so the surfaces answer alike, and
// the access gate applies the REST permissions (access_rules_grpc.go).

// ListChatScenarios lists the scenarios the caller may read, as
// GET /v1/chat/scenarios.
func (g *GRPCServer) ListChatScenarios(ctx context.Context, _ *proto.ListChatScenariosRequest) (*proto.ListChatScenariosResponse, error) {
	list := g.server.ListChatScenarios(ctx)
	out := &proto.ListChatScenariosResponse{Scenarios: make([]*proto.ChatScenarioProto, 0, len(list)), Version: g.server.ChatScenariosVersion()}
	for _, sc := range list {
		out.Scenarios = append(out.Scenarios, chatScenarioToProto(sc))
	}
	return out, nil
}

// GetChatScenario returns one scenario, as GET /v1/chat/scenarios/{name}.
func (g *GRPCServer) GetChatScenario(ctx context.Context, req *proto.GetChatScenarioRequest) (*proto.ChatScenarioProto, error) {
	sc, err := g.server.GetChatScenario(ctx, req.GetName())
	if err != nil {
		return nil, chatScenarioGRPCError(err)
	}
	return chatScenarioToProto(sc), nil
}

// PutChatScenario creates or replaces a scenario, as
// PUT /v1/chat/scenarios/{name}.
func (g *GRPCServer) PutChatScenario(ctx context.Context, req *proto.PutChatScenarioRequest) (*proto.PutChatScenarioResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil { // a writable server and an administrator
		return nil, err
	}
	sc, err := chatScenarioFromProto(req.GetScenario())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	created, err := g.server.PutChatScenario(ctx, sc)
	if err != nil {
		return nil, chatScenarioGRPCError(err)
	}
	return &proto.PutChatScenarioResponse{Scenario: chatScenarioToProto(sc), Created: created, Version: g.server.ChatScenariosVersion()}, nil
}

// DeleteChatScenario removes a scenario, as DELETE /v1/chat/scenarios/{name}.
func (g *GRPCServer) DeleteChatScenario(ctx context.Context, req *proto.DeleteChatScenarioRequest) (*proto.DeleteChatScenarioResponse, error) {
	if err := g.corpusAdmin(ctx); err != nil {
		return nil, err
	}
	if err := g.server.DeleteChatScenario(req.GetName()); err != nil {
		return nil, chatScenarioGRPCError(err)
	}
	return &proto.DeleteChatScenarioResponse{Deleted: true, Version: g.server.ChatScenariosVersion()}, nil
}

// GetChatScenarioPrompt returns the composed system prompt, as
// GET /v1/chat/scenarios/{name}/prompt.
func (g *GRPCServer) GetChatScenarioPrompt(ctx context.Context, req *proto.GetChatScenarioPromptRequest) (*proto.GetChatScenarioPromptResponse, error) {
	sc, err := g.server.GetChatScenario(ctx, req.GetName())
	if err != nil {
		return nil, chatScenarioGRPCError(err)
	}
	p := g.server.ChatScenarioPrompt(sc)
	return &proto.GetChatScenarioPromptResponse{Name: p.Name, Prompt: p.Prompt, Version: p.Version, Bytes: int64(p.Bytes),
		ApproxTokens: int64(p.ApproxTokens), Warning: p.Warning, MissingSkills: p.MissingSkills,
		McpServers: chatMcpStatesToProto(p.McpServers)}, nil
}

// chatScenarioGRPCError maps a failure to its status, as chatScenarioError
// maps it to an HTTP one.
func chatScenarioGRPCError(err error) error {
	code := codes.InvalidArgument
	switch {
	case errors.Is(err, errChatScenarioNotFound):
		code = codes.NotFound
	case errors.Is(err, ErrForbidden):
		code = codes.PermissionDenied
	case errors.Is(err, ErrUnauthorized):
		code = codes.Unauthenticated
	}
	return status.Error(code, err.Error())
}

// chatScenarioToProto is the scenario on the wire.
func chatScenarioToProto(sc *ChatScenario) *proto.ChatScenarioProto {
	llm := &proto.ChatLLMProto{Model: sc.LLM.Model, MaxTokens: safeInt32(sc.LLM.MaxTokens)}
	if t := sc.LLM.Temperature; t != nil {
		llm.Temperature, llm.TemperatureSet = *t, true
	}
	if len(sc.LLM.ExtraBody) > 0 {
		b, _ := json.Marshal(sc.LLM.ExtraBody) // stored as JSON, so it encodes
		llm.ExtraBodyJson = string(b)
	}
	return &proto.ChatScenarioProto{
		Name: sc.Name, Description: sc.Description, Enabled: sc.Enabled,
		OperatorPrompt: sc.OperatorPrompt, Guardrails: sc.Guardrails,
		OffTopic:           &proto.ChatOffTopicProto{Mode: sc.OffTopic.Mode, RedirectTemplate: sc.OffTopic.RedirectTemplate},
		AllowedCollections: sc.AllowedCollections, Skills: sc.Skills,
		Style: &proto.ChatStyleProto{Emoji: sc.Style.Emoji, Verbosity: sc.Style.Verbosity, Markdown: sc.Style.Markdown,
			CitationTemplate: sc.Style.CitationTemplate},
		Llm: llm, MaxTurns: safeInt32(sc.MaxTurns), TokenBudgetPerSession: int64(sc.TokenBudgetPerSession),
		RateLimit: &proto.ChatRateLimitProto{MessagesPerMinute: safeInt32(sc.RateLimit.MessagesPerMinute)},
		CreatedAt: chatUnix(sc.CreatedAt), UpdatedAt: chatUnix(sc.UpdatedAt), McpServers: chatMcpServersToProto(sc.McpServers),
		Logging: &proto.ChatLoggingProto{Enabled: sc.Logging.Enabled, RetentionDays: safeInt32(sc.Logging.RetentionDays),
			Redact: sc.Logging.Redact},
	}
}

// chatUnix is a time in Unix seconds, 0 for the zero time.
func chatUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// chatScenarioFromProto reads a scenario sent by a client. The timestamps
// are the store's to set.
func chatScenarioFromProto(p *proto.ChatScenarioProto) (*ChatScenario, error) {
	if p == nil {
		return nil, errors.New("scenario is required")
	}
	sc := &ChatScenario{
		Name: p.GetName(), Description: p.GetDescription(), Enabled: p.GetEnabled(),
		OperatorPrompt: p.GetOperatorPrompt(), Guardrails: p.GetGuardrails(),
		OffTopic:           ChatOffTopic{Mode: p.GetOffTopic().GetMode(), RedirectTemplate: p.GetOffTopic().GetRedirectTemplate()},
		AllowedCollections: p.GetAllowedCollections(), Skills: p.GetSkills(),
		Style: ChatStyle{Emoji: p.GetStyle().GetEmoji(), Verbosity: p.GetStyle().GetVerbosity(), Markdown: p.GetStyle().GetMarkdown(),
			CitationTemplate: p.GetStyle().GetCitationTemplate()},
		LLM:      ChatLLM{Model: p.GetLlm().GetModel(), MaxTokens: int(p.GetLlm().GetMaxTokens())},
		MaxTurns: int(p.GetMaxTurns()), TokenBudgetPerSession: int(p.GetTokenBudgetPerSession()),
		RateLimit:  ChatRateLimit{MessagesPerMinute: int(p.GetRateLimit().GetMessagesPerMinute())},
		McpServers: chatMcpServersFromProto(p.GetMcpServers()),
	}
	if l := p.GetLogging(); l != nil { // #406
		sc.Logging = ChatLogging{Enabled: l.GetEnabled(), RetentionDays: int(l.GetRetentionDays()), Redact: l.Redact}
	}
	if p.GetLlm().GetTemperatureSet() {
		t := p.GetLlm().GetTemperature()
		sc.LLM.Temperature = &t
	}
	if raw := p.GetLlm().GetExtraBodyJson(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &sc.LLM.ExtraBody); err != nil {
			return nil, errors.New("llm.extra_body_json must be a JSON object: " + err.Error())
		}
	}
	return sc, nil
}
