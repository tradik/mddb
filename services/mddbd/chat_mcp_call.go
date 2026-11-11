package main

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// Calling an external tool for a chat (MCP-005). mddb-chat runs the model and
// decides when a tool is called; mddbd makes the call, because the server's
// credential never leaves mddbd. Every switch is checked here again on each
// call — the modules, the scenario, the server, the tool in allowedTools,
// and the tool still read-only in the server's own tools/list — so a chat
// with a stale copy of a scenario cannot reach a tool it no longer may.
//
// What fails upstream (a timeout, a refused connection, a tool's own error)
// is not an error of this call: the answer carries isError and a short text
// for the model, and the chat's turn goes on.

var (
	errChatMcpServerNotFound = errors.New("mcp server not found in the scenario")
	errChatMcpNotAllowed     = errors.New("mcp tool not allowed")
)

// chatMcpCallRequest names the tool to call.
type chatMcpCallRequest struct {
	Scenario  string         `json:"scenario"`
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
}

// chatMcpCallResult is the tool's answer, cut to the server's resultMaxChars.
type chatMcpCallResult struct {
	Scenario   string `json:"scenario"`
	Server     string `json:"server"`
	Tool       string `json:"tool"`
	Content    string `json:"content"`
	IsError    bool   `json:"isError"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"durationMs"`
}

// truncateRunes cuts s to at most n characters.
func truncateRunes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
}

// chatMcpServerFor finds the server of a scenario the caller may read and
// checks every switch for the tool. ErrForbidden or errChatScenarioNotFound
// come from reading the scenario.
func (s *Server) chatMcpServerFor(ctx context.Context, req *chatMcpCallRequest) (*ChatMcpServer, error) {
	sc, err := s.GetChatScenario(ctx, req.Scenario)
	if err != nil {
		return nil, err
	}
	for i := range sc.McpServers {
		m := &sc.McpServers[i]
		if m.Name != req.Server {
			continue
		}
		if reason := chatMcpInactiveReason(sc, m, s.chatMcpFeatureOff(), s.ChatScenarios.HasMcpSecret); reason != "" {
			return nil, fmt.Errorf("%w: %s", errChatMcpNotAllowed, reason)
		}
		for _, tool := range m.AllowedTools {
			if tool == req.Tool {
				return m, nil
			}
		}
		return nil, fmt.Errorf("%w: %s is not in the allowed tools of %s", errChatMcpNotAllowed, req.Tool, m.Name)
	}
	return nil, fmt.Errorf("%w: %s", errChatMcpServerNotFound, req.Server)
}

// CallChatMcpTool calls an allowed tool of a scenario's server.
func (s *Server) CallChatMcpTool(ctx context.Context, req *chatMcpCallRequest) (*chatMcpCallResult, error) {
	m, err := s.chatMcpServerFor(ctx, req)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	timeout := time.Duration(m.TimeoutMs) * time.Millisecond
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := &chatMcpCallResult{Scenario: req.Scenario, Server: m.Name, Tool: req.Tool}
	text, isError, err := s.callChatMcp(callCtx, m, req)
	switch {
	case errors.Is(err, errChatMcpNotAllowed):
		return nil, err
	case callCtx.Err() != nil:
		text, isError = fmt.Sprintf("the external tool %s of %s did not answer within %d ms", req.Tool, m.Name, m.TimeoutMs), true
	case err != nil:
		text, isError = fmt.Sprintf("the external tool %s of %s failed: %v", req.Tool, m.Name, err), true
	}
	out.Content, out.Truncated = truncateRunes(text, m.ResultMaxChars)
	out.IsError, out.DurationMs = isError, time.Since(start).Milliseconds()
	return out, nil
}

// callChatMcp opens a session, checks the tool is still read-only in the
// server's own list, and calls it.
func (s *Server) callChatMcp(ctx context.Context, m *ChatMcpServer, req *chatMcpCallRequest) (string, bool, error) {
	c, _, err := s.connectChatMcp(ctx, m.URL, m.SecretRef, time.Duration(m.TimeoutMs)*time.Millisecond)
	if err != nil {
		return "", false, err
	}
	tools, err := c.listTools(ctx)
	if err != nil {
		return "", false, err
	}
	for _, t := range tools {
		if t.Name != req.Tool {
			continue
		}
		if reason := t.refusal(); reason != "" {
			return "", false, fmt.Errorf("%w: %s of %s is refused: %s", errChatMcpNotAllowed, t.Name, m.Name, reason)
		}
		return c.callTool(ctx, req.Tool, req.Arguments)
	}
	return "", false, fmt.Errorf("%s no longer offers the tool %s", m.Name, req.Tool)
}
