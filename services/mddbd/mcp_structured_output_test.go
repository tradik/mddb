package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// #461: a tool that declares an outputSchema answers every successful call
// with structuredContent that validates against it, in every output form, on
// every protocol path. The schemas are checked against real answers here, so a
// schema that drifts from what the tool returns fails a test, not a client.

// schemaErrors validates v against a JSON Schema using the keywords the output
// schemas use: type (a name or a list of names), properties, items,
// additionalProperties as a schema, and required. It returns one line per
// mismatch, each naming the path. Numbers are json.Number, as a client that
// keeps integers exact decodes them.
func schemaErrors(path string, schema map[string]any, v any) []string {
	if types, ok := schema["type"]; ok && !schemaTypeMatches(types, v) {
		return []string{fmt.Sprintf("%s: %s is not of type %v", path, jsonKind(v), types)}
	}
	var errs []string
	switch val := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		for _, req := range schemaRequired(schema) {
			if _, ok := val[req]; !ok {
				errs = append(errs, fmt.Sprintf("%s: required property %q is missing", path, req))
			}
		}
		extra, _ := schema["additionalProperties"].(map[string]any)
		for k, child := range val {
			if sub, ok := props[k].(map[string]any); ok {
				errs = append(errs, schemaErrors(path+"."+k, sub, child)...)
			} else if extra != nil {
				errs = append(errs, schemaErrors(path+"."+k, extra, child)...)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, child := range val {
				errs = append(errs, schemaErrors(fmt.Sprintf("%s[%d]", path, i), items, child)...)
			}
		}
	}
	return errs
}

// schemaRequired is the schema's required list, in either Go form.
func schemaRequired(schema map[string]any) []string {
	switch req := schema["required"].(type) {
	case []string:
		return req
	case []any:
		out := make([]string, 0, len(req))
		for _, r := range req {
			out = append(out, fmt.Sprint(r))
		}
		return out
	}
	return nil
}

// schemaTypeMatches reports whether v is one of the named JSON types.
func schemaTypeMatches(types any, v any) bool {
	switch t := types.(type) {
	case string:
		return jsonTypeIs(t, v)
	case []string:
		for _, name := range t {
			if jsonTypeIs(name, v) {
				return true
			}
		}
	case []any:
		for _, name := range t {
			if s, ok := name.(string); ok && jsonTypeIs(s, v) {
				return true
			}
		}
	}
	return false
}

// jsonTypeIs reports whether v is of the JSON Schema type name.
func jsonTypeIs(name string, v any) bool {
	kind := jsonKind(v)
	switch name {
	case "number":
		return kind == "integer" || kind == "number"
	default:
		return kind == name
	}
}

// jsonKind names the JSON type of a value decoded with UseNumber.
func jsonKind(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if _, err := val.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	}
	return fmt.Sprintf("%T", v)
}

func TestSchemaErrors(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []string{"id"},
		"properties": map[string]any{
			"id":    map[string]any{"type": "string"},
			"n":     map[string]any{"type": "integer"},
			"score": map[string]any{"type": "number"},
			"tags":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"any":   map[string]any{"type": []any{"string", "null"}},
			"pair":  map[string]any{"type": []string{"boolean", "integer"}},
			"free":  map[string]any{},
			"by": map[string]any{"type": "object", "required": []any{"x"},
				"additionalProperties": map[string]any{"type": "integer"}},
		},
	}
	good := `{"id":"a","n":1,"score":0.5,"tags":["x"],"any":null,"pair":true,"free":[1],"by":{"x":2},"other":{}}`
	if errs := schemaErrors("$", schema, decodeExact(t, good)); errs != nil {
		t.Fatalf("valid document refused: %v", errs)
	}
	for bad, want := range map[string]string{
		`{"n":1}`:                     `required property "id"`,
		`{"id":1}`:                    "$.id: integer is not of type string",
		`{"id":"a","n":1.5}`:          "$.n: number is not of type integer",
		`{"id":"a","score":"x"}`:      "$.score: string is not of type number",
		`{"id":"a","tags":[true]}`:    "$.tags[0]: boolean",
		`{"id":"a","any":3}`:          "$.any: integer",
		`{"id":"a","pair":"s"}`:       "$.pair: string",
		`{"id":"a","by":{"x":"y"}}`:   "$.by.x: string",
		`{"id":"a","by":{}}`:          `required property "x"`,
		`[]`:                          "$: array is not of type object",
		`{"id":"a","tags":{"a":"b"}}`: "$.tags: object",
	} {
		errs := schemaErrors("$", schema, decodeExact(t, bad))
		if len(errs) != 1 || !strings.Contains(errs[0], want) {
			t.Errorf("%s: errors %v, want one containing %q", bad, errs, want)
		}
	}
	if schemaTypeMatches(42, "x") || jsonKind(struct{}{}) != "struct {}" {
		t.Fatal("unknown type keyword or Go value matched")
	}
}

// decodeExact decodes JSON keeping numbers as json.Number.
func decodeExact(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("not JSON: %v: %s", err, s)
	}
	return v
}

// roundTrip is v as a client receives it: encoded, then decoded exactly.
func roundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return decodeExact(t, string(b))
}

// outputCase is one call of a tool with an output schema.
type outputCase struct {
	name     string
	args     map[string]any
	maxLimit int  // the server's result cap for this call; 0 keeps the default
	empty    bool // run on a server without any collection
}

// outputSchemaCases holds, for every tool with an output schema, calls that
// between them produce each of its output forms. A tool given a schema without
// an entry here fails TestMCPOutputSchemas_StructuredContent.
func outputSchemaCases() map[string][]outputCase {
	q := func(extra map[string]any) map[string]any {
		out := map[string]any{"collection": "gold", "query": "pump water"}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	group := map[string]any{"key": "category", "per_group": 1}
	return map[string][]outputCase{
		"get_stats": {
			{name: "stats", args: map[string]any{}},
			{name: "empty", args: map[string]any{}, empty: true},
		},
		"vector_stats": {
			{name: "stats", args: map[string]any{}},
			{name: "empty", args: map[string]any{}, empty: true},
		},
		"get_checksum": {
			{name: "checksum", args: map[string]any{"collection": "gold"}},
			{name: "empty", args: map[string]any{"collection": "nothing"}},
		},
		"classify_document": {
			{name: "text", args: map[string]any{"text": "the pump seal leaks water", "labels": []any{"pump", "budget"}}},
			{name: "document", args: map[string]any{"collection": "gold", "key": "manual", "lang": "en", "labels": []any{"pump", "budget"}, "top_k": 1}},
			{name: "none-above", args: map[string]any{"text": "zzqx", "labels": []any{"pump"}, "multi": true, "threshold": 0.99}},
		},
		"aggregate": {
			{name: "facets", args: map[string]any{"collection": "gold", "facets": []any{"category"},
				"histograms": []any{map[string]any{"field": "addedAt", "interval": "day"}}}},
			{name: "bare", args: map[string]any{"collection": "gold"}},
			{name: "empty", args: map[string]any{"collection": "nothing", "facets": []any{"category"}}},
		},
		"search_documents": {
			{name: "full", args: map[string]any{"collection": "gold", "limit": 3}},
			{name: "content", args: map[string]any{"collection": "gold", "include_content": true}},
			{name: "projected", args: map[string]any{"collection": "gold", "limit": 3, "fields": []any{"title"}}},
			{name: "passages", args: map[string]any{"collection": "gold", "limit": 3, "format": "passages"}},
			{name: "capped", args: map[string]any{"collection": "gold", "limit": 3}, maxLimit: 2},
			{name: "capped-passages", args: map[string]any{"collection": "gold", "limit": 3, "format": "passages"}, maxLimit: 2},
			{name: "under-cap", args: map[string]any{"collection": "gold", "limit": 2}, maxLimit: 2},
			{name: "no-content", args: map[string]any{"collection": "gold", "include_content": false}},
			{name: "empty-collection", args: map[string]any{"collection": "nothing"}},
			{name: "no-match", args: map[string]any{"collection": "gold", "filter_meta": map[string]any{"category": []any{"none"}}}},
			{name: "empty-passages", args: map[string]any{"collection": "nothing", "format": "passages"}},
		},
		"full_text_search": {
			{name: "full", args: q(map[string]any{"limit": 3})},
			{name: "fuzzy", args: q(map[string]any{"limit": 3, "fuzzy": 1})},
			{name: "projected", args: q(map[string]any{"limit": 3, "fields": []any{"title"}})},
			{name: "passages", args: q(map[string]any{"limit": 3, "format": "passages"})},
			{name: "grouped", args: q(map[string]any{"limit": 3, "group_by": group})},
			{name: "capped", args: q(map[string]any{"limit": 3}), maxLimit: 2},
			{name: "capped-passages", args: q(map[string]any{"limit": 3, "format": "passages"}), maxLimit: 2},
			{name: "under-cap", args: q(map[string]any{"limit": 2}), maxLimit: 2},
			{name: "highlight", args: q(map[string]any{"highlight": true, "facet_by": []any{"category"}, "lang": "en"})},
			{name: "highlight-projected", args: q(map[string]any{"highlight": true, "fields": []any{"title"}, "lang": "en"})},
			{name: "no-content", args: q(map[string]any{"include_content": false})},
			{name: "images", args: q(map[string]any{"include_images": true})},
			{name: "no-match", args: q(map[string]any{"query": "zzqx"})},
			{name: "wildcard", args: q(map[string]any{"query": "pum*", "mode": "wildcard"})},
			{name: "phrase", args: q(map[string]any{"query": "pump seal", "mode": "phrase", "fields": []any{"title"}})},
			{name: "no-match-projected", args: q(map[string]any{"query": "zzqx", "fields": []any{"title"}})},
			{name: "no-match-passages", args: q(map[string]any{"query": "zzqx", "format": "passages"})},
		},
		"semantic_search": {
			{name: "full", args: q(map[string]any{"top_k": 3})},
			{name: "content", args: q(map[string]any{"top_k": 3, "include_content": true, "explain": true})},
			{name: "projected", args: q(map[string]any{"top_k": 3, "fields": []any{"title"}})},
			{name: "passages", args: q(map[string]any{"top_k": 3, "format": "passages"})},
			{name: "grouped", args: q(map[string]any{"top_k": 3, "group_by": group})},
			{name: "chunks", args: q(map[string]any{"top_k": 3, "retrieval_mode": "chunk"})},
			{name: "capped", args: q(map[string]any{"top_k": 3}), maxLimit: 2},
			{name: "capped-passages", args: q(map[string]any{"top_k": 3, "format": "passages"}), maxLimit: 2},
			{name: "under-cap", args: q(map[string]any{"top_k": 2}), maxLimit: 2},
			{name: "no-content", args: q(map[string]any{"include_content": false})},
			{name: "images", args: q(map[string]any{"include_images": true})},
			{name: "no-match", args: q(map[string]any{"threshold": 0.99})},
			{name: "empty-collection", args: map[string]any{"collection": "nothing", "query": "pump"}},
			{name: "empty-projected", args: map[string]any{"collection": "nothing", "query": "pump", "fields": []any{"title"}}},
		},
		"hybrid_search": {
			{name: "full", args: q(map[string]any{"top_k": 3, "explain": true})},
			{name: "default", args: q(nil)},
			{name: "rrf", args: q(map[string]any{"top_k": 3, "strategy": "rrf"})},
			{name: "projected", args: q(map[string]any{"top_k": 3, "fields": []any{"title"}})},
			{name: "passages", args: q(map[string]any{"top_k": 3, "format": "passages"})},
			{name: "grouped", args: q(map[string]any{"top_k": 3, "group_by": group})},
			{name: "capped", args: q(map[string]any{"top_k": 3}), maxLimit: 2},
			{name: "capped-passages", args: q(map[string]any{"top_k": 3, "format": "passages"}), maxLimit: 2},
			{name: "under-cap", args: q(map[string]any{"top_k": 2}), maxLimit: 2},
			{name: "facets", args: q(map[string]any{"facet_by": []any{"category"}, "include_images": true})},
			{name: "no-match", args: q(map[string]any{"query": "zzqx", "threshold": 0.99})},
			{name: "empty-collection", args: map[string]any{"collection": "nothing", "query": "pump"}},
		},
	}
}

// structuredGold is the golden collection plus a document with metadata, so
// facets and group_by have a key to work on.
func structuredGold(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := goldenServer(t)
	addTestDoc(t, s, "gold", "impeller", "en", "# Impeller\n\nThe pump impeller moves water.",
		map[string][]string{"category": {"pump"}, "title": {"Impeller"}})
	deadline := time.Now().Add(10 * time.Second)
	for s.IndexingStatus("gold").PendingEmbeddings > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return s, cleanup
}

// checkToolResult requires a successful result whose structuredContent equals
// the text answer and validates against the tool's declared schema. It marks
// in seen each declared property the answer carries.
func checkToolResult(t *testing.T, label string, schema map[string]any, res any, seen map[string]bool) {
	t.Helper()
	obj, _ := res.(map[string]any)
	if obj == nil || obj["isError"] == true {
		t.Errorf("%s: not a successful result: %v", label, res)
		return
	}
	structured, ok := obj["structuredContent"].(map[string]any)
	if !ok {
		t.Errorf("%s: no structuredContent: %v", label, res)
		return
	}
	content, _ := obj["content"].([]any)
	if len(content) == 0 {
		t.Errorf("%s: no content: %v", label, res)
		return
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !reflect.DeepEqual(decodeExact(t, text), any(structured)) {
		t.Errorf("%s: structuredContent differs from the text answer:\n%s", label, text)
	}
	if errs := schemaErrors("$", schema, structured); errs != nil {
		t.Errorf("%s: answer does not match the declared outputSchema:\n  %s\n%s", label, strings.Join(errs, "\n  "), text)
	}
	markDeclared("$", schema, structured, seen)
}

// markDeclared marks in seen the path of every declared property v carries.
func markDeclared(path string, schema map[string]any, v any, seen map[string]bool) {
	switch val := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		extra, _ := schema["additionalProperties"].(map[string]any)
		for k, child := range val {
			if sub, ok := props[k].(map[string]any); ok {
				seen[path+"."+k] = true
				markDeclared(path+"."+k, sub, child, seen)
			} else if extra != nil {
				markDeclared(path+".*", extra, child, seen)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for _, child := range val {
				markDeclared(path+"[]", items, child, seen)
			}
		}
	}
}

// declaredPaths lists the path of every property a schema declares, in the
// form markDeclared records them.
func declaredPaths(path string, schema map[string]any) []string {
	var out []string
	props, _ := schema["properties"].(map[string]any)
	for k, sub := range props {
		out = append(out, path+"."+k)
		out = append(out, declaredPaths(path+"."+k, sub.(map[string]any))...)
	}
	if items, ok := schema["items"].(map[string]any); ok {
		out = append(out, declaredPaths(path+"[]", items)...)
	}
	if extra, ok := schema["additionalProperties"].(map[string]any); ok {
		out = append(out, declaredPaths(path+".*", extra)...)
	}
	return out
}

// mcpSurfaces is one server as each tools/call path reaches it.
type mcpSurfaces struct {
	server *Server
	rpc    *MCPHandler
	http   *MCPHTTPServer
}

func newMCPSurfaces(s *Server) mcpSurfaces {
	return mcpSurfaces{
		server: s,
		rpc:    NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{Name: "mddbd"}, "", ModeRW, ""),
		http:   s.newMCPHTTPServer(),
	}
}

// TestMCPOutputSchemas_StructuredContent runs every call in outputSchemaCases
// through tools/call (legacy and 2026-07-28 revisions) and POST /tools/call.
// Each answer must validate, and each declared property must turn up in at
// least one answer, so a schema cannot name a field under a name the tool
// never uses.
func TestMCPOutputSchemas_StructuredContent(t *testing.T) {
	withSupportedVersions(t, MCPModernProtocolVersion, MCPProtocolVersion)
	gold, cleanup := structuredGold(t)
	defer cleanup()
	empty, cleanupEmpty := newHandlerTestServer(t)
	defer cleanupEmpty()
	surfaces := map[bool]mcpSurfaces{false: newMCPSurfaces(gold), true: newMCPSurfaces(empty)}
	cases := outputSchemaCases()

	var withSchema []string
	for _, tool := range mcpAllTools(nil) {
		if tool.OutputSchema != nil {
			withSchema = append(withSchema, tool.Name)
		}
	}
	sort.Strings(withSchema)
	if len(withSchema) != len(mcpToolOutputSchemas) {
		t.Fatalf("tools with an outputSchema %v, want all of %d", withSchema, len(mcpToolOutputSchemas))
	}
	for _, name := range withSchema {
		calls := cases[name]
		if len(calls) == 0 {
			t.Errorf("%s declares an outputSchema and has no case in outputSchemaCases", name)
			continue
		}
		schema, _ := roundTrip(t, mcpToolOutputSchemas[name]).(map[string]any)
		seen := map[string]bool{}
		for _, c := range calls {
			on := surfaces[c.empty]
			on.server.SearchMaxLimit = c.maxLimit
			label := name + "/" + c.name
			params := func() map[string]any { return map[string]any{"name": name, "arguments": c.args} }

			legacy := on.rpc.Handle(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params()})
			checkToolResult(t, label+" (legacy)", schema, roundTrip(t, legacy["result"]), seen)

			modern := modernResult(t, on.rpc, modernRequest("tools/call", params()))
			checkToolResult(t, label+" (2026-07-28)", schema, roundTrip(t, modern), seen)

			body, _ := json.Marshal(params())
			rec := httptest.NewRecorder()
			on.http.handleToolCall(rec, httptest.NewRequest(http.MethodPost, "/mcp/tools/call", bytes.NewReader(body)))
			if rec.Code != http.StatusOK {
				t.Errorf("%s (POST /tools/call): %d %s", label, rec.Code, rec.Body.String())
				continue
			}
			checkToolResult(t, label+" (POST /tools/call)", schema, decodeExact(t, rec.Body.String()), seen)
			on.server.SearchMaxLimit = 0
		}
		for _, p := range declaredPaths("$", schema) {
			if !seen[p] {
				t.Errorf("%s: the outputSchema declares %s, which no answer carried", name, p)
			}
		}
	}
}

// A tool without a schema keeps the #368 behaviour, and a schema tool answers
// with an object even when its text is not one.
func TestMCPStructuredContent(t *testing.T) {
	capped := `{"results":[],"limitCapped":true,"limit":2}`
	if got := mcpStructuredContent("autocomplete", capped); !reflect.DeepEqual(got, map[string]any{"limitCapped": true, "limit": 2}) {
		t.Fatalf("capped tool without a schema: %v", got)
	}
	if got := mcpStructuredContent("autocomplete", `{"results":[]}`); got != nil {
		t.Fatalf("uncapped tool without a schema: %v", got)
	}
	for _, text := range []string{"plain prose", "[1,2]", "null", ""} {
		if got := mcpStructuredContent("get_stats", text); !reflect.DeepEqual(got, map[string]any{"text": text}) {
			t.Errorf("schema tool, text %q: %v", text, got)
		}
	}
	got := mcpStructuredContent("get_checksum", `{"documentCount":9007199254740993,"checksum":"ab"}`)
	if got["documentCount"] != json.Number("9007199254740993") {
		t.Fatalf("int64 not kept exact: %#v", got["documentCount"])
	}

	res := mcpToolResult("autocomplete", `{"results":[]}`)
	if _, ok := res["structuredContent"]; ok {
		t.Fatalf("uncapped tool without a schema has structuredContent: %v", res)
	}
	res = mcpToolResult("get_checksum", `{"checksum":"ab"}`)
	if res["structuredContent"].(map[string]any)["checksum"] != "ab" {
		t.Fatalf("schema tool: %v", res)
	}
	if res["content"].([]map[string]any)[0]["text"] != `{"checksum":"ab"}` {
		t.Fatalf("text changed: %v", res)
	}
}
