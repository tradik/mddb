package main

import "strings"

// The composed system prompt of a scenario (#403): what mddb-chat sends the
// model, and what the panel previews. The parts come in a fixed order —
// operator prompt, guardrails, the off-topic clause, the style clause, then
// each allowed collection's responsePrompt (RAG-002) — so an operator reading
// the preview knows which setting wrote which paragraph.
//
// Skills (#404) are stored by name only; the skill store expands them later,
// so they are not part of this composition yet.

// defaultRedirectTemplate is the redirect clause when none is written.
const defaultRedirectTemplate = "That is outside what I can help with here. I can help with questions about the topics I cover — ask me one of those."

// ComposeChatPrompt builds the system prompt. responsePrompt returns a
// collection's formatting instruction, "" when it has none.
func ComposeChatPrompt(c *ChatScenario, responsePrompt func(collection string) string) string {
	parts := make([]string, 0, 5+len(c.AllowedCollections))
	if p := strings.TrimSpace(c.OperatorPrompt); p != "" {
		parts = append(parts, p)
	}
	if g := strings.TrimSpace(c.Guardrails); g != "" {
		parts = append(parts, "Guardrails — never do the following:\n"+g)
	}
	parts = append(parts, offTopicClause(c.OffTopic), styleClause(c.Style))
	for _, coll := range c.AllowedCollections {
		if rp := strings.TrimSpace(responsePrompt(coll)); rp != "" {
			parts = append(parts, "For answers drawn from the collection "+coll+":\n"+rp)
		}
	}
	return strings.Join(parts, "\n\n")
}

// offTopicClause says what to do with a question outside the scope.
func offTopicClause(o ChatOffTopic) string {
	switch o.Mode {
	case OffTopicRedirect:
		tmpl := strings.TrimSpace(o.RedirectTemplate)
		if tmpl == "" {
			tmpl = defaultRedirectTemplate
		}
		return "If a question is outside your scope, do not answer it; reply with: " + tmpl
	case OffTopicBrief:
		return "If a question is outside your scope, answer it briefly, then return to the topics you cover."
	default:
		return "If a question is outside your scope, decline politely and say which topics you can help with."
	}
}

// styleClause turns the style settings into instructions.
func styleClause(s ChatStyle) string {
	var lines []string
	switch s.Emoji {
	case EmojiSparingly:
		lines = append(lines, "Use emoji sparingly.")
	case EmojiFree:
		lines = append(lines, "Emoji are welcome.")
	default:
		lines = append(lines, "Do not use emoji.")
	}
	switch s.Verbosity {
	case VerbosityShort:
		lines = append(lines, "Keep answers short.")
	case VerbosityLong:
		lines = append(lines, "Answer thoroughly, with detail and examples.")
	}
	if s.Markdown {
		lines = append(lines, "Format answers in Markdown.")
	} else {
		lines = append(lines, "Answer in plain text, without Markdown.")
	}
	if t := strings.TrimSpace(s.CitationTemplate); t != "" {
		lines = append(lines, "Cite every source as: "+t)
	}
	return strings.Join(lines, " ")
}

// ChatScenarioPrompt composes a scenario's prompt with this server's
// collection response prompts.
func (s *Server) ChatScenarioPrompt(c *ChatScenario) string {
	return ComposeChatPrompt(c, func(coll string) string { return s.ResponsePrompt(s.Aliases.Resolve(coll), "") })
}
