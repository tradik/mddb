package main

import (
	"errors"
	"fmt"
	"net/url"
)

// External knowledge over MCP (MCP-005): a scenario may name foreign MCP
// servers whose read-only tools the assistant can call next to its
// collections. Each level has its own switch, and a higher one turns the
// lower off: the chat module, the scenario, the chatExternalMcp module, the
// server, and the tool — only those in allowedTools exist for the model.

// Defaults and bounds of a server entry.
const (
	chatMcpTimeoutDefault     = 8000
	chatMcpTimeoutMin         = 100
	chatMcpTimeoutMax         = 60000
	chatMcpResultCharsDefault = 4000
	chatMcpResultCharsMin     = 100
	chatMcpResultCharsMax     = 100000
	maxChatMcpServers         = 16
	maxChatMcpTools           = 64
)

// ChatMcpServer is one external server of a scenario.
type ChatMcpServer struct {
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	Enabled        bool     `json:"enabled"`
	SecretRef      string   `json:"secretRef,omitempty"`
	AllowedTools   []string `json:"allowedTools"`
	TimeoutMs      int      `json:"timeoutMs"`
	ResultMaxChars int      `json:"resultMaxChars"`
}

// validChatMcpURL accepts an absolute https URL without credentials in it:
// a credential belongs in a secret, where no read returns it.
func validChatMcpURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Scheme != "https" || u.Host == "":
		return fmt.Errorf("%q: an MCP server is reached over https only", raw)
	case u.User != nil:
		return errors.New("an MCP server URL may not hold a user or password; store it as a secret")
	}
	return nil
}

// boundOrDefault fills 0 with def and refuses a value outside [lo, hi].
func boundOrDefault(field string, v *int, def, lo, hi int) error {
	if *v == 0 {
		*v = def
	}
	if *v < lo || *v > hi {
		return fmt.Errorf("%s must be between %d and %d", field, lo, hi)
	}
	return nil
}

// normalize checks one entry and fills its defaults.
func (m *ChatMcpServer) normalize() error {
	if err := validChatKeyName("mcp server", m.Name); err != nil {
		return err
	}
	if err := validChatMcpURL(m.URL); err != nil {
		return fmt.Errorf("mcp server %s: %w", m.Name, err)
	}
	if err := boundOrDefault("mcp server "+m.Name+": timeoutMs", &m.TimeoutMs, chatMcpTimeoutDefault, chatMcpTimeoutMin, chatMcpTimeoutMax); err != nil {
		return err
	}
	if err := boundOrDefault("mcp server "+m.Name+": resultMaxChars", &m.ResultMaxChars, chatMcpResultCharsDefault,
		chatMcpResultCharsMin, chatMcpResultCharsMax); err != nil {
		return err
	}
	if len(m.AllowedTools) > maxChatMcpTools {
		return fmt.Errorf("mcp server %s: at most %d allowed tools", m.Name, maxChatMcpTools)
	}
	seen := map[string]bool{}
	for _, tool := range m.AllowedTools {
		if tool == "" || len(tool) > maxChatScenarioNameLen || seen[tool] {
			return fmt.Errorf("mcp server %s: allowed tool %q is empty, too long or listed twice", m.Name, tool)
		}
		seen[tool] = true
	}
	if m.AllowedTools == nil {
		m.AllowedTools = []string{}
	}
	return nil
}

// normalizeChatMcpServers checks a scenario's servers: each valid, each name
// once.
func normalizeChatMcpServers(servers []ChatMcpServer) error {
	if len(servers) > maxChatMcpServers {
		return fmt.Errorf("at most %d mcp servers", maxChatMcpServers)
	}
	seen := map[string]bool{}
	for i := range servers {
		if err := servers[i].normalize(); err != nil {
			return err
		}
		if seen[servers[i].Name] {
			return fmt.Errorf("mcp server %s is listed twice", servers[i].Name)
		}
		seen[servers[i].Name] = true
	}
	return nil
}

// chatMcpToolState is whether the model sees one allowed tool, and why not.
type chatMcpToolState struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Reason string `json:"reason,omitempty"`
}

// chatMcpServerState is the state of one server of a scenario, part of the
// composed scenario (GET /v1/chat/scenarios/{name}/prompt).
type chatMcpServerState struct {
	Name    string             `json:"name"`
	URL     string             `json:"url"`
	Enabled bool               `json:"enabled"`
	Active  bool               `json:"active"`
	Reason  string             `json:"reason,omitempty"`
	Tools   []chatMcpToolState `json:"tools"`
}

// chatMcpInactiveReason is why a server of sc is off, "" when it is on —
// the first switch that is off, from the top. featureOff names the module
// that is off (chatMcpFeatureOff), "" when none is; secretSet reports
// whether a secret exists.
func chatMcpInactiveReason(sc *ChatScenario, m *ChatMcpServer, featureOff string, secretSet func(string) bool) string {
	switch {
	case featureOff != "":
		return "feature " + featureOff + " is off"
	case !sc.Enabled:
		return "scenario is disabled"
	case !m.Enabled:
		return "server is disabled"
	case m.SecretRef != "" && !secretSet(m.SecretRef):
		return "secret " + m.SecretRef + " does not exist"
	}
	return ""
}

// chatMcpStates is the state of every server of sc and of its tools.
func chatMcpStates(sc *ChatScenario, featureOff string, secretSet func(string) bool) []chatMcpServerState {
	out := make([]chatMcpServerState, 0, len(sc.McpServers))
	for i := range sc.McpServers {
		m := &sc.McpServers[i]
		reason := chatMcpInactiveReason(sc, m, featureOff, secretSet)
		st := chatMcpServerState{Name: m.Name, URL: m.URL, Enabled: m.Enabled, Active: reason == "", Reason: reason,
			Tools: make([]chatMcpToolState, 0, len(m.AllowedTools))}
		for _, tool := range m.AllowedTools {
			st.Tools = append(st.Tools, chatMcpToolState{Name: tool, Active: reason == "", Reason: reason})
		}
		out = append(out, st)
	}
	return out
}
