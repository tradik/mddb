package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Per-collection response prompt (RAG-002).
//
// The instruction for how to format a RAG answer lives in the client today: a
// per-scenario system prompt in mddb-chat's TOML, and nothing at all for MCP
// agents. So a collection of API docs that wants code blocks and key references,
// and a collection of runbooks that wants numbered steps, are indistinguishable
// to every consumer — each client has to know, separately, what each collection
// expects.
//
// The prompt belongs with the data. A collection carries its own formatting
// instruction, and every consumer picks it up without being told.

// MaxResponsePromptBytes caps the stored instruction.
//
// Four kilobytes is a generous formatting instruction and a small fraction of
// any model's context. The cap exists because this text is prepended to prompts
// automatically: an unbounded value would silently eat the context budget the
// answer needs, and the caller would see a worse answer rather than an error.
const MaxResponsePromptBytes = 4096

// ValidateResponsePrompt checks a prompt before it is stored.
func ValidateResponsePrompt(prompt string) error {
	if prompt == "" {
		return nil
	}
	if n := len(prompt); n > MaxResponsePromptBytes {
		return fmt.Errorf("responsePrompt is %d bytes, over the %d-byte limit", n, MaxResponsePromptBytes)
	}
	// Stored text travels through JSON, the binlog and every client; invalid
	// UTF-8 would break some of them in ways that are hard to trace back here.
	if !utf8.ValidString(prompt) {
		return fmt.Errorf("responsePrompt is not valid UTF-8")
	}
	return nil
}

// ResponsePrompt returns a collection's formatting instruction with its
// template variables expanded, or "" when none is configured.
//
// Variables are expanded through the same ExpandTemplate the automation rules
// use — a second template syntax would be a second thing to document and to get
// subtly wrong.
func (s *Server) ResponsePrompt(collection, query string) string {
	if s == nil || s.CollectionManager == nil || collection == "" {
		return ""
	}
	cfg, found := s.CollectionManager.Get(collection)
	if !found || cfg == nil || cfg.ResponsePrompt == "" {
		return ""
	}
	return ExpandTemplate(cfg.ResponsePrompt, map[string]string{
		"collection": collection,
		"query":      query,
	})
}

// ComposeSystemPrompt joins an operator's own instruction with the collection's.
//
// Order matters and is deliberate: the operator's prompt is policy — who the
// assistant is, what it may say — while the collection's is about the shape of
// its data. Policy first, so a collection cannot talk its way past it by
// starting with "ignore your previous instructions"; a later line arguing with
// an earlier one is a much weaker position than the reverse.
func ComposeSystemPrompt(operatorPrompt, collectionPrompt string) string {
	operatorPrompt = strings.TrimSpace(operatorPrompt)
	collectionPrompt = strings.TrimSpace(collectionPrompt)

	switch {
	case collectionPrompt == "":
		return operatorPrompt
	case operatorPrompt == "":
		return collectionPrompt
	default:
		return operatorPrompt + "\n\n" + collectionPrompt
	}
}
