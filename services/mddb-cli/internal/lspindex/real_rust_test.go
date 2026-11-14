//go:build lspreal

package lspindex

// rust-analyzer against this repo's Rust service and a broken crate
// (CODE-018): local only, like the other real-server tests. rust-analyzer
// must be on PATH (rustup component add rust-analyzer) or in MDDB_LSP_RUST.

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestRealRustAnalyzer indexes services/mddb-chat (118 files). Its two
// traits are implemented through #[async_trait] (a proc-macro) and in test
// modules, so the implementations need the expanded crate.
func TestRealRustAnalyzer(t *testing.T) {
	res := realRun(t, "../../../mddb-chat", ".rs", "rust")
	want := map[string][]string{
		"src/llm/provider.rs#LlmProvider": {"src/chat/testing/llm.rs:ScriptedLlm", "src/llm/anthropic.rs:AnthropicProvider",
			"src/llm/openai.rs:OpenAiProvider"},
		"src/chat/tools.rs#DocSearch": {"src/chat/handler.rs:MddbClient", "src/chat/mcp/call_tests.rs:FakeMddbd",
			"src/chat/mcp/call_tests.rs:SearchOnly", "src/chat/tools/tests.rs:FakeSearch"},
		"src/llm/provider.rs#LlmProvider.chat_with_tools": {"src/chat/testing/llm.rs:ScriptedLlm.chat_with_tools",
			"src/llm/anthropic.rs:AnthropicProvider.chat_with_tools", "src/llm/openai.rs:OpenAiProvider.chat_with_tools"},
	}
	for target, w := range want {
		got := implsOf(res, target)
		slices.Sort(got)
		t.Logf("%s: %v", target, got)
		if !slices.Equal(got, w) {
			t.Errorf("%s: implementations %q, want %q", target, got, w)
		}
	}
}

// TestRealRustAnalyzerRefused: a Cargo.toml cargo metadata cannot read.
func TestRealRustAnalyzerRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"broken\"\nversion =\n")
	writeFile(t, filepath.Join(root, "src", "lib.rs"), "pub fn f() -> u8 {\n    1\n}\n")
	spec, err := Spec("rust")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Index(context.Background(), root, []string{"src/lib.rs"}, spec, Options{Timeout: 5 * time.Minute})
	if !errors.Is(err, ErrWorkspaceNotLoaded) {
		t.Fatalf("err = %v", err)
	}
	t.Logf("refused: %.400s", err)
}
