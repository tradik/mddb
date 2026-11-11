package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Chat scenarios (#403): how an assistant behaves, stored next to the data.
//
// A chat assistant's behaviour used to live in mddb-chat's static TOML — the
// system prompt, the collections it may search, its LLM settings — and
// changing a word meant editing a file and restarting the service. A scenario
// is now a first-class object in mddbd, edited in mddb-panel and read by
// mddb-chat, which polls one version counter to pick up changes.
//
// Precedence follows the retrieval profiles (RAG-001): an explicit per-request
// value > the scenario > MDDB's default.

// Off-topic modes: what the assistant does with a question outside its scope.
const (
	OffTopicRefuse   = "refuse"
	OffTopicRedirect = "redirect"
	OffTopicBrief    = "brief"
)

// Emoji policies.
const (
	EmojiOff       = "off"
	EmojiSparingly = "sparingly"
	EmojiFree      = "free"
)

// Verbosity levels.
const (
	VerbosityShort  = "short"
	VerbosityNormal = "normal"
	VerbosityLong   = "long"
)

// Bounds. A scenario is stored data, so nonsense is refused when written.
const (
	maxChatScenarioNameLen   = 128
	maxChatScenarioTextBytes = 16384
	maxChatScenarioSkills    = 64
	maxChatTemperature       = 2.0
)

// ChatScenario is one assistant configuration.
type ChatScenario struct {
	Name                  string          `json:"name"`
	Description           string          `json:"description,omitempty"`
	Enabled               bool            `json:"enabled"`
	OperatorPrompt        string          `json:"operatorPrompt,omitempty"`
	Guardrails            string          `json:"guardrails,omitempty"`
	OffTopic              ChatOffTopic    `json:"offTopic"`
	AllowedCollections    []string        `json:"allowedCollections"`
	Skills                []string        `json:"skills,omitempty"`
	Style                 ChatStyle       `json:"style"`
	LLM                   ChatLLM         `json:"llm"`
	MaxTurns              int             `json:"maxTurns,omitempty"`
	TokenBudgetPerSession int             `json:"tokenBudgetPerSession,omitempty"`
	RateLimit             ChatRateLimit   `json:"rateLimit"`
	McpServers            []ChatMcpServer `json:"mcpServers,omitempty"` // MCP-005, chat_mcp_server.go
	Logging               ChatLogging     `json:"logging"`              // #406: the turn log, off by default
	CreatedAt             time.Time       `json:"createdAt"`
	UpdatedAt             time.Time       `json:"updatedAt"`
}

// ChatOffTopic is the off-topic behaviour.
type ChatOffTopic struct {
	Mode             string `json:"mode"`
	RedirectTemplate string `json:"redirectTemplate,omitempty"`
}

// ChatStyle is how answers are written.
type ChatStyle struct {
	Emoji            string `json:"emoji"`
	Verbosity        string `json:"verbosity"`
	Markdown         bool   `json:"markdown"`
	CitationTemplate string `json:"citationTemplate,omitempty"`
}

// ChatLLM is the model and its parameters. Temperature is a pointer because
// 0 is a meaningful value.
type ChatLLM struct {
	Model       string         `json:"model,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	MaxTokens   int            `json:"maxTokens,omitempty"`
	ExtraBody   map[string]any `json:"extraBody,omitempty"`
}

// ChatRateLimit bounds a session's traffic.
type ChatRateLimit struct {
	MessagesPerMinute int `json:"messagesPerMinute,omitempty"`
}

// errChatScenarioNotFound: a name no scenario has.
var errChatScenarioNotFound = errors.New("chat scenario not found")

// validChatScenarioName applies the alias naming rules: a name is a key and
// a path segment, so it may not hold '|', '/' or spaces.
func validChatScenarioName(name string) error { return validChatKeyName("scenario", name, "version") }

// validChatKeyName is the rule for a scenario's name and a skill's (#404);
// reserved are the names the routes of that kind use themselves.
func validChatKeyName(kind, name string, reserved ...string) error {
	taken := false
	for _, r := range reserved {
		taken = taken || name == r
	}
	switch {
	case name == "" || len(name) > maxChatScenarioNameLen:
		return fmt.Errorf("%s name must be 1 to %d characters", kind, maxChatScenarioNameLen)
	case strings.ContainsAny(name, "|/ "), taken:
		return fmt.Errorf("%s name %q may not contain '|', '/' or spaces, or be %s", kind, name, `"`+strings.Join(reserved, `" or "`)+`"`)
	}
	return nil
}

// validChatText bounds one free-text field.
func validChatText(field, text string) error {
	if n := len(text); n > maxChatScenarioTextBytes {
		return fmt.Errorf("%s is %d bytes, over the %d-byte limit", field, n, maxChatScenarioTextBytes)
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%s is not valid UTF-8", field)
	}
	return nil
}

// oneOf fills an empty choice with its default and refuses an unknown one.
func oneOf(field string, v *string, def string, allowed ...string) error {
	if *v == "" {
		*v = def
	}
	for _, a := range allowed {
		if *v == a {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", field, strings.Join(allowed, ", "))
}

// Normalize fills defaults and refuses a scenario that cannot mean anything.
// exists reports whether a name is a collection or an alias.
func (c *ChatScenario) Normalize(exists func(string) bool) error {
	if err := validChatScenarioName(c.Name); err != nil {
		return err
	}
	for field, text := range map[string]string{"description": c.Description, "operatorPrompt": c.OperatorPrompt,
		"guardrails": c.Guardrails, "offTopic.redirectTemplate": c.OffTopic.RedirectTemplate,
		"style.citationTemplate": c.Style.CitationTemplate} {
		if err := validChatText(field, text); err != nil {
			return err
		}
	}
	if err := oneOf("offTopic.mode", &c.OffTopic.Mode, OffTopicRefuse, OffTopicRefuse, OffTopicRedirect, OffTopicBrief); err != nil {
		return err
	}
	if err := oneOf("style.emoji", &c.Style.Emoji, EmojiOff, EmojiOff, EmojiSparingly, EmojiFree); err != nil {
		return err
	}
	if err := oneOf("style.verbosity", &c.Style.Verbosity, VerbosityNormal, VerbosityShort, VerbosityNormal, VerbosityLong); err != nil {
		return err
	}
	if t := c.LLM.Temperature; t != nil && (*t < 0 || *t > maxChatTemperature) {
		return fmt.Errorf("llm.temperature must be between 0 and %g", maxChatTemperature)
	}
	for field, n := range map[string]int{"llm.maxTokens": c.LLM.MaxTokens, "maxTurns": c.MaxTurns,
		"tokenBudgetPerSession": c.TokenBudgetPerSession, "rateLimit.messagesPerMinute": c.RateLimit.MessagesPerMinute} {
		if n < 0 {
			return fmt.Errorf("%s may not be negative", field)
		}
	}
	if len(c.Skills) > maxChatScenarioSkills {
		return fmt.Errorf("at most %d skills", maxChatScenarioSkills)
	}
	for _, sk := range c.Skills {
		if sk == "" || len(sk) > maxChatScenarioNameLen {
			return fmt.Errorf("skill names must be 1 to %d characters", maxChatScenarioNameLen)
		}
	}
	if err := normalizeChatMcpServers(c.McpServers); err != nil {
		return err
	}
	if err := c.Logging.normalize(); err != nil {
		return err
	}
	return c.normalizeCollections(exists)
}

// normalizeCollections refuses an empty, repeated or unknown collection.
func (c *ChatScenario) normalizeCollections(exists func(string) bool) error {
	seen := map[string]bool{}
	out := make([]string, 0, len(c.AllowedCollections))
	for _, name := range c.AllowedCollections {
		switch {
		case name == "":
			return errors.New("allowedCollections may not hold an empty name")
		case seen[name]:
			return fmt.Errorf("collection %q is listed twice", name)
		case exists != nil && !exists(name):
			return fmt.Errorf("collection %q does not exist", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	c.AllowedCollections = out
	return nil
}
