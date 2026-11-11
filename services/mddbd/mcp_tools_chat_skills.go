package main

import (
	"context"
	"errors"
	"time"

	json "mddb/internal/jsonx"
)

// Chat skill tools (#404), in the admin toolset and admin only, reads
// included: a skill's text is part of system prompts. They call what REST
// and gRPC call, through the direct client. Importing from a URL is left to
// REST, gRPC and the panel, where an operator reads the text before saving.

// mcpChatSkillTools are the chat skill tools.
func mcpChatSkillTools() []MCPTool {
	name := map[string]interface{}{"type": "string", "description": "The skill's name"}
	str := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": description}
	}
	return []MCPTool{
		{
			Name: "chat_skill_list",
			Description: "List the chat skills — reusable SKILL.md instruction blocks that chat scenarios attach by name — " +
				"with each one's description, version, size, source and the scenarios using it, without contents. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			Name:        "chat_skill_get",
			Description: "Get one chat skill with its content and the list of its kept earlier versions; pass version to read one of those. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name":    name,
				"version": map[string]interface{}{"type": "integer", "description": "A kept version; the current one when left out"},
			}, "required": []string{"name"}},
		},
		{
			Name: "chat_skill_put",
			Description: "Create a chat skill or store a new version of it. content is the instructions, or a whole SKILL.md whose " +
				"front matter gives the description. Every scenario attaching the skill gets the new text; bumps the chat scenarios' version counter. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name":        name,
				"content":     str("Markdown instructions, or a whole SKILL.md"),
				"description": str("One or two sentences on what the skill does"),
				"source": map[string]interface{}{"type": "object", "description": "Where the text was fetched from, when it was imported",
					"properties": map[string]interface{}{"url": str("https URL"), "commit": str("Git commit"),
						"fetchedAt": str("RFC 3339 time; now when left out")}},
			}, "required": []string{"name", "content"}},
		},
		{
			Name:        "chat_skill_delete",
			Description: "Delete a chat skill. Refused while a scenario attaches it; the error names them. Bumps the chat scenarios' version counter. Needs admin.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": name}, "required": []string{"name"}},
		},
	}
}

func (s *MCPToolServer) toolChatSkillList(ctx context.Context, _ map[string]interface{}) (string, error) {
	list, version, err := s.client.ChatSkillList(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(chatSkillList{Skills: list, Version: version}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatSkillGet(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	sk, err := s.client.ChatSkillGet(ctx, name, mcpGetInt(args, "version"))
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(sk, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatSkillPut(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	sk := &ChatSkill{Name: name, Description: mcpGetString(args, "description"), Content: mcpGetString(args, "content")}
	if src, given := args["source"].(map[string]interface{}); given {
		sk.Source = &ChatSkillSource{URL: mcpGetString(src, "url"), Commit: mcpGetString(src, "commit"), FetchedAt: time.Now().UTC()}
		if at := mcpGetString(src, "fetchedAt"); at != "" {
			if sk.Source.FetchedAt, err = time.Parse(time.RFC3339, at); err != nil {
				return "", errors.New("source.fetchedAt must be an RFC 3339 time")
			}
		}
	}
	view, created, err := s.client.ChatSkillPut(ctx, sk)
	if err != nil {
		return "", err
	}
	data, _ := json.MarshalIndent(map[string]any{"skill": view, "created": created}, "", "  ")
	return string(data), nil
}

func (s *MCPToolServer) toolChatSkillDelete(ctx context.Context, args map[string]interface{}) (string, error) {
	name, err := chatScenarioName(args)
	if err != nil {
		return "", err
	}
	if err := s.client.ChatSkillDelete(ctx, name); err != nil {
		return "", err
	}
	data, _ := json.Marshal(map[string]any{"deleted": true, "name": name})
	return string(data), nil
}
