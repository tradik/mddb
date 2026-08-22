package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// INT-018: the agent instructions name MCP tools, and a renamed or removed
// tool would leave them telling an agent to call something that does not
// exist. DOC-001 is the precedent — the docs claimed 67 tools while the code
// defined 77, and nothing noticed.

// toolMention matches a tool name written as inline code: `hybrid_search`.
var toolMention = regexp.MustCompile("`([a-z][a-z0-9_]{3,})`")

// notTools are lowercase snake_case identifiers that appear in the
// instructions but are parameters, values or field names rather than tools.
var notTools = map[string]bool{
	"retrievalmode": true, "windowsize": true, "cachettl": true,
	"highlight": true, "limit": true, "fields": true, "alpha": true,
	"chunk": true, "window": true, "parent": true, "key": true,
	"meta.title": true, "true": true, "false": true,
}

func TestAgentInstructionsNameRealTools(t *testing.T) {
	known := map[string]bool{}
	for _, tool := range mcpBuiltinTools() {
		known[tool.Name] = true
	}
	if len(known) == 0 {
		t.Fatal("no built-in tools found — the guard would pass vacuously")
	}

	base := filepath.Join("..", "..", "integrations", "agent-instructions")
	var files []string
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdc")) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", base, err)
	}
	if len(files) == 0 {
		t.Fatalf("no instruction files under %s", base)
	}

	var mentionedAnywhere []string
	for _, path := range files {
		data, err := os.ReadFile(path) // #nosec G304 -- repo-relative path from a walk
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range toolMention.FindAllStringSubmatch(string(data), -1) {
			name := m[1]
			if notTools[strings.ToLower(name)] || !strings.Contains(name, "_") {
				continue
			}
			mentionedAnywhere = append(mentionedAnywhere, name)
			if !known[name] {
				t.Errorf("%s names %q, which is not a built-in MCP tool",
					filepath.Base(path), name)
			}
		}
	}

	// A guard that matched nothing would pass while the instructions rotted.
	if len(mentionedAnywhere) < 5 {
		t.Errorf("only %d tool mentions found (%v) — the regex is probably stale",
			len(mentionedAnywhere), mentionedAnywhere)
	}
}

// The decision tree is the point of the file; if a search tool disappears from
// it, an agent loses the guidance that made it choose correctly.
func TestAgentInstructionsCoverTheSearchTools(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "integrations", "agent-instructions", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	for _, want := range []string{
		"full_text_search", "semantic_search", "hybrid_search",
		"search_documents", "aggregate",
		"memory_start_session", "memory_recall",
		"bulk_ingest_submit", "add_document",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the instructions no longer mention %q", want)
		}
	}
}

// Generated variants must match the source, or a user pasting the Cursor rule
// gets different advice from one reading AGENTS.md.
func TestGeneratedInstructionVariantsCarryTheSourceBody(t *testing.T) {
	base := filepath.Join("..", "..", "integrations", "agent-instructions")
	source, err := os.ReadFile(filepath.Join(base, "AGENTS.md")) //nolint:gosec // G304: a fixed repo-relative path, not input
	if err != nil {
		t.Fatal(err)
	}
	// A distinctive line from the middle of the source; front matter differs
	// between variants, the body must not.
	marker := "Do not fetch a document to edit part of it."
	if !strings.Contains(string(source), marker) {
		t.Fatalf("the marker line is gone from AGENTS.md; update this test")
	}

	variants := []string{
		filepath.Join(base, "claude-code", "SKILL.md"),
		filepath.Join(base, "cursor", "mddb.mdc"),
		filepath.Join(base, "windsurf", "mddb.md"),
	}
	sort.Strings(variants)
	for _, v := range variants {
		data, err := os.ReadFile(v) //nolint:gosec // G304: v comes from the fixed list above, not from input
		if err != nil {
			t.Errorf("%s: %v (run: make agent-instructions)", filepath.Base(v), err)
			continue
		}
		if !strings.Contains(string(data), marker) {
			t.Errorf("%s does not carry the source body — regenerate it", filepath.Base(v))
		}
	}
}
