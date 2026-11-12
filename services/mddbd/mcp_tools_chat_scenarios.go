package main

import (
	"context"
	"errors"

	json "mddb/internal/jsonx"
)

// Chat scenario tools (#403), in the admin toolset: list and get read,
// put and delete need admin. They call what REST and gRPC call, through the
// direct client (mcp_direct_chat_scenarios.go).

// chatScenarioSchema describes the scenario object chat_scenario_put takes —
// the body of PUT /v1/chat/scenarios/{name}.
var chatScenarioSchema = map[string]interface{}{
	"type":        "object",
	"description": "The whole scenario; fields left out are reset to their defaults.",
	"properties": map[string]interface{}{
		"description":    map[string]interface{}{"type": "string"},
		"enabled":        map[string]interface{}{"type": "boolean"},
		"operatorPrompt": map[string]interface{}{"type": "string", "description": "Who the assistant is, its tone and language policy"},
		"guardrails":     map[string]interface{}{"type": "string", "description": "What it must not do or say"},
		"offTopic": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"mode":             map[string]interface{}{"type": "string", "enum": []string{OffTopicRefuse, OffTopicRedirect, OffTopicBrief}},
			"redirectTemplate": map[string]interface{}{"type": "string"},
		}},
		"allowedCollections": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"},
			"description": "Existing collections or aliases the assistant may search"},
		"skills": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"},
			"description": "Names of chat skills, appended to the prompt in this order after the guardrails"},
		"style": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"emoji":            map[string]interface{}{"type": "string", "enum": []string{EmojiOff, EmojiSparingly, EmojiFree}},
			"verbosity":        map[string]interface{}{"type": "string", "enum": []string{VerbosityShort, VerbosityNormal, VerbosityLong}},
			"markdown":         map[string]interface{}{"type": "boolean"},
			"citationTemplate": map[string]interface{}{"type": "string"},
		}},
		"llm": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"model":       map[string]interface{}{"type": "string"},
			"temperature": map[string]interface{}{"type": "number"},
			"maxTokens":   map[string]interface{}{"type": "integer"},
			"extraBody":   map[string]interface{}{"type": "object", "description": "Sent to the model API as is, e.g. reasoning_effort"},
			"loopGuard": map[string]interface{}{"type": "object",
				"description": "Overrides mddb-chat's repetition-loop guard (#413); 0 or absent keeps its setting",
				"properties": map[string]interface{}{
					"enabled":   map[string]interface{}{"type": "boolean"},
					"ngram":     map[string]interface{}{"type": "integer", "minimum": 0, "maximum": maxLoopGuardNgram},
					"window":    map[string]interface{}{"type": "integer", "minimum": 0, "maximum": maxLoopGuardWindow},
					"ratio":     map[string]interface{}{"type": "number", "minimum": 0, "maximum": 1},
					"minOutput": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": maxLoopGuardMinOutput},
					"unit":      map[string]interface{}{"type": "string", "enum": []string{LoopGuardUnitChars, LoopGuardUnitWords}},
				}},
		}},
		"maxTurns":              map[string]interface{}{"type": "integer"},
		"tokenBudgetPerSession": map[string]interface{}{"type": "integer"},
		"rateLimit": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"messagesPerMinute": map[string]interface{}{"type": "integer"},
		}},
		"mcpServers": map[string]interface{}{"type": "array", "items": chatMcpServerSchema,
			"description": "External MCP servers whose read-only tools the assistant may call (MCP-005); each allowed tool is checked against the server when saved"},
	},
}

// mcpChatScenarioTools are the chat scenario tools.
func mcpChatScenarioTools() []MCPTool {
	name := map[string]interface{}{"type": "string", "description": "The scenario's name"}
	return []MCPTool{
		{
			Name: "chat_scenario_list",
			Description: "List the chat scenarios the caller may read — each an assistant's prompt, guardrails, allowed " +
				"collections, style, LLM parameters and limits — with the store's version counter, which grows on every change.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name: "chat_scenario_get",
			Description: "Get one chat scenario and its composed system prompt — operator prompt, guardrails, off-topic clause, its skills in order, " +
				"style clause, then each allowed collection's responsePrompt — with the prompt's size and the skills it names that do not exist.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": name}, "required": []string{"name"}},
		},
		{
			Name:        "chat_scenario_put",
			Description: "Create a chat scenario or replace it whole. Every allowed collection must exist. Bumps the version counter. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name": name, "scenario": chatScenarioSchema,
			}, "required": []string{"name", "scenario"}},
		},
		{
			Name:        "chat_scenario_delete",
			Description: "Delete a chat scenario; an assistant using it falls back to its own configuration. Bumps the version counter. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": name}, "required": []string{"name"}},
		},
	}
}

func (s *MCPToolServer) toolChatScenarioList(ctx context.Context, _ map[string]interface{}) (string, error) {
	list, version, err := s.client.ChatScenarioList(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(chatScenarioList{Scenarios: list, Version: version}, "", "  ")
	return string(data), nil
}

// chatScenarioName reads the required name argument.
func chatScenarioName(args map[string]interface{}) (string, error) {
	name := mcpGetString(args, "name")
	if name == "" {
		return "", errors.New("name is required")
	}
	return name, nil
}

func (s *MCPToolServer) toolChatScenarioGet(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	sc, p, err := s.client.ChatScenarioGet(ctx, name)
	if err != nil {
		return "", err
	}
	out := map[string]any{"scenario": sc, "prompt": p.Prompt, "bytes": p.Bytes, "approxTokens": p.ApproxTokens}
	if p.Warning != "" {
		out["warning"] = p.Warning
	}
	if len(p.MissingSkills) > 0 {
		out["missingSkills"] = p.MissingSkills
	}
	if len(p.McpServers) > 0 {
		out["mcpServers"] = p.McpServers
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatScenarioPut(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	body, isObject := args["scenario"].(map[string]interface{})
	if !isObject {
		return "", errors.New("scenario is required and must be an object")
	}
	var sc ChatScenario
	raw, _ := json.Marshal(body) // decoded from JSON, so it encodes
	if err := json.Unmarshal(raw, &sc); err != nil {
		return "", errors.New("scenario: " + err.Error())
	}
	sc.Name = name
	created, err := s.client.ChatScenarioPut(ctx, &sc)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]any{"scenario": &sc, "created": created}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatScenarioDelete(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	if err := s.client.ChatScenarioDelete(ctx, name); err != nil {
		return "", err
	}
	data, _ := json.Marshal(map[string]any{"deleted": true, "name": name})
	return string(data), nil
}
