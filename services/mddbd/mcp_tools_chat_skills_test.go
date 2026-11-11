package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/proto"
)

// #404: chat skills over MCP, and the three surfaces against one store.

func TestChatSkillMCPLifecycle(t *testing.T) {
	s := chatServer(t)
	ts, admin := chatMCP(s), asClaims(root)
	out, err := ts.mcpCallTool(admin, "chat_skill_put", map[string]interface{}{"name": "tone", "content": toneSkillMD})
	if err != nil || !strings.Contains(out, `"created": true`) || !strings.Contains(out, `"description": "Friendly answers"`) ||
		!strings.Contains(out, `"content": "Be kind."`) {
		t.Fatalf("put: %s, %v", out, err)
	}
	source := map[string]interface{}{"url": "https://example.com/SKILL.md", "commit": "abc", "fetchedAt": "2026-11-11T10:00:00Z"}
	out, err = ts.mcpCallTool(admin, "chat_skill_put", map[string]interface{}{"name": "tone", "content": "Be kind and brief.",
		"description": "Brief", "source": source})
	if err != nil || !strings.Contains(out, `"created": false`) || !strings.Contains(out, `"version": 2`) ||
		!strings.Contains(out, `"fetchedAt": "2026-11-11T10:00:00Z"`) {
		t.Fatalf("version 2: %s, %v", out, err)
	}
	// A source without a time is fetched now.
	out, err = ts.mcpCallTool(admin, "chat_skill_put", map[string]interface{}{"name": "other", "content": "x",
		"source": map[string]interface{}{"url": "https://example.com/o"}})
	if err != nil || !strings.Contains(out, `"url": "https://example.com/o"`) {
		t.Fatalf("a source without a time: %s, %v", out, err)
	}
	out, err = ts.mcpCallTool(admin, "chat_skill_list", nil)
	if err != nil || !strings.Contains(out, `"version": 3`) || strings.Contains(out, `"content"`) || !strings.Contains(out, `"bytes": 18`) {
		t.Fatalf("list: %s, %v", out, err)
	}
	out, err = ts.mcpCallTool(admin, "chat_skill_get", map[string]interface{}{"name": "tone"})
	if err != nil || !strings.Contains(out, `"content": "Be kind and brief."`) || !strings.Contains(out, `"history"`) {
		t.Fatalf("get: %s, %v", out, err)
	}
	out, err = ts.mcpCallTool(admin, "chat_skill_get", map[string]interface{}{"name": "tone", "version": float64(1)})
	if err != nil || !strings.Contains(out, `"content": "Be kind."`) {
		t.Fatalf("get version 1: %s, %v", out, err)
	}

	for _, tool := range []string{"chat_skill_get", "chat_skill_put", "chat_skill_delete"} {
		if _, err := ts.mcpCallTool(admin, tool, map[string]interface{}{"content": "x"}); err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Errorf("%s without a name: %v", tool, err)
		}
	}
	for why, args := range map[string]map[string]interface{}{
		"no content":      {"name": "x"},
		"a bad time":      {"name": "x", "content": "x", "source": map[string]interface{}{"url": "https://example.com/o", "fetchedAt": "yesterday"}},
		"an http source":  {"name": "x", "content": "x", "source": map[string]interface{}{"url": "http://example.com/o"}},
		"a reserved name": {"name": "import", "content": "x"},
	} {
		if _, err := ts.mcpCallTool(admin, "chat_skill_put", args); err == nil {
			t.Errorf("put with %s was accepted", why)
		}
	}
	if _, err := ts.mcpCallTool(admin, "chat_skill_get", map[string]interface{}{"name": "ghost"}); !errors.Is(err, errChatSkillNotFound) {
		t.Errorf("get of a missing skill: %v", err)
	}
	// Admin only, reads included; a read-only server stores nothing.
	for _, tool := range []string{"chat_skill_list", "chat_skill_get", "chat_skill_put", "chat_skill_delete"} {
		args := map[string]interface{}{"name": "tone", "content": "x"}
		if _, err := ts.mcpCallTool(asClaims(alice), tool, args); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s as alice: %v", tool, err)
		}
		ro := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRead}
		_, err := ro.mcpCallTool(admin, tool, args)
		if readOnly := strings.HasSuffix(tool, "_list") || strings.HasSuffix(tool, "_get"); readOnly != (err == nil) {
			t.Errorf("%s on a read-only server: %v", tool, err)
		}
	}
	// In use: the error names the scenarios.
	if _, err := s.PutChatScenario(&ChatScenario{Name: "support", Skills: []string{"tone"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_skill_delete", map[string]interface{}{"name": "tone"}); !errors.Is(err, errChatSkillInUse) ||
		!strings.Contains(err.Error(), "in use by: support") {
		t.Errorf("delete in use: %v", err)
	}
	// The scenario's tool shows the skill in the prompt, and what is missing.
	if _, err := s.PutChatScenario(&ChatScenario{Name: "support", Skills: []string{"tone", "ghost"}}); err != nil {
		t.Fatal(err)
	}
	out, err = ts.mcpCallTool(admin, "chat_scenario_get", map[string]interface{}{"name": "support"})
	if err != nil || !strings.Contains(out, `Skill tone — Brief:\nBe kind and brief.`) || !strings.Contains(out, `"missingSkills": [`) ||
		!strings.Contains(out, `"approxTokens"`) || !strings.Contains(out, `"bytes"`) || strings.Contains(out, `"warning"`) {
		t.Fatalf("scenario get: %s, %v", out, err)
	}
	t.Setenv("MDDB_CHAT_PROMPT_MAX_BYTES", "350")
	if out, err = ts.mcpCallTool(admin, "chat_scenario_get", map[string]interface{}{"name": "support"}); err != nil || !strings.Contains(out, `"warning"`) {
		t.Fatalf("scenario get near the cap: %s, %v", out, err)
	}
	if err := s.DeleteChatScenario("support"); err != nil {
		t.Fatal(err)
	}
	out, err = ts.mcpCallTool(admin, "chat_skill_delete", map[string]interface{}{"name": "tone"})
	if err != nil || out != `{"deleted":true,"name":"tone"}` {
		t.Fatalf("delete: %s, %v", out, err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_skill_delete", map[string]interface{}{"name": "tone"}); !errors.Is(err, errChatSkillNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// A client that fails: the tools pass its error on.
	failing := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	if _, err := failing.toolChatSkillList(context.Background(), nil); !errors.Is(err, errCovBClient) {
		t.Errorf("list through a failing client: %v", err)
	}
}

func (c *covBAllErrClient) ChatSkillList(context.Context) ([]*chatSkillView, uint64, error) {
	return nil, 0, errCovBClient
}

// Deleting a skill is gated (MCP-004) and asks for its name to be typed; the
// tools sit in the admin toolset, and only the reads work read-only.
func TestChatSkillToolsClassification(t *testing.T) {
	ts := &MCPToolServer{}
	for name, readOnly := range map[string]bool{"chat_skill_list": true, "chat_skill_get": true, "chat_skill_put": false, "chat_skill_delete": false} {
		if mcpToolGroup[name] != ToolsetAdmin || ts.isToolReadOnly(name) != readOnly || mcpToolAccessRules[name].class != accessAdmin {
			t.Errorf("%s: toolset %q, read-only %v, rule %+v", name, mcpToolGroup[name], ts.isToolReadOnly(name), mcpToolAccessRules[name])
		}
		if mcpNeedsConfirmation(name) != (name == "chat_skill_delete") {
			t.Errorf("%s: gated = %v", name, mcpNeedsConfirmation(name))
		}
	}
	args := map[string]interface{}{"name": "tone"}
	q := newMCPConfirmRequest("chat_skill_delete", args)
	props := q.Params["requestedSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	if name, _ := props["name"].(map[string]interface{}); name == nil || name["title"] != "Skill name" {
		t.Errorf("chat_skill_delete schema = %v", props)
	}
	if err := checkMCPConfirmation("chat_skill_delete", args, accept(map[string]interface{}{"confirm": true, "name": "other"})); err == nil {
		t.Error("a wrong skill name was accepted")
	}
	if err := checkMCPConfirmation("chat_skill_delete", args, accept(map[string]interface{}{"confirm": true, "name": "tone"})); err != nil {
		t.Errorf("the right skill name was refused: %v", err)
	}
}

// Parity (#404): a skill written on one surface reads the same on the other
// two, every write moves the scenarios' version counter, and a scenario
// attaching the skills composes one prompt on all three.
func TestChatSkillParityAcrossSurfaces(t *testing.T) {
	s := chatServer(t)
	g, ts, admin := NewGRPCServer(s), chatMCP(s), asClaims(root)
	source := map[string]interface{}{"url": "https://example.com/SKILL.md", "commit": skillShaOne, "fetchedAt": "2026-11-11T10:00:00Z"}
	body, _ := json.Marshal(map[string]interface{}{"description": "Friendly answers", "content": "Be kind.", "source": source})

	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/skills/via-rest", string(body)); rec.Code != http.StatusCreated {
		t.Fatalf("REST put: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := g.PutChatSkill(admin, &proto.PutChatSkillRequest{Skill: &proto.ChatSkillProto{Name: "via-grpc", Description: "Friendly answers",
		Content: "Be kind.", Source: &proto.ChatSkillSourceProto{Url: "https://example.com/SKILL.md", Commit: skillShaOne, FetchedAt: 1794391200}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_skill_put", map[string]interface{}{"name": "via-mcp", "description": "Friendly answers",
		"content": "Be kind.", "source": source}); err != nil {
		t.Fatal(err)
	}

	// normal is a skill's JSON without what differs by design: the name,
	// which comes first, and the two times, which come last.
	normal := func(sk any) string {
		b, _ := json.Marshal(sk)
		return string(b)[strings.Index(string(b), `"description"`):strings.Index(string(b), `"createdAt"`)]
	}
	stored := func(name string) *ChatSkill {
		sk, found := s.ChatScenarios.GetSkill(name)
		if !found {
			t.Fatalf("%s is not stored", name)
		}
		return sk
	}
	want := normal(stored("via-rest"))
	for _, name := range []string{"via-grpc", "via-mcp"} {
		if got := normal(stored(name)); got != want || !strings.Contains(got, skillShaOne) {
			t.Errorf("%s stored\n %s\nwant\n %s", name, got, want)
		}
	}

	// Each surface lists all three at the same version, and reads one alike.
	restList := skillJSON[chatSkillList](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/skills", ""))
	grpcList, err := g.ListChatSkills(admin, &proto.ListChatSkillsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	mcpOut, err := ts.mcpCallTool(admin, "chat_skill_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var mcpList chatSkillList
	if err := json.Unmarshal([]byte(mcpOut), &mcpList); err != nil {
		t.Fatal(err)
	}
	if len(restList.Skills) != 3 || len(grpcList.GetSkills()) != 3 || len(mcpList.Skills) != 3 ||
		restList.Version != 3 || grpcList.GetVersion() != 3 || mcpList.Version != 3 || s.ChatScenariosVersion() != 3 {
		t.Fatalf("lists: REST %d@%d, gRPC %d@%d, MCP %d@%d", len(restList.Skills), restList.Version,
			len(grpcList.GetSkills()), grpcList.GetVersion(), len(mcpList.Skills), mcpList.Version)
	}
	restOne := skillJSON[chatSkillView](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/skills/via-mcp", ""))
	grpcOne, err := g.GetChatSkill(admin, &proto.GetChatSkillRequest{Name: "via-rest"})
	if err != nil {
		t.Fatal(err)
	}
	mcpGet, err := ts.mcpCallTool(admin, "chat_skill_get", map[string]interface{}{"name": "via-grpc"})
	if err != nil {
		t.Fatal(err)
	}
	var mcpOne chatSkillView
	if err := json.Unmarshal([]byte(mcpGet), &mcpOne); err != nil {
		t.Fatal(err)
	}
	if restOne.Content != "Be kind." || grpcOne.GetContent() != restOne.Content || mcpOne.Content != restOne.Content ||
		grpcOne.GetSource().GetCommit() != restOne.Source.Commit || mcpOne.Source.Commit != restOne.Source.Commit ||
		grpcOne.GetSource().GetFetchedAt() != restOne.Source.FetchedAt.Unix() || !mcpOne.Source.FetchedAt.Equal(restOne.Source.FetchedAt) ||
		int(grpcOne.GetVersion()) != restOne.Version || mcpOne.Version != restOne.Version || int(grpcOne.GetBytes()) != restOne.Bytes || mcpOne.Bytes != restOne.Bytes {
		t.Errorf("one skill reads differently:\nREST %+v\ngRPC %+v\nMCP  %+v", restOne, grpcOne, mcpOne)
	}

	// One scenario over the three skills: the same prompt everywhere.
	if _, err := s.PutChatScenario(&ChatScenario{Name: "support", Skills: []string{"via-mcp", "via-rest", "via-grpc", "ghost"}}); err != nil {
		t.Fatal(err)
	}
	restPrompt := skillJSON[chatScenarioPrompt](t, chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/support/prompt", ""))
	grpcPrompt, err := g.GetChatScenarioPrompt(admin, &proto.GetChatScenarioPromptRequest{Name: "support"})
	if err != nil {
		t.Fatal(err)
	}
	mcpScenario, err := ts.mcpCallTool(admin, "chat_scenario_get", map[string]interface{}{"name": "support"})
	if err != nil {
		t.Fatal(err)
	}
	var viaMCP chatScenarioPrompt
	if err := json.Unmarshal([]byte(mcpScenario), &viaMCP); err != nil {
		t.Fatal(err)
	}
	order := strings.Index(restPrompt.Prompt, "Skill via-mcp") < strings.Index(restPrompt.Prompt, "Skill via-rest") &&
		strings.Index(restPrompt.Prompt, "Skill via-rest") < strings.Index(restPrompt.Prompt, "Skill via-grpc")
	if !order || restPrompt.Prompt != grpcPrompt.GetPrompt() || restPrompt.Prompt != viaMCP.Prompt ||
		int64(restPrompt.Bytes) != grpcPrompt.GetBytes() || restPrompt.Bytes != viaMCP.Bytes ||
		int64(restPrompt.ApproxTokens) != grpcPrompt.GetApproxTokens() || restPrompt.ApproxTokens != viaMCP.ApproxTokens ||
		strings.Join(restPrompt.MissingSkills, ",") != "ghost" || strings.Join(grpcPrompt.GetMissingSkills(), ",") != "ghost" ||
		strings.Join(viaMCP.MissingSkills, ",") != "ghost" {
		t.Errorf("prompts differ:\nREST %+v\ngRPC %+v\nMCP  %+v", restPrompt, grpcPrompt, viaMCP)
	}
	usedBy, err := g.GetChatSkillUsedBy(admin, &proto.GetChatSkillUsedByRequest{Name: "via-rest"})
	if err != nil || strings.Join(usedBy.GetScenarios(), ",") != "support" || strings.Join(restOne.UsedBy, ",") != "" {
		t.Errorf("used by: %+v, %v", usedBy, err)
	}

	// In use on every surface; then a delete on any surface is seen by the others.
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/skills/via-grpc", ""); rec.Code != http.StatusConflict {
		t.Errorf("REST delete in use: %d", rec.Code)
	}
	if err := s.DeleteChatScenario("support"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.DeleteChatSkill(admin, &proto.DeleteChatSkillRequest{Name: "via-rest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_skill_delete", map[string]interface{}{"name": "via-grpc"}); err != nil {
		t.Fatal(err)
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/skills/via-mcp", ""); rec.Body.String() != `{"deleted":true,"version":8}` {
		t.Errorf("REST delete: %s", rec.Body.String())
	}
	if list, _ := g.ListChatSkills(admin, &proto.ListChatSkillsRequest{}); len(list.GetSkills()) != 0 || list.GetVersion() != 8 {
		t.Errorf("after three deletes: %d skills at version %d", len(list.GetSkills()), list.GetVersion())
	}
}
