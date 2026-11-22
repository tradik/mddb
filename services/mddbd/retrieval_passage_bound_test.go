package main

import (
	"context"
	"strings"
	"testing"
)

// The token bound on a passage, the window under a Markdown heading, and
// `fields` with passages.

func TestPassageBound_WindowShrinksThenCuts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	// Twenty-one paragraphs of 400 bytes: 100 tokens a line.
	para := strings.Repeat("word ", 80)
	doc := addTestDoc(t, s, "notes", "long.md", "en", strings.TrimSuffix(strings.Repeat(para+"\n", 21), "\n"), nil)
	if p := passageAround("notes", &doc, 11, 10, 0, 0, "fts"); p.Truncated || p.Lines.End-p.Lines.Start != 20 {
		t.Fatalf("no bound: %+v", p.Lines)
	}
	p := passageAround("notes", &doc, 11, 10, 500, 0, "fts")
	if !p.Truncated || p.Tokens > 500 || p.Lines.Start != 10 || p.Lines.End != 12 {
		t.Fatalf("shrunk window: %+v tokens=%d truncated=%v", p.Lines, p.Tokens, p.Truncated)
	}
	// The hit line alone is over the bound: it is cut, on a rune, and marked.
	one := addTestDoc(t, s, "notes", "one.md", "en", "a\nx"+strings.Repeat("é", 400)+"\nb", nil)
	p = passageAround("notes", &one, 2, 5, 50, 0, "fts")
	if !p.Truncated || p.Lines.Start != 2 || p.Lines.End != 2 || !strings.HasSuffix(p.Content, cutMark) ||
		p.Content != "x"+strings.Repeat("é", 99)+cutMark {
		t.Fatalf("cut line: %+v %q", p.Lines, p.Content)
	}
	if (PassageOptions{}).tokenCap() != defaultPassageTokens || (PassageOptions{PassageTokens: 1 << 20}).tokenCap() != maxPassageTokens {
		t.Fatal("tokenCap defaults")
	}
	if err := (PassageOptions{PassageTokens: -1}).ValidatePassageOptions(); err == nil {
		t.Fatal("negative passageTokens accepted")
	}
}

func TestPassageBound_SearchSurfaces(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		ts := seedLineRepo(t, s)
		search := func(args map[string]interface{}) PassagesResponse {
			args["collection"], args["query"], args["format"] = semColl, "lease suspended decision", "passages"
			return decodePassages(t, []byte(lineTool(t, ts, "full_text_search", args)))
		}
		// The heading's section, bounded by default: the paragraph is 420
		// tokens and the window around it would be more.
		got := search(map[string]interface{}{})
		if len(got.Passages) != 1 || got.Passages[0].Lines.Start != 5 || got.Passages[0].Tokens > defaultPassageTokens ||
			!strings.HasPrefix(got.Passages[0].Content, "## Decision") {
			t.Fatalf("default bound: %+v", got.Passages)
		}
		// A lower bound cuts further and says so.
		got = search(map[string]interface{}{"passage_tokens": 40.0})
		if p := got.Passages[0]; !p.Truncated || p.Tokens > 42 {
			t.Fatalf("passage_tokens: %+v", p)
		}
		// fields asks for the place without the text; include_content
		// brings the text back.
		got = search(map[string]interface{}{"fields": []interface{}{"key"}})
		if p := got.Passages[0]; p.Content != "" || p.Tokens != 0 || p.Path != "docs/adr.md" {
			t.Fatalf("fields: %+v", p)
		}
		got = search(map[string]interface{}{"fields": []interface{}{"key"}, "include_content": true})
		if got.Passages[0].Content == "" {
			t.Fatal("include_content with fields dropped the text")
		}
	})
}

func TestPassageBound_ChunksOnlyWhenAsked(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	doc := addTestDoc(t, s, "notes", "c.md", "en", strings.Repeat("chunk text ", 100), nil)
	hits := []passageSource{{doc: doc, chunkText: doc.ContentMD, startLine: 1, endLine: 1, bestChunk: -1}}
	if p := s.buildPassages("notes", hits, PassageOptions{}, "vector")[0]; p.Truncated || p.Tokens < 200 {
		t.Fatalf("a chunk is bounded by the chunk size, not by default: %+v", p)
	}
	if p := s.buildPassages("notes", hits, PassageOptions{PassageTokens: 20}, "vector")[0]; !p.Truncated || p.Tokens > 22 {
		t.Fatalf("explicit bound on a chunk: %+v", p)
	}
}

func TestDefaultInstructions(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ctx := context.Background()
	handler := func(instructions string) *MCPHandler {
		return NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, instructions, ModeRead, "")
	}
	// No code anywhere: a document store or a chat is told nothing.
	addTestDoc(t, s, "kb", "faq.md", "en", "# FAQ\n\nQuestions.\n", nil)
	if got := handler("").instructionsFor(ctx); got != "" {
		t.Fatalf("no code: %q", got)
	}
	// The answer is cached; a collection of code shows after it expires.
	addTestDoc(t, s, "repo", "main.go", "en", "package main\n\nfunc main() {}\n", nil)
	if got := handler("").instructionsFor(ctx); got != "" {
		t.Fatalf("cached probe: %q", got)
	}
	s.codeProbe = codeProbe{}
	if got := handler("").instructionsFor(ctx); got != mcpDefaultCodeInstructions {
		t.Fatalf("code: %q", got)
	}
	// The operator's own win, "none" turns the default off, and a key
	// without the code toolset never sees it.
	if got := handler("use it well").instructionsFor(ctx); got != "use it well" {
		t.Fatalf("operator's: %q", got)
	}
	if got := handler(mcpInstructionsNone).instructionsFor(ctx); got != "" {
		t.Fatalf("none: %q", got)
	}
	if got := handler("").instructionsFor(withKeyToolsets(ctx, map[string]bool{ToolsetSearch: true})); got != "" {
		t.Fatalf("no code toolset: %q", got)
	}
	// A client that is not the server itself has no collections to look at.
	if got := covDHandler().defaultInstructions(ctx); got != "" {
		t.Fatalf("remote client: %q", got)
	}
}
