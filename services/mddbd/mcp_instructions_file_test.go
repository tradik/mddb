package main

import (
	"os"
	"path/filepath"
	"testing"
)

// #327: MCP instructions from a file, with the inline variable winning.
func TestMCPInstructionsFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(path, []byte("\nUse code_graph first.\n\nThen read the file.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MDDB_MCP_INSTRUCTIONS", "")
	t.Setenv("MDDB_MCP_INSTRUCTIONS_FILE", path)
	if got := mcpInstructionsFromEnv(); got != "Use code_graph first.\n\nThen read the file." {
		t.Errorf("from file = %q", got)
	}

	t.Setenv("MDDB_MCP_INSTRUCTIONS", "inline wins")
	if got := mcpInstructionsFromEnv(); got != "inline wins" {
		t.Errorf("both set = %q, want the inline value", got)
	}

	t.Setenv("MDDB_MCP_INSTRUCTIONS", "")
	t.Setenv("MDDB_MCP_INSTRUCTIONS_FILE", filepath.Join(t.TempDir(), "missing.md"))
	if got := mcpInstructionsFromEnv(); got != "" {
		t.Errorf("unreadable file = %q, want unset", got)
	}

	t.Setenv("MDDB_MCP_INSTRUCTIONS_FILE", "")
	if got := mcpInstructionsFromEnv(); got != "" {
		t.Errorf("neither set = %q", got)
	}

	var cfg ServerConfig
	t.Setenv("MDDB_MCP_INSTRUCTIONS_FILE", path)
	applyEnvConfig(&cfg)
	if cfg.MCP.Instructions == "" {
		t.Error("applyEnvConfig ignored MDDB_MCP_INSTRUCTIONS_FILE")
	}
}
