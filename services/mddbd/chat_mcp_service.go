package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// errChatMcpUnreachable: inspecting a server failed on the way there or back.
var errChatMcpUnreachable = errors.New("mcp server unreachable")

// The one implementation of external MCP servers (MCP-005) behind REST, gRPC
// and MCP: the secrets, inspecting a server, and checking a scenario's
// servers when it is saved. Every surface gates writes on a global
// administrator before it gets here.

// chatMcpFeatureOff names the first module external tools need that is off
// (GO-049): chat, then chatExternalMcp, which is off by default; "" when
// both are on.
func (s *Server) chatMcpFeatureOff() string {
	for _, f := range []string{FeatureChat, FeatureChatExternalMCP} {
		if !s.featureEnabled(f) {
			return f
		}
	}
	return ""
}

// ListChatMcpSecrets lists the secrets: names, headers and set, never values.
func (s *Server) ListChatMcpSecrets() []*chatMcpSecretView {
	if s.ChatScenarios == nil {
		return []*chatMcpSecretView{}
	}
	return s.ChatScenarios.ListMcpSecrets()
}

// PutChatMcpSecret validates and stores a secret; true when it is new.
func (s *Server) PutChatMcpSecret(name, header, value string) (bool, error) {
	if s.ChatScenarios == nil {
		return false, errChatScenariosUnavailable
	}
	if err := validChatMcpSecret(name, header, value); err != nil {
		return false, err
	}
	return s.ChatScenarios.PutMcpSecret(name, header, []byte(value))
}

// DeleteChatMcpSecret removes a secret no scenario uses.
func (s *Server) DeleteChatMcpSecret(name string) ([]string, error) {
	if s.ChatScenarios == nil {
		return nil, errChatScenariosUnavailable
	}
	return s.ChatScenarios.DeleteMcpSecret(name)
}

// chatMcpToolView is a tool of an inspected server, with whether a scenario
// may allow it.
type chatMcpToolView struct {
	mcpRemoteTool
	Allowable bool   `json:"allowable"`
	Reason    string `json:"reason,omitempty"`
}

// chatMcpInspection is what a server offers.
type chatMcpInspection struct {
	URL             string            `json:"url"`
	ServerName      string            `json:"serverName"`
	ServerVersion   string            `json:"serverVersion"`
	ProtocolVersion string            `json:"protocolVersion"`
	Tools           []chatMcpToolView `json:"tools"`
}

// connectChatMcp opens a session with a server, its credential read from
// secretRef. The URL was checked by the caller (a scenario's when it was
// saved); the SSRF guard applies to every request regardless.
func (s *Server) connectChatMcp(ctx context.Context, rawURL, secretRef string, timeout time.Duration) (*mcpRemoteClient, *mcpRemoteServerInfo, error) {
	var auth http.Header
	var secret string
	if secretRef != "" {
		var err error
		if auth, secret, err = s.ChatScenarios.McpSecretHeader(secretRef); err != nil {
			return nil, nil, err
		}
	}
	c := newMcpRemoteClient(rawURL, auth, secret, timeout)
	info, err := c.initialize(ctx)
	return c, info, err
}

// InspectChatMcpServer connects to a server and lists its tools. Refused
// while the chatExternalMcp module is off.
func (s *Server) InspectChatMcpServer(ctx context.Context, rawURL, secretRef string) (*chatMcpInspection, error) {
	if off := s.chatMcpFeatureOff(); off != "" {
		return nil, featureDisabledError{feature: off}
	}
	if s.ChatScenarios == nil {
		return nil, errChatScenariosUnavailable
	}
	timeout := chatMcpTimeoutDefault * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := validChatMcpURL(rawURL); err != nil {
		return nil, err
	}
	if secretRef != "" && !s.ChatScenarios.HasMcpSecret(secretRef) {
		return nil, fmt.Errorf("%w: %s", errChatMcpSecretNotFound, secretRef)
	}
	c, info, err := s.connectChatMcp(ctx, rawURL, secretRef, timeout)
	var tools []mcpRemoteTool
	if err == nil {
		tools, err = c.listTools(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errChatMcpUnreachable, err)
	}
	out := &chatMcpInspection{URL: rawURL, ServerName: info.ServerInfo.Name, ServerVersion: info.ServerInfo.Version,
		ProtocolVersion: info.ProtocolVersion, Tools: make([]chatMcpToolView, 0, len(tools))}
	for _, t := range tools {
		reason := t.refusal()
		out.Tools = append(out.Tools, chatMcpToolView{mcpRemoteTool: t, Allowable: reason == "", Reason: reason})
	}
	return out, nil
}

// checkChatMcpServers refuses a scenario whose servers name a missing
// secret, or allow a tool the server does not offer read-only. With the
// chatExternalMcp module off nothing is contacted: the servers are stored,
// inactive. A server that cannot be reached refuses the save unless it is
// disabled.
func (s *Server) checkChatMcpServers(ctx context.Context, sc *ChatScenario) error {
	for i := range sc.McpServers {
		if ref := sc.McpServers[i].SecretRef; ref != "" && !s.ChatScenarios.HasMcpSecret(ref) {
			return fmt.Errorf("mcp server %s: secret %s does not exist", sc.McpServers[i].Name, ref)
		}
	}
	if !s.featureEnabled(FeatureChatExternalMCP) {
		return nil
	}
	for i := range sc.McpServers {
		if err := s.checkChatMcpServer(ctx, &sc.McpServers[i]); err != nil {
			return err
		}
	}
	return nil
}

// checkChatMcpServer reads one server's tools and checks the allowed ones.
func (s *Server) checkChatMcpServer(ctx context.Context, m *ChatMcpServer) error {
	timeout := time.Duration(m.TimeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tools, err := s.chatMcpTools(ctx, m, timeout)
	switch {
	case err != nil && !m.Enabled:
		return nil // a disabled server may be down
	case err != nil:
		return fmt.Errorf("mcp server %s cannot be reached (save it disabled to keep it): %w", m.Name, err)
	}
	for _, name := range m.AllowedTools {
		t, found := tools[name]
		if !found {
			return fmt.Errorf("mcp server %s does not offer the tool %s", m.Name, name)
		}
		if reason := t.refusal(); reason != "" {
			return fmt.Errorf("tool %s of mcp server %s is refused: %s; only read-only tools may be allowed", name, m.Name, reason)
		}
	}
	return nil
}

// chatMcpTools connects to a scenario's server and returns its tools by name.
func (s *Server) chatMcpTools(ctx context.Context, m *ChatMcpServer, timeout time.Duration) (map[string]mcpRemoteTool, error) {
	c, _, err := s.connectChatMcp(ctx, m.URL, m.SecretRef, timeout)
	if err != nil {
		return nil, err
	}
	list, err := c.listTools(ctx)
	if err != nil {
		return nil, err
	}
	tools := make(map[string]mcpRemoteTool, len(list))
	for _, t := range list {
		tools[t.Name] = t
	}
	return tools, nil
}
