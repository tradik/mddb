package main

import (
	"strings"
	"unicode/utf8"
)

// The conversation log and answer feedback (#406).
//
// A scenario that opts in (logging.enabled) has mddb-chat record every turn —
// anonymous sessions included — in the conversation store: a signed user's
// turns go to that user's conversation (#405), an anonymous session's to a
// conversation whose sub is "anon:<session id>", kept for the scenario's
// logging.retentionDays. A logged turn is an assistant message with an
// outcome; its question is the user message before it. Nothing else marks a
// log: the admin views (chat_log_store.go) read the same documents.
//
// Privacy: redaction is on by default (chat_redact.go), the log and the gaps
// are global-admin only, and an anonymous session's id is never shown as an
// identity.

// Outcomes of a logged turn.
const (
	ChatOutcomeAnswered  = "answered"
	ChatOutcomeNoResults = "noResults"
	ChatOutcomeRefused   = "refused"
	ChatOutcomeError     = "error"
)

// Ratings of an answer.
const (
	ChatRatingUp   = "up"
	ChatRatingDown = "down"
)

// Bounds of a logged turn and of feedback.
const (
	chatMaxResultKeys     = 50
	chatMaxQueryBytes     = 4096
	chatMaxCommentRunes   = 2000
	chatAnonPrefix        = "anon:"
	chatExcerptRunes      = 280
	chatMaxToolCallsPerMs = 64 // tool calls on one message
)

// ChatFeedback is a user's rating of one assistant message.
type ChatFeedback struct {
	Rating  string `json:"rating"`
	Comment string `json:"comment,omitempty"`
	At      int64  `json:"at"`
}

// ChatLogging is a scenario's turn log (#406). Off unless enabled; Redact is
// a pointer so that "not set" means on.
type ChatLogging struct {
	Enabled       bool  `json:"enabled"`
	RetentionDays int   `json:"retentionDays,omitempty"`
	Redact        *bool `json:"redact,omitempty"`
}

// normalize fills Redact (default true) and bounds the retention.
func (l *ChatLogging) normalize() error {
	if l.RetentionDays < 0 || l.RetentionDays > chatMaxRetentionDays {
		return chatInvalid("logging.retentionDays must be between 0 and %d", chatMaxRetentionDays)
	}
	if l.Redact == nil {
		on := true
		l.Redact = &on
	}
	return nil
}

// validChatOutcome reports whether o is an outcome a turn may carry; "" is
// a message that is not a logged turn.
func validChatOutcome(o string) bool {
	switch o {
	case "", ChatOutcomeAnswered, ChatOutcomeNoResults, ChatOutcomeRefused, ChatOutcomeError:
		return true
	}
	return false
}

// validChatTurn checks and settles the #406 fields of message i of an
// append: its id (the client's when it looks like one, so a rating can name
// it, else a fresh one), the outcome, the latency and the tool calls. A
// rating cannot be appended; it comes through chatFeedback.
func validChatTurn(i int, m *ChatMessage) error {
	if !chatIDRe.MatchString(m.ID) {
		m.ID = generateMemoryMessageID() // the memory store's message ids: 12 random bytes
	}
	m.Feedback = nil
	switch {
	case !validChatOutcome(m.Outcome):
		return chatInvalid("message %d: outcome must be answered, noResults, refused or error", i)
	case m.Outcome != "" && m.Role != "assistant":
		return chatInvalid("message %d: only an assistant message has an outcome", i)
	case m.LoopStopped && m.Role != "assistant":
		return chatInvalid("message %d: only an assistant message is stopped by the loop guard", i)
	case m.LatencyMs < 0:
		return chatInvalid("message %d: latencyMs is negative", i)
	case len(m.ToolCalls) > chatMaxToolCallsPerMs:
		return chatInvalid("message %d: at most %d tool calls", i, chatMaxToolCallsPerMs)
	}
	for j, tc := range m.ToolCalls {
		switch {
		case len(tc.Query) > chatMaxQueryBytes:
			return chatInvalid("message %d: tool call %d: query is longer than %d bytes", i, j, chatMaxQueryBytes)
		case len(tc.ResultKeys) > chatMaxResultKeys:
			return chatInvalid("message %d: tool call %d: at most %d result keys", i, j, chatMaxResultKeys)
		case len(tc.Server) > chatMaxNameBytes:
			return chatInvalid("message %d: tool call %d: server is longer than %d bytes", i, j, chatMaxNameBytes)
		}
	}
	return nil
}

// markChatRefusals turns an "answered" turn whose reply is the scenario's
// redirect text into "refused": the model was told to reply with exactly
// that to an off-topic question (offTopicClause). refusal is "" for a
// scenario that does not redirect.
func markChatRefusals(msgs []ChatMessage, refusal string) {
	want := chatComparable(refusal)
	if want == "" {
		return
	}
	for i := range msgs {
		if msgs[i].Outcome == ChatOutcomeAnswered && strings.Contains(chatComparable(msgs[i].Content), want) {
			msgs[i].Outcome = ChatOutcomeRefused
		}
	}
}

// chatComparable is text lowercased with its whitespace collapsed.
func chatComparable(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// chatRefusalText is the reply a redirecting scenario gives an off-topic
// question, "" for any other scenario or none.
func (s *Server) chatRefusalText(scenario string) string {
	if s.ChatScenarios == nil {
		return ""
	}
	sc, found := s.ChatScenarios.Get(scenario)
	if !found || sc.OffTopic.Mode != OffTopicRedirect {
		return ""
	}
	if t := strings.TrimSpace(sc.OffTopic.RedirectTemplate); t != "" {
		return t
	}
	return defaultRedirectTemplate
}

// chatExcerpt is the start of a text: one line, at most chatExcerptRunes
// characters, with an ellipsis when cut.
func chatExcerpt(text string) string {
	line := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(line) <= chatExcerptRunes {
		return line
	}
	return string([]rune(line)[:chatExcerptRunes]) + "…"
}

// chatAnonymous reports whether a sub is an anonymous session's log.
func chatAnonymous(sub string) bool { return strings.HasPrefix(sub, chatAnonPrefix) }
