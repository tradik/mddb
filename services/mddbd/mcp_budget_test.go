package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Size budgets for what an agent pays to search code.
//
// An agent that answers a code question through MCP has to spend fewer
// tokens than one that uses grep, and that is decided by bytes: of each
// schema the client loads and of each answer. The ceilings below are for a
// fixed repository and fixed calls, so a change that grows an answer or a
// schema fails here instead of being found months later in a bill. Raise a
// ceiling only with the measurement that justifies it.

// toolSchemaBytes is a tool's definition as tools/list sends it.
func toolSchemaBytes(t *testing.T, name string) int {
	t.Helper()
	for _, tool := range mcpAllTools(nil) {
		if tool.Name == name {
			data, err := json.Marshal(tool)
			if err != nil {
				t.Fatal(err)
			}
			return len(data)
		}
	}
	t.Fatalf("no tool %s", name)
	return 0
}

func TestBudget_Schemas(t *testing.T) {
	for name, ceiling := range map[string]int{"code_search": 800, "code_symbol": 2000} {
		if got := toolSchemaBytes(t, name); got > ceiling {
			t.Errorf("%s schema is %d bytes, budget %d", name, got, ceiling)
		}
	}
	// A tool with an output schema must answer in structuredContent, which
	// is JSON: the line answers need these two to stay without one.
	for _, name := range []string{"code_search", "code_symbol"} {
		if _, declared := mcpToolOutputSchemas[name]; declared {
			t.Errorf("%s has an output schema; its line answer would be sent as JSON again", name)
		}
	}
	if got := len(mcpDefaultCodeInstructions); got > 600 {
		t.Errorf("default instructions are %d bytes, budget 600 (about 150 tokens)", got)
	}
}

func TestBudget_Answers(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ts := seedLineRepo(t, s)
	sym := func(extra map[string]interface{}) map[string]interface{} {
		args := map[string]interface{}{"collection": semColl, "format": "lines"}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	cases := []struct {
		label, tool string
		args        map[string]interface{}
		ceiling     int
	}{
		{"exact identifier", "code_search", map[string]interface{}{"collection": semColl, "query": "Close()", "mode": "exact"}, 420},
		{"words", "code_search", map[string]interface{}{"collection": semColl, "query": "flush"}, 130},
		{"words in a document", "code_search", map[string]interface{}{"collection": semColl, "query": "lease suspended decision"}, 300},
		{"implementations", "code_symbol", sym(map[string]interface{}{"name": "Closer", "include": []interface{}{"definitions", "implementations"}}), 220},
		{"references of one method", "code_symbol", sym(map[string]interface{}{"name": "Conn.Close", "include": []interface{}{"definitions", "references"}}), 240},
		{"callers", "code_symbol", sym(map[string]interface{}{"name": "Conn.Close", "include": []interface{}{"callers"}}), 140},
		// The JSON answers stay compact: no indentation.
		{"references as JSON", "code_symbol", map[string]interface{}{"collection": semColl, "name": "Conn.Close", "include": []interface{}{"definitions", "references"}}, 1100},
		{"passages", "full_text_search", map[string]interface{}{"collection": semColl, "query": "lease suspended decision", "format": "passages", "passage_lines": 2.0}, 2200},
		{"passages without text", "full_text_search", map[string]interface{}{"collection": semColl, "query": "Close", "format": "passages", "fields": []interface{}{"key"}, "limit": 5.0}, 1100},
	}
	for _, c := range cases {
		out, err := ts.mcpCallTool(context.Background(), c.tool, c.args)
		if err != nil {
			t.Errorf("%s: %v", c.label, err)
			continue
		}
		if len(out) > c.ceiling {
			t.Errorf("%s: answer is %d bytes, budget %d:\n%s", c.label, len(out), c.ceiling, out)
		}
		if strings.Contains(out, "\n  \"") {
			t.Errorf("%s: answer is indented JSON", c.label)
		}
		t.Logf("%-26s %4d bytes (budget %d)", c.label, len(out), c.ceiling)
	}
}
