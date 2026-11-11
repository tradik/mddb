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

// #403: chat scenarios over MCP, and the three surfaces against one store.

func chatMCP(s *Server) *MCPToolServer {
	return &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
}

func TestChatScenarioMCPLifecycle(t *testing.T) {
	s := chatServer(t)
	ts := chatMCP(s)
	admin := asClaims(root)
	var scenario map[string]interface{}
	if err := json.Unmarshal([]byte(chatSupportBody), &scenario); err != nil {
		t.Fatal(err)
	}
	out, err := ts.mcpCallTool(admin, "chat_scenario_put", map[string]interface{}{"name": "support", "scenario": scenario})
	if err != nil || !strings.Contains(out, `"created": true`) || !strings.Contains(out, `"name": "support"`) {
		t.Fatalf("put: %s, %v", out, err)
	}
	out, err = ts.mcpCallTool(admin, "chat_scenario_put", map[string]interface{}{"name": "support", "scenario": scenario})
	if err != nil || !strings.Contains(out, `"created": false`) {
		t.Fatalf("replace: %s, %v", out, err)
	}
	out, err = ts.mcpCallTool(asClaims(alice), "chat_scenario_list", nil)
	if err != nil || !strings.Contains(out, `"version": 2`) || !strings.Contains(out, `"operatorPrompt": "You are Ada."`) {
		t.Fatalf("list: %s, %v", out, err)
	}
	out, err = ts.mcpCallTool(asClaims(alice), "chat_scenario_get", map[string]interface{}{"name": "support"})
	if err != nil || !strings.Contains(out, `"prompt": "You are Ada.\n\nGuardrails`) || !strings.Contains(out, `"scenario"`) {
		t.Fatalf("get: %s, %v", out, err)
	}

	for tool, args := range map[string]map[string]interface{}{
		"chat_scenario_get": {}, "chat_scenario_delete": {}, "chat_scenario_put": {"scenario": scenario},
	} {
		if _, err := ts.mcpCallTool(admin, tool, args); err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Errorf("%s without a name: %v", tool, err)
		}
	}
	for why, sc := range map[string]interface{}{
		"missing": nil, "not an object": "text", "wrong type": map[string]interface{}{"maxTurns": "many"},
		"unknown collection": map[string]interface{}{"allowedCollections": []interface{}{"ghost"}},
	} {
		if _, err := ts.mcpCallTool(admin, "chat_scenario_put", map[string]interface{}{"name": "x", "scenario": sc}); err == nil {
			t.Errorf("put with a scenario that is %s was accepted", why)
		}
	}
	if _, err := ts.mcpCallTool(admin, "chat_scenario_get", map[string]interface{}{"name": "ghost"}); !errors.Is(err, errChatScenarioNotFound) {
		t.Errorf("get of a missing scenario: %v", err)
	}
	// Admin tools are refused to the others, and a read-only server writes nothing.
	for _, tool := range []string{"chat_scenario_put", "chat_scenario_delete"} {
		args := map[string]interface{}{"name": "support", "scenario": scenario}
		if _, err := ts.mcpCallTool(asClaims(alice), tool, args); !errors.Is(err, ErrForbidden) {
			t.Errorf("%s as alice: %v", tool, err)
		}
		ro := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRead}
		if _, err := ro.mcpCallTool(admin, tool, args); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s on a read-only server: %v", tool, err)
		}
	}
	out, err = ts.mcpCallTool(admin, "chat_scenario_delete", map[string]interface{}{"name": "support"})
	if err != nil || out != `{"deleted":true,"name":"support"}` {
		t.Fatalf("delete: %s, %v", out, err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_scenario_delete", map[string]interface{}{"name": "support"}); !errors.Is(err, errChatScenarioNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// A client that fails: the list tool passes its error on.
	failing := &MCPToolServer{client: covBNewAllErr(), globalMode: ModeRW}
	if _, err := failing.toolChatScenarioList(context.Background(), nil); !errors.Is(err, errCovBClient) {
		t.Errorf("list through a failing client: %v", err)
	}
}

func (c *covBAllErrClient) ChatScenarioList(context.Context) ([]*ChatScenario, uint64, error) {
	return nil, 0, errCovBClient
}

// Deleting a scenario is gated (MCP-004) and asks for its name to be typed;
// the tools sit in the admin toolset, and only the reads work read-only.
func TestChatScenarioToolsClassification(t *testing.T) {
	if !mcpNeedsConfirmation("chat_scenario_delete") {
		t.Fatal("chat_scenario_delete must be gated")
	}
	ts := &MCPToolServer{}
	for name, readOnly := range map[string]bool{"chat_scenario_list": true, "chat_scenario_get": true, "chat_scenario_put": false, "chat_scenario_delete": false} {
		if mcpToolGroup[name] != ToolsetAdmin || ts.isToolReadOnly(name) != readOnly {
			t.Errorf("%s: toolset %q, read-only %v", name, mcpToolGroup[name], ts.isToolReadOnly(name))
		}
		if name != "chat_scenario_delete" && mcpNeedsConfirmation(name) {
			t.Errorf("%s must not be gated", name)
		}
	}
	args := map[string]interface{}{"name": "support"}
	q := newMCPConfirmRequest("chat_scenario_delete", args)
	props := q.Params["requestedSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	if name, _ := props["name"].(map[string]interface{}); name == nil || name["title"] != "Scenario name" {
		t.Errorf("chat_scenario_delete schema = %v", props)
	}
	if err := checkMCPConfirmation("chat_scenario_delete", args, accept(map[string]interface{}{"confirm": true, "name": "other"})); err == nil {
		t.Error("a wrong scenario name was accepted")
	}
	if err := checkMCPConfirmation("chat_scenario_delete", args, accept(map[string]interface{}{"confirm": true, "name": "support"})); err != nil {
		t.Errorf("the right scenario name was refused: %v", err)
	}
}

// Parity (#403): a scenario written on one surface reads the same on the
// other two, and every write moves the one version counter.
func TestChatScenarioParityAcrossSurfaces(t *testing.T) {
	s := chatServer(t)
	g, ts, admin := NewGRPCServer(s), chatMCP(s), asClaims(root)
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(chatSupportBody), &body); err != nil {
		t.Fatal(err)
	}

	// The same scenario, written three ways under three names.
	if rec := chatREST(t, s, root, http.MethodPut, "/v1/chat/scenarios/via-rest", chatSupportBody); rec.Code != http.StatusCreated {
		t.Fatalf("REST put: %d %s", rec.Code, rec.Body.String())
	}
	fromGRPC := chatProtoFixture()
	fromGRPC.Name = "via-grpc"
	if _, err := g.PutChatScenario(admin, &proto.PutChatScenarioRequest{Scenario: fromGRPC}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_scenario_put", map[string]interface{}{"name": "via-mcp", "scenario": body}); err != nil {
		t.Fatal(err)
	}

	// normal is a scenario's JSON without what differs by design: the name,
	// which comes first, and the two times, which come last.
	normal := func(sc *ChatScenario) string {
		b, _ := json.Marshal(sc)
		return string(b)[strings.Index(string(b), `"description"`):strings.Index(string(b), `"createdAt"`)]
	}
	stored := func(name string) *ChatScenario {
		sc, found := s.ChatScenarios.Get(name)
		if !found {
			t.Fatalf("%s is not stored", name)
		}
		return sc
	}
	want := normal(stored("via-rest"))
	for _, name := range []string{"via-grpc", "via-mcp"} {
		if got := normal(stored(name)); got != want {
			t.Errorf("%s stored\n %s\nwant\n %s", name, got, want)
		}
	}

	// Each surface reads all three, with the same version and prompt.
	var restList chatScenarioList
	if err := json.Unmarshal(chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios", "").Body.Bytes(), &restList); err != nil {
		t.Fatal(err)
	}
	grpcList, err := g.ListChatScenarios(admin, &proto.ListChatScenariosRequest{})
	if err != nil {
		t.Fatal(err)
	}
	mcpOut, err := ts.mcpCallTool(admin, "chat_scenario_list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var mcpList chatScenarioList
	if err := json.Unmarshal([]byte(mcpOut), &mcpList); err != nil {
		t.Fatal(err)
	}
	if len(restList.Scenarios) != 3 || len(grpcList.GetScenarios()) != 3 || len(mcpList.Scenarios) != 3 ||
		restList.Version != 3 || grpcList.GetVersion() != 3 || mcpList.Version != 3 {
		t.Fatalf("lists: REST %d@%d, gRPC %d@%d, MCP %d@%d", len(restList.Scenarios), restList.Version,
			len(grpcList.GetScenarios()), grpcList.GetVersion(), len(mcpList.Scenarios), mcpList.Version)
	}
	for i, sc := range restList.Scenarios {
		viaGRPC, err := chatScenarioFromProto(grpcList.GetScenarios()[i])
		if err != nil {
			t.Fatal(err)
		}
		if normal(sc) != want || normal(viaGRPC) != want || normal(mcpList.Scenarios[i]) != want {
			t.Errorf("%s reads differently across the surfaces", sc.Name)
		}
	}
	var restPrompt chatScenarioPrompt
	if err := json.Unmarshal(chatREST(t, s, root, http.MethodGet, "/v1/chat/scenarios/via-mcp/prompt", "").Body.Bytes(), &restPrompt); err != nil {
		t.Fatal(err)
	}
	grpcPrompt, err := g.GetChatScenarioPrompt(admin, &proto.GetChatScenarioPromptRequest{Name: "via-rest"})
	if err != nil {
		t.Fatal(err)
	}
	mcpGet, err := ts.mcpCallTool(admin, "chat_scenario_get", map[string]interface{}{"name": "via-grpc"})
	if err != nil {
		t.Fatal(err)
	}
	var viaMCP struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(mcpGet), &viaMCP); err != nil {
		t.Fatal(err)
	}
	if restPrompt.Prompt == "" || restPrompt.Prompt != grpcPrompt.GetPrompt() || restPrompt.Prompt != viaMCP.Prompt {
		t.Errorf("prompts differ:\nREST %q\ngRPC %q\nMCP  %q", restPrompt.Prompt, grpcPrompt.GetPrompt(), viaMCP.Prompt)
	}

	// A delete on any surface is seen by the others.
	if _, err := g.DeleteChatScenario(admin, &proto.DeleteChatScenarioRequest{Name: "via-rest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.mcpCallTool(admin, "chat_scenario_delete", map[string]interface{}{"name": "via-grpc"}); err != nil {
		t.Fatal(err)
	}
	if rec := chatREST(t, s, root, http.MethodDelete, "/v1/chat/scenarios/via-mcp", ""); rec.Body.String() != `{"deleted":true,"version":6}` {
		t.Errorf("REST delete: %s", rec.Body.String())
	}
	if list, _ := g.ListChatScenarios(admin, &proto.ListChatScenariosRequest{}); len(list.GetScenarios()) != 0 || list.GetVersion() != 6 {
		t.Errorf("after three deletes: %d scenarios at version %d", len(list.GetScenarios()), list.GetVersion())
	}
}
