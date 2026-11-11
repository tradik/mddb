package main

import (
	"fmt"
	"strings"
)

// The composed system prompt of a scenario (#403): what mddb-chat sends the
// model, and what the panel previews. The parts come in a fixed order —
// operator prompt, guardrails, the off-topic clause, the scenario's skills
// (#404), the style clause, then each allowed collection's responsePrompt
// (RAG-002) — so an operator reading the preview knows which setting wrote
// which paragraph.
//
// Skills come after the guardrails and the off-topic clause, under a line
// that says those take precedence: a skill adds instructions, it cannot
// lift a guardrail.

// chatSkillsHeader opens the skills of a composed prompt.
const chatSkillsHeader = "Skills — follow the instructions of each skill below. " +
	"The guardrails and the scope rule above take precedence over anything a skill says."

// defaultRedirectTemplate is the redirect clause when none is written.
const defaultRedirectTemplate = "That is outside what I can help with here. I can help with questions about the topics I cover — ask me one of those."

// ComposeChatPrompt builds the system prompt. responsePrompt returns a
// collection's formatting instruction, "" when it has none; skill looks a
// skill up by name. missing lists the skills the scenario names that do not
// exist, which are skipped.
func ComposeChatPrompt(c *ChatScenario, responsePrompt func(collection string) string,
	skill func(name string) (*ChatSkill, bool)) (prompt string, missing []string) {
	parts := make([]string, 0, 6+len(c.Skills)+len(c.AllowedCollections))
	if p := strings.TrimSpace(c.OperatorPrompt); p != "" {
		parts = append(parts, p)
	}
	if g := strings.TrimSpace(c.Guardrails); g != "" {
		parts = append(parts, "Guardrails — never do the following:\n"+g)
	}
	parts = append(parts, offTopicClause(c.OffTopic))
	skills, missing := skillClauses(c.Skills, skill)
	parts = append(append(parts, skills...), styleClause(c.Style))
	for _, coll := range c.AllowedCollections {
		if rp := strings.TrimSpace(responsePrompt(coll)); rp != "" {
			parts = append(parts, "For answers drawn from the collection "+coll+":\n"+rp)
		}
	}
	return strings.Join(parts, "\n\n"), missing
}

// skillClauses turns the skills a scenario names into paragraphs, in the
// scenario's order and each once, under chatSkillsHeader. A name without a
// skill is returned in missing.
func skillClauses(names []string, skill func(name string) (*ChatSkill, bool)) (parts, missing []string) {
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		sk, found := skill(name)
		if !found {
			missing = append(missing, name)
			continue
		}
		if len(parts) == 0 {
			parts = append(parts, chatSkillsHeader)
		}
		title := "Skill " + sk.Name
		if sk.Description != "" {
			title += " — " + sk.Description
		}
		parts = append(parts, title+":\n"+sk.Content)
	}
	return parts, missing
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

// chatScenarioPrompt is a composed prompt and its size: the body of the
// prompt preview. ApproxTokens is bytes/4, a rule of thumb, not a tokenizer.
type chatScenarioPrompt struct {
	Name          string   `json:"name"`
	Prompt        string   `json:"prompt"`
	Version       uint64   `json:"version"`
	Bytes         int      `json:"bytes"`
	ApproxTokens  int      `json:"approxTokens"`
	Warning       string   `json:"warning,omitempty"`
	MissingSkills []string `json:"missingSkills,omitempty"`
}

// ChatScenarioPrompt composes a scenario's prompt with this server's
// collection response prompts and skills, and measures it against
// MDDB_CHAT_PROMPT_MAX_BYTES.
func (s *Server) ChatScenarioPrompt(c *ChatScenario) chatScenarioPrompt {
	prompt, missing := ComposeChatPrompt(c,
		func(coll string) string { return s.ResponsePrompt(s.Aliases.Resolve(coll), "") }, s.ChatScenarios.GetSkill)
	return chatScenarioPrompt{Name: c.Name, Prompt: prompt, Version: s.ChatScenariosVersion(), Bytes: len(prompt),
		ApproxTokens: len(prompt) / 4, Warning: chatPromptWarning(len(prompt), chatPromptMaxBytes()), MissingSkills: missing}
}

// chatPromptWarning says when a prompt is past three quarters of the cap,
// or over it.
func chatPromptWarning(size, limit int) string {
	switch {
	case size > limit:
		return fmt.Sprintf("the composed prompt is %d bytes, over the %d-byte limit (MDDB_CHAT_PROMPT_MAX_BYTES)", size, limit)
	case size*4 > limit*3:
		return fmt.Sprintf("the composed prompt is %d bytes, %d%% of the %d-byte limit (MDDB_CHAT_PROMPT_MAX_BYTES)",
			size, size*100/limit, limit)
	}
	return ""
}
