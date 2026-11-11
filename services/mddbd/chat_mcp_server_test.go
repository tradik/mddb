package main

import (
	"fmt"
	"strings"
	"testing"
)

// MCP-005: a scenario's external servers — their bounds, and which switch
// keeps the model from seeing their tools.

func TestChatMcpServerNormalize(t *testing.T) {
	ok := ChatScenario{Name: "s", McpServers: []ChatMcpServer{{Name: "wiki", URL: "https://wiki.example.com/mcp"}}}
	if err := ok.Normalize(anyCollection); err != nil {
		t.Fatal(err)
	}
	if m := ok.McpServers[0]; m.TimeoutMs != 8000 || m.ResultMaxChars != 4000 || m.AllowedTools == nil {
		t.Errorf("defaults: %+v", m)
	}
	many := make([]ChatMcpServer, maxChatMcpServers+1)
	tools := make([]string, maxChatMcpTools+1)
	for i := range tools {
		tools[i] = fmt.Sprint("t", i)
	}
	for want, servers := range map[string][]ChatMcpServer{
		"at most 16 mcp servers":       many,
		"mcp server name":              {{URL: "https://a"}},
		"https only":                   {{Name: "a", URL: "http://a.example.com"}},
		"https only ":                  {{Name: "a", URL: "https://"}},
		"store it as a secret":         {{Name: "a", URL: "https://user:" + "pw@a.example.com"}},
		"timeoutMs must be":            {{Name: "a", URL: "https://a", TimeoutMs: 50}},
		"resultMaxChars must be":       {{Name: "a", URL: "https://a", ResultMaxChars: 1_000_000}},
		"at most 64 allowed tools":     {{Name: "a", URL: "https://a", AllowedTools: tools}},
		"empty, too long or listed":    {{Name: "a", URL: "https://a", AllowedTools: []string{"x", "x"}}},
		"empty, too long or listed ":   {{Name: "a", URL: "https://a", AllowedTools: []string{""}}},
		"mcp server a is listed twice": {{Name: "a", URL: "https://a"}, {Name: "a", URL: "https://b"}},
	} {
		sc := ChatScenario{Name: "s", McpServers: servers}
		if err := sc.Normalize(anyCollection); err == nil || !strings.Contains(err.Error(), strings.TrimSpace(want)) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestChatMcpStates(t *testing.T) {
	sc := &ChatScenario{Name: "s", Enabled: true, McpServers: []ChatMcpServer{
		{Name: "on", Enabled: true, AllowedTools: []string{"search"}},
		{Name: "off", AllowedTools: []string{"search"}},
		{Name: "keyless", Enabled: true, SecretRef: "ghost", AllowedTools: []string{}},
	}}
	none := func(string) bool { return false }
	states := chatMcpStates(sc, "", none)
	if !states[0].Active || !states[0].Tools[0].Active || states[0].Reason != "" {
		t.Errorf("on: %+v", states[0])
	}
	if states[1].Active || states[1].Reason != "server is disabled" || states[1].Tools[0].Reason != "server is disabled" {
		t.Errorf("off: %+v", states[1])
	}
	if states[2].Reason != "secret ghost does not exist" {
		t.Errorf("keyless: %+v", states[2])
	}
	sc.Enabled = false
	if r := chatMcpStates(sc, "", none)[0].Reason; r != "scenario is disabled" {
		t.Errorf("scenario off: %q", r)
	}
	if r := chatMcpStates(sc, FeatureChatExternalMCP, none)[0].Reason; r != "feature chatExternalMcp is off" {
		t.Errorf("module off: %q", r)
	}
	if r := chatMcpStates(sc, FeatureChat, none)[0].Reason; r != "feature chat is off" {
		t.Errorf("chat off: %q", r)
	}
}

// The modules external tools need (GO-049): chatExternalMcp ships off, and
// chat is named first when both are off.
func TestChatMcpFeatureOff(t *testing.T) {
	s := chatServer(t)
	if off := s.chatMcpFeatureOff(); off != FeatureChatExternalMCP {
		t.Errorf("default: %q", off)
	}
	externalMcpOn(t, s)
	if off := s.chatMcpFeatureOff(); off != "" {
		t.Errorf("on: %q", off)
	}
	setFeature(t, s, FeatureChat, false)
	if off := s.chatMcpFeatureOff(); off != FeatureChat {
		t.Errorf("chat off: %q", off)
	}
}

// The composed prompt carries each server's state and, with one active,
// the clause that frames external results as data.
func TestChatScenarioPromptWithMcpServers(t *testing.T) {
	s := chatServer(t)
	sc := &ChatScenario{Name: "s", Enabled: true, McpServers: []ChatMcpServer{{Name: "wiki", URL: "https://w", Enabled: true,
		AllowedTools: []string{"search"}}}}
	p := s.ChatScenarioPrompt(sc)
	if strings.Contains(p.Prompt, chatMcpClause) || p.McpServers[0].Active || p.McpServers[0].Reason != "feature chatExternalMcp is off" {
		t.Errorf("module off: %+v", p)
	}
	externalMcpOn(t, s)
	p = s.ChatScenarioPrompt(sc)
	if !strings.HasSuffix(p.Prompt, chatMcpClause) || !p.McpServers[0].Active {
		t.Errorf("module on: %+v", p)
	}
	if p := s.ChatScenarioPrompt(&ChatScenario{Name: "plain"}); p.McpServers != nil {
		t.Error("a scenario without servers has server states")
	}
}
