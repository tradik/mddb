package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-049, per module: switched off, its REST routes answer 404, its RPCs
// FAILED_PRECONDITION, its MCP tools leave tools/list and are refused;
// switched on, every one of them gets through to its handler.

// moduleSurface lists one module's entry points from the gate tables, plus
// the chat ones matched by name.
func moduleSurface(feature string) (routes, methods, tools []string) {
	for p, f := range httpFeatureRoutes {
		if f == feature {
			routes = append(routes, p)
		}
	}
	for m, f := range grpcFeatureMethods {
		if f == feature {
			methods = append(methods, "/mddb.MDDB/"+m)
		}
	}
	if feature == FeatureChat {
		routes = append(routes, "/v1/chat/scenarios", "/v1/chat/skills/", "/v1/chat/conversations")
		methods = append(methods, "/mddb.MDDB/ListChatScenarios", "/mddb.MDDB/ListChatSkills", "/mddb.MDDB/ExportChatConversations")
	}
	for _, t := range mcpBuiltinTools() {
		if toolFeature(t.Name) == feature {
			tools = append(tools, t.Name)
		}
	}
	return routes, methods, tools
}

func featureToolNames(tools []MCPTool) map[string]bool {
	out := map[string]bool{}
	for _, t := range tools {
		out[t.Name] = true
	}
	return out
}

func TestEveryModuleSwitchesEverySurface(t *testing.T) {
	t.Setenv(featuresForceOffEnv, "")
	s, cleanup := newTestServer(t)
	defer cleanup()
	s.Features = NewFeatureManager(s.DB)
	if err := s.Features.LoadAll(); err != nil {
		t.Fatal(err)
	}
	reached := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	client := NewDirectClient(s)
	for _, d := range featureRegistry {
		routes, methods, tools := moduleSurface(d.Name)
		if d.Name != FeatureChatExternalMCP && len(routes) == 0 {
			t.Errorf("%s has no route", d.Name)
		}
		for _, on := range []bool{false, true} {
			if err := s.Features.Set(d.Name, on, "test"); err != nil {
				t.Fatal(err)
			}
			for _, p := range routes {
				rec := httptest.NewRecorder()
				s.featureGate(p, reached).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
				want := http.StatusTeapot
				if !on {
					want = http.StatusNotFound
				}
				if rec.Code != want || (!on && rec.Body.String() != `{"error":"feature disabled","feature":"`+d.Name+`"}`) {
					t.Errorf("%s on=%v REST %s: %d %s", d.Name, on, p, rec.Code, rec.Body.String())
				}
			}
			for _, m := range methods {
				uerr, serr := grpcCall(s, m)
				for _, err := range []error{uerr, serr} {
					if on && err != nil || !on && (status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "feature disabled: "+d.Name) {
						t.Errorf("%s on=%v gRPC %s: %v", d.Name, on, m, err)
					}
				}
			}
			listed := featureToolNames(featureVisibleTools(client, nil, mcpAllTools(nil)))
			ts := &MCPToolServer{client: client, globalMode: ModeRW}
			for _, tool := range tools {
				// Switched on, the gate lets it through (the tool itself is
				// tested where it lives); off, the call is refused first.
				err := s.toolFeatureError(tool)
				if !on {
					_, err = ts.mcpCallTool(context.Background(), tool, map[string]interface{}{})
				}
				refused := err != nil && strings.Contains(err.Error(), "feature disabled: "+d.Name)
				if listed[tool] != on || refused == on {
					t.Errorf("%s on=%v MCP %s: listed %v, call %v", d.Name, on, tool, listed[tool], err)
				}
			}
		}
		_ = s.Features.Set(d.Name, d.Default, "test")
	}
}

func TestCoreIsNeverSwitched(t *testing.T) {
	for _, p := range []string{"/v1/search", "/v1/fts", "/v1/features", "/v1/backup", "/v1/auth/login"} {
		if routeFeature(p) != "" {
			t.Errorf("route %s is core", p)
		}
	}
	for _, m := range []string{"/mddb.MDDB/Search", "/mddb.MDDB/ListFeatures", "/mddb.MDDB/SetFeature", "/mddb.MDDBReplication/StreamBinlog"} {
		if rpcFeature(m) != "" {
			t.Errorf("rpc %s is core", m)
		}
	}
	for _, tool := range []string{"search_documents", "feature_list", "feature_set", "full_text_search"} {
		if toolFeature(tool) != "" {
			t.Errorf("tool %s is core", tool)
		}
	}
	for f := range mcpToolsetFeatures {
		if mcpToolsets[f] == nil {
			t.Errorf("toolset %s does not exist", f)
		}
	}
	for tool := range mcpToolFeatures {
		if _, ok := mcpToolGroup[tool]; !ok {
			t.Errorf("tool %s does not exist", tool)
		}
	}
	for route := range httpFeatureRoutes {
		if _, ok := httpAccessRules[route]; !ok {
			t.Errorf("route %s is not registered", route)
		}
	}
}

func TestFeatureGatesOnCustomToolsAndOtherClients(t *testing.T) {
	t.Setenv(featuresForceOffEnv, "codeIntel")
	s, cleanup := newTestServer(t)
	defer cleanup()
	s.Features = NewFeatureManager(s.DB)
	_ = s.Features.LoadAll()
	custom := []MCPCustomToolConfig{{Name: "deps", Action: "code_graph"}, {Name: "find", Action: "semantic_search"}}
	all := mcpToolsFor(context.Background(), custom)
	listed := featureToolNames(featureVisibleTools(NewDirectClient(s), custom, all))
	if listed["deps"] || !listed["find"] || listed["code_graph"] {
		t.Errorf("a custom tool over a disabled module's tool is hidden: %v", listed)
	}
	if got := featureVisibleTools(covBNewAllErr(), custom, all); len(got) != len(all) {
		t.Error("without an in-process server the list is unchanged")
	}
	ts := &MCPToolServer{client: NewDirectClient(s), customTools: custom, globalMode: ModeRW}
	if _, err := ts.mcpCallTool(context.Background(), "deps", map[string]interface{}{}); err == nil || !strings.Contains(err.Error(), "feature disabled: codeIntel") {
		t.Errorf("a custom tool over a disabled module is refused: %v", err)
	}
	// The MCP transports list through the filter too.
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")
	resp := h.handleToolsList(context.Background(), map[string]interface{}{})
	if featureToolNames(resp["tools"].([]MCPTool))["code_graph"] {
		t.Error("stdio/JSON-RPC tools/list shows a disabled module's tool")
	}
	m := &MCPHTTPServer{client: NewDirectClient(s)}
	rec := httptest.NewRecorder()
	m.handleTools(rec, httptest.NewRequest(http.MethodGet, "/tools", nil))
	if strings.Contains(rec.Body.String(), `"code_graph"`) {
		t.Error("/tools shows a disabled module's tool")
	}
}

func TestFeatureGateGraphQL(t *testing.T) {
	t.Setenv(featuresForceOffEnv, "codeIntel")
	s, cleanup := newTestServer(t)
	defer cleanup()
	s.Features = NewFeatureManager(s.DB)
	_ = s.Features.LoadAll()
	next := func(context.Context) (any, error) { return "ran", nil }
	field := func(object, name string) context.Context {
		return graphql.WithFieldContext(context.Background(), &graphql.FieldContext{Object: object, Field: graphql.CollectedField{Field: &ast.Field{Name: name}}})
	}
	if _, err := s.featureFieldMiddleware(field("Query", "codeSymbol"), next); err == nil || !strings.Contains(err.Error(), "feature disabled: codeIntel") {
		t.Errorf("codeSymbol: %v", err)
	}
	for _, ctx := range []context.Context{field("Query", "search"), context.Background()} {
		if got, err := s.featureFieldMiddleware(ctx, next); err != nil || got != "ran" {
			t.Errorf("core field: %v, %v", got, err)
		}
	}
}
