package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// covDHealthClient answers health and fails stats, which is all the resource
// paths below need from a client.
type covDHealthClient struct {
	MCPClient
}

func (covDHealthClient) Health(context.Context) (*MCPHealth, error) {
	return &MCPHealth{Status: "healthy", Mode: "read"}, nil
}

func (covDHealthClient) Stats(context.Context) (*MCPStats, error) {
	return nil, errors.New("stats unavailable")
}

func covDHandler() *MCPHandler {
	return NewMCPHandlerWithConfig(covDHealthClient{}, nil, MCPServerInfo{
		Description: "test server", Vendor: "tradik", Homepage: "https://example.com",
	}, "use it well", ModeRead, "")
}

func TestCovDHandlerLegacyMethods(t *testing.T) {
	h := covDHandler()

	init := h.Handle(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]interface{}{}})
	res, _ := init["result"].(map[string]interface{})
	info, _ := res["serverInfo"].(map[string]interface{})
	if info["name"] != "mddbd" || info["description"] != "test server" || info["vendor"] != "tradik" || info["homepage"] != "https://example.com" {
		t.Errorf("serverInfo = %v", info)
	}
	if res["instructions"] != "use it well" {
		t.Errorf("instructions = %v", res["instructions"])
	}

	list := h.Handle(map[string]interface{}{"id": 2, "method": "resources/list"})
	if r, _ := list["result"].(map[string]interface{}); len(r["resources"].([]MCPResource)) == 0 {
		t.Errorf("resources = %v", list)
	}

	tmpl := h.Handle(map[string]interface{}{"id": 2, "method": "resources/templates/list"})
	if r, _ := tmpl["result"].(map[string]interface{}); len(r["resourceTemplates"].([]MCPResourceTemplate)) == 0 {
		t.Errorf("templates = %v", tmpl)
	}

	read := h.Handle(map[string]interface{}{"id": 3, "method": "resources/read", "params": map[string]interface{}{"uri": "mddb://health"}})
	contents := read["result"].(map[string]interface{})["contents"].([]map[string]interface{})
	if contents[0]["uri"] != "mddb://health" || !strings.Contains(contents[0]["text"].(string), `"healthy"`) {
		t.Errorf("resources/read = %v", read)
	}

	bad := h.Handle(map[string]interface{}{"id": 4, "method": "resources/read", "params": map[string]interface{}{"uri": "ftp://x"}})
	if e, _ := bad["error"].(map[string]interface{}); e == nil || !strings.Contains(e["message"].(string), "unsupported uri scheme") {
		t.Errorf("bad resource = %v", bad)
	}

	unknown := h.Handle(map[string]interface{}{"id": 5, "method": "no/such"})
	if e, _ := unknown["error"].(map[string]interface{}); e == nil || e["code"] != mcpErrMethodNotFound {
		t.Errorf("unknown method = %v", unknown)
	}
}

func TestCovDHandlerToolsListCursor(t *testing.T) {
	h := covDHandler()
	all := h.Handle(map[string]interface{}{"id": 1, "method": "tools/list"})["result"].(map[string]interface{})["tools"].([]MCPTool)
	if len(all) < 2 {
		t.Fatalf("need at least two tools, got %d", len(all))
	}
	page := h.Handle(map[string]interface{}{"id": 2, "method": "tools/list", "params": map[string]interface{}{"cursor": all[0].Name}})
	rest := page["result"].(map[string]interface{})["tools"].([]MCPTool)
	if len(rest) != len(all)-1 || rest[0].Name != all[1].Name {
		t.Errorf("after cursor %q got %d tools starting %q", all[0].Name, len(rest), rest[0].Name)
	}
}

func TestCovDStdioNotifier(t *testing.T) {
	var out bytes.Buffer
	notify := mcpStdioNotifier(&out)
	notify(map[string]interface{}{"method": "notifications/message"})
	if out.String() != `{"method":"notifications/message"}`+"\n" {
		t.Errorf("notification line = %q", out.String())
	}
	out.Reset()
	notify(map[string]interface{}{"bad": make(chan int)}) // cannot be encoded
	if out.Len() != 0 {
		t.Errorf("unencodable notification wrote %q", out.String())
	}
}

func TestCovDServeMCPStdio(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	in := strings.Join([]string{
		``,          // blank: skipped
		`{not json`, // parse error: logged, skipped
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	s.serveMCPStdio(strings.NewReader(in), &out)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var msgs []map[string]interface{}
	for _, l := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("output line is not JSON: %q", l)
		}
		msgs = append(msgs, m)
	}
	// ping, then the error log notification for the failing call, then its result.
	if len(msgs) != 3 {
		t.Fatalf("got %d lines: %s", len(msgs), out.String())
	}
	if msgs[0]["id"] != float64(1) {
		t.Errorf("first response = %v", msgs[0])
	}
	if msgs[1]["method"] != "notifications/message" {
		t.Errorf("expected a log notification before the result, got %v", msgs[1])
	}
	if r, _ := msgs[2]["result"].(map[string]interface{}); msgs[2]["id"] != float64(2) || r["isError"] != true {
		t.Errorf("tools/call result = %v", msgs[2])
	}

	// A line over the 4 MB buffer stops the loop with a scanner error.
	out.Reset()
	s.serveMCPStdio(strings.NewReader(strings.Repeat("x", 5<<20)), &out)
	if out.Len() != 0 {
		t.Errorf("oversized line produced output %q", out.String()[:min(out.Len(), 80)])
	}
}

// TestCovDRunMCPStdio binds the loop to the process's stdin and stdout, so it
// must not run in parallel with anything that writes to them.
func TestCovDRunMCPStdio(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()

	dir := t.TempDir()
	inPath := filepath.Join(dir, "in")
	if err := os.WriteFile(inPath, []byte(`{"jsonrpc":"2.0","id":9,"method":"ping"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(inPath) // #nosec G304 -- test temp file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(filepath.Join(dir, "out")) // #nosec G304 -- test temp file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()

	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, out
	s.runMCPStdio()
	os.Stdin, os.Stdout = oldIn, oldOut

	got, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"id":9`) || !strings.Contains(string(got), `"result":{}`) {
		t.Errorf("stdout = %q", got)
	}
}

func TestCovDHandlerToolCallOutsideToolset(t *testing.T) {
	t.Setenv("MDDB_MCP_TOOLSETS", "")
	h := covDHandler()
	ctx := withKeyToolsets(context.Background(), map[string]bool{ToolsetSearch: true})
	resp := h.HandleContext(ctx, map[string]interface{}{"id": 1, "method": "tools/call",
		"params": map[string]interface{}{"name": "get_stats", "arguments": map[string]interface{}{}}})
	if e, _ := resp["error"].(map[string]interface{}); e == nil || e["code"] != mcpErrMethodNotFound {
		t.Errorf("restricted tools/call = %v", resp)
	}
}
