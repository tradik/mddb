package main

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TEST-002. mcpCallTool is the single door through which an agent reaches all
// 80 built-in tools. Two properties matter more than any individual tool's
// behaviour: that every advertised tool is actually reachable, and that a
// read-only server refuses the ones that write.

// dispatchedToolNames reads the case labels of mcpCallTool's switch.
//
// Read from source rather than by calling each tool: invoking 80 tools needs 80
// fixtures, while the question here is only whether the switch has a case. The
// same technique guards the OpenAPI route table (DOC-010).
func dispatchedToolNames(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile("mcp_tools.go")
	if err != nil {
		t.Fatalf("read mcp_tools.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "func (s *MCPToolServer) mcpCallTool(")
	if start < 0 {
		t.Fatal("mcpCallTool not found — this guard is stale")
	}
	// The dispatcher ends at the next top-level func.
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		end = len(body) - start - 1
	}
	dispatcher := body[start : start+1+end]

	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`case "([a-z_0-9]+)"`).FindAllStringSubmatch(dispatcher, -1) {
		names[m[1]] = true
	}
	if len(names) == 0 {
		t.Fatal("found no case labels in mcpCallTool — the guard regex is stale")
	}
	return names
}

// A tool in the advertised list but missing from the dispatcher answers an
// agent with "unknown tool" — the tool exists as far as discovery is
// concerned and does not exist as far as calling is concerned.
func TestEveryAdvertisedToolIsDispatchable(t *testing.T) {
	dispatched := dispatchedToolNames(t)

	var missing []string
	for _, tool := range mcpBuiltinTools() {
		if !dispatched[tool.Name] {
			missing = append(missing, tool.Name)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%d tools are advertised but not dispatched:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// The mirror: a case with no advertised tool is dead code an agent can never
// reach, and usually the leftover of a rename.
func TestEveryDispatchedToolIsAdvertised(t *testing.T) {
	advertised := map[string]bool{}
	for _, tool := range mcpBuiltinTools() {
		advertised[tool.Name] = true
	}

	var orphaned []string
	for name := range dispatchedToolNames(t) {
		if !advertised[name] {
			orphaned = append(orphaned, name)
		}
	}
	sort.Strings(orphaned)

	if len(orphaned) > 0 {
		t.Errorf("%d tools are dispatched but never advertised:\n  %s",
			len(orphaned), strings.Join(orphaned, "\n  "))
	}
}

// Every tool must be classified, or read-only enforcement is deciding by
// default rather than by intent.
func TestEveryToolHasAReadOnlyClassification(t *testing.T) {
	var unclassified []string
	for _, tool := range mcpBuiltinTools() {
		if _, ok := mcpToolAnnotations[tool.Name]; !ok {
			unclassified = append(unclassified, tool.Name)
		}
	}
	sort.Strings(unclassified)

	if len(unclassified) > 0 {
		t.Errorf("%d tools carry no annotation, so read-only mode falls back to refusing them blindly:\n  %s",
			len(unclassified), strings.Join(unclassified, "\n  "))
	}
}

// The safety property: a read-only server must refuse every tool that writes.
// This is what stands between a replica and an agent that mutates it.
func TestReadOnlyModeRefusesWritingTools(t *testing.T) {
	s := &MCPToolServer{globalMode: ModeRead}

	writers := []string{
		"add_document", "delete_document", "add_documents_batch",
		"delete_documents_batch", "update_document", "delete_collection",
		"set_schema", "delete_schema", "restore_backup", "import_url",
		"truncate_revisions", "restore_revision", "set_collection_config",
	}
	for _, name := range writers {
		_, err := s.mcpCallTool(context.Background(), name, map[string]interface{}{})
		if err == nil {
			t.Errorf("%s was allowed on a read-only server", name)
			continue
		}
		if !strings.Contains(err.Error(), "read-only") {
			t.Errorf("%s was refused for the wrong reason: %v", name, err)
		}
	}
}

// And it must not refuse the ones that only read, or a replica becomes useless
// for the thing replicas are for.
func TestReadOnlyModeAllowsReadingTools(t *testing.T) {
	s := &MCPToolServer{globalMode: ModeRead}

	readers := []string{
		"search_documents", "semantic_search", "full_text_search",
		"hybrid_search", "get_stats", "get_document_meta", "list_schemas",
		"code_graph", "vector_stats", "get_meta_keys",
	}
	for _, name := range readers {
		if !s.isToolReadOnly(name) {
			t.Errorf("%s is classified as a writer; a read-only server would refuse it", name)
		}
	}
}

// A per-protocol override exists so MCP can be read-only on a writable server —
// handing an agent a database it cannot damage.
func TestMCPModeOverridesTheServerMode(t *testing.T) {
	s := &MCPToolServer{globalMode: ModeRW, mode: ModeRead}

	if _, err := s.mcpCallTool(context.Background(), "add_document", map[string]interface{}{}); err == nil {
		t.Error("the MCP read-only override was ignored on a writable server")
	}
}

// Custom tools wrap read-only actions, so they must survive read-only mode —
// otherwise defining one silently disables it on every replica.
func TestCustomToolsAreReadOnly(t *testing.T) {
	s := &MCPToolServer{
		globalMode: ModeRead,
		customTools: []MCPCustomToolConfig{
			{Name: "search-handbook", Action: "search_documents"},
			{Name: "find-runbooks", Action: "semantic_search"},
		},
	}
	for _, name := range []string{"search-handbook", "find-runbooks"} {
		if !s.isToolReadOnly(name) {
			t.Errorf("custom tool %q is treated as a writer", name)
		}
	}
	// A name that is neither built-in nor defined is not read-only: unknown
	// must not mean safe.
	if s.isToolReadOnly("something-nobody-defined") {
		t.Error("an unknown tool was treated as read-only")
	}
}

func TestUnknownToolIsRejected(t *testing.T) {
	s := &MCPToolServer{globalMode: ModeRW}

	_, err := s.mcpCallTool(context.Background(), "no_such_tool", map[string]interface{}{})
	if err == nil {
		t.Fatal("an unknown tool name was accepted")
	}
	if !strings.Contains(err.Error(), "no_such_tool") {
		t.Errorf("the error does not name the tool: %v", err)
	}
}

// --- argument coercion ---
//
// These turn untrusted JSON into typed arguments. An agent sending a number
// where a string belongs must get a sane default rather than a panic.

func TestArgumentCoercionHandlesWrongTypes(t *testing.T) {
	args := map[string]interface{}{
		"str":    123,
		"num":    "not a number",
		"flag":   "yes",
		"slice":  "not a slice",
		"absent": nil,
	}

	if got := mcpGetString(args, "str"); got != "" {
		t.Errorf("a non-string coerced to %q, want the empty default", got)
	}
	if got := mcpGetInt(args, "num"); got != 0 {
		t.Errorf("a non-number coerced to %d, want 0", got)
	}
	if got := mcpGetFloat(args, "num"); got != 0 {
		t.Errorf("a non-number coerced to %v, want 0", got)
	}
	// A bare string is deliberately wrapped into a one-element slice — an
	// agent sending "tag": "x" instead of ["x"] means the same thing.
	if got := mcpGetStringSlice(args, "slice"); len(got) != 1 || got[0] != "not a slice" {
		t.Errorf("a bare string coerced to %v, want a one-element slice", got)
	}
	if got := mcpGetString(args, "missing"); got != "" {
		t.Errorf("a missing key gave %q", got)
	}
	if got := mcpGetStringSlice(args, "absent"); len(got) != 0 {
		t.Errorf("a nil value gave %v", got)
	}
}

// JSON has no integers, so every number arrives as float64. A tool taking
// top_k must accept what an agent can actually send.
func TestIntegerArgumentsAcceptJSONNumbers(t *testing.T) {
	args := map[string]interface{}{"top_k": float64(42), "exact": 7}

	if got := mcpGetInt(args, "top_k"); got != 42 {
		t.Errorf("a JSON number gave %d, want 42", got)
	}
	if got := mcpGetInt(args, "exact"); got != 7 {
		t.Errorf("a Go int gave %d, want 7", got)
	}
}

// Agents send booleans as strings often enough that refusing them would break
// real callers.
func TestBooleanArgumentsAcceptCommonSpellings(t *testing.T) {
	truthy := []interface{}{true, "true", "TRUE", "1"}
	for _, v := range truthy {
		if !mcpGetBool(map[string]interface{}{"flag": v}, "flag") {
			t.Errorf("%#v was not read as true", v)
		}
	}
	falsy := []interface{}{false, "false", "0", "", nil, "maybe"}
	for _, v := range falsy {
		if mcpGetBool(map[string]interface{}{"flag": v}, "flag") {
			t.Errorf("%#v was read as true", v)
		}
	}
}

func TestMetaMapCoercion(t *testing.T) {
	args := map[string]interface{}{
		"meta": map[string]interface{}{
			"tag":    []interface{}{"a", "b"},
			"single": "one",
			"junk":   []interface{}{1, "keep"},
		},
	}
	got := mcpGetMetaMap(args, "meta")

	if tags := got["tag"]; len(tags) != 2 || tags[0] != "a" {
		t.Errorf("a list of strings became %v", tags)
	}
	if single := got["single"]; len(single) != 1 || single[0] != "one" {
		t.Errorf("a bare string became %v", single)
	}
	// A non-string entry must be dropped rather than stringified into
	// something the caller never wrote.
	if junk := got["junk"]; len(junk) != 1 || junk[0] != "keep" {
		t.Errorf("mixed types became %v", junk)
	}
	if got := mcpGetMetaMap(map[string]interface{}{"meta": "not a map"}, "meta"); len(got) != 0 {
		t.Errorf("a non-map became %v", got)
	}
}
