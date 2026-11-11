package main

import (
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
)

// The turns of the conversation log (#406): read out of stored
// conversations, filtered and paged. Storage is in chat_log_store.go.

// ChatLogFilter selects logged turns. From and To are unix seconds,
// inclusive, 0 for no bound. Rating is up, down, none (unrated) or any
// (rated); Outcome one of the outcomes.
type ChatLogFilter struct {
	Scenario string
	From     int64
	To       int64
	Rating   string
	Outcome  string
	Cursor   string
	Limit    int
}

// ChatLogTurn is one logged turn as the admin views show it. Sub and
// DisplayName are a signed user's; an anonymous session has neither.
type ChatLogTurn struct {
	ConversationID string         `json:"conversationId"`
	MessageID      string         `json:"messageId"`
	Scenario       string         `json:"scenario"`
	Sub            string         `json:"sub,omitempty"`
	DisplayName    string         `json:"displayName,omitempty"`
	Anonymous      bool           `json:"anonymous"`
	At             int64          `json:"at"`
	Question       string         `json:"question"`
	Answer         string         `json:"answer"`
	AnswerExcerpt  string         `json:"answerExcerpt"`
	Outcome        string         `json:"outcome"`
	Rating         string         `json:"rating,omitempty"`
	Comment        string         `json:"comment,omitempty"`
	Model          string         `json:"model,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	LatencyMs      int64          `json:"latencyMs,omitempty"`
	Tokens         *ChatTokens    `json:"tokens,omitempty"`
	ToolCalls      []ChatToolCall `json:"toolCalls,omitempty"`
}

// ChatLogPage is one page of the log, newest first.
type ChatLogPage struct {
	Turns      []ChatLogTurn `json:"turns"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// validate refuses a filter that cannot match anything sensible.
func (f ChatLogFilter) validate() error {
	switch {
	case f.Rating != "" && f.Rating != ChatRatingUp && f.Rating != ChatRatingDown && f.Rating != "none" && f.Rating != "any":
		return chatInvalid("rating must be up, down, none or any")
	case f.Outcome != "" && !validChatOutcome(f.Outcome):
		return chatInvalid("outcome must be answered, noResults, refused or error")
	case f.From < 0 || f.To < 0 || (f.To > 0 && f.From > f.To):
		return chatInvalid("from and to must be unix seconds with from <= to")
	}
	return nil
}

// matches reports whether a turn passes the filter (the scenario is
// filtered when the conversations are read).
func (f ChatLogFilter) matches(t *ChatLogTurn) bool {
	switch {
	case f.From > 0 && t.At < f.From, f.To > 0 && t.At > f.To:
		return false
	case f.Outcome != "" && t.Outcome != f.Outcome:
		return false
	}
	switch f.Rating {
	case "":
		return true
	case "none":
		return t.Rating == ""
	case "any":
		return t.Rating != ""
	default:
		return t.Rating == f.Rating
	}
}

// chatLogTurns reads the logged turns out of conversations: every assistant
// message with an outcome, with the user message before it as its question.
func chatLogTurns(convs []*ChatConversation, f ChatLogFilter) []ChatLogTurn {
	var out []ChatLogTurn
	for _, c := range convs {
		question := ""
		for i := range c.Messages {
			m := &c.Messages[i]
			switch {
			case m.Role == "user":
				question = m.Content
			case m.Role == "assistant" && m.Outcome != "":
				t := chatLogTurn(c, m, question)
				if f.matches(&t) {
					out = append(out, t)
				}
				question = ""
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return chatTurnNewer(out[i], out[j]) })
	return out
}

// chatLogTurn is one answer of a conversation as a turn.
func chatLogTurn(c *ChatConversation, m *ChatMessage, question string) ChatLogTurn {
	t := ChatLogTurn{
		ConversationID: c.ID, MessageID: m.ID, Scenario: c.Scenario, Anonymous: chatAnonymous(c.Sub),
		At: m.CreatedAt, Question: question, Answer: m.Content, AnswerExcerpt: chatExcerpt(m.Content),
		Outcome: m.Outcome, Model: m.Model, Provider: m.Provider, LatencyMs: m.LatencyMs,
		Tokens: m.Tokens, ToolCalls: m.ToolCalls,
	}
	if !t.Anonymous {
		t.Sub, t.DisplayName = c.Sub, c.DisplayName
	}
	if m.Feedback != nil {
		t.Rating, t.Comment = m.Feedback.Rating, m.Feedback.Comment
	}
	return t
}

// chatTurnKey is the position of a turn in the log's order.
func chatTurnKey(t ChatLogTurn) string {
	return strconv.FormatInt(t.At, 10) + "|" + t.ConversationID + "|" + t.MessageID
}

// chatTurnNewer orders the log newest first, the ids breaking a tie.
func chatTurnNewer(a, b ChatLogTurn) bool {
	if a.At != b.At {
		return a.At > b.At
	}
	if a.ConversationID != b.ConversationID {
		return a.ConversationID > b.ConversationID
	}
	return a.MessageID > b.MessageID
}

// pageChatLog cuts one page out of turns sorted newest first.
func pageChatLog(turns []ChatLogTurn, limit int, cursor string) (ChatLogPage, error) {
	if limit <= 0 {
		limit = chatDefaultListLimit
	}
	limit = min(limit, chatMaxListLimit)
	if cursor != "" {
		after, err := parseChatLogCursor(cursor)
		if err != nil {
			return ChatLogPage{}, err
		}
		start := len(turns)
		for i, t := range turns {
			if chatTurnNewer(after, t) {
				start = i
				break
			}
		}
		turns = turns[start:]
	}
	page := ChatLogPage{Turns: []ChatLogTurn{}}
	if len(turns) > limit {
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(chatTurnKey(turns[limit-1])))
		turns = turns[:limit]
	}
	page.Turns = append(page.Turns, turns...)
	return page, nil
}

// parseChatLogCursor reads a cursor back into the turn it stands after.
func parseChatLogCursor(cursor string) (ChatLogTurn, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	parts := strings.SplitN(string(raw), "|", 3)
	if err != nil || len(parts) != 3 {
		return ChatLogTurn{}, chatInvalid("unreadable cursor")
	}
	at, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return ChatLogTurn{}, chatInvalid("unreadable cursor")
	}
	return ChatLogTurn{At: at, ConversationID: parts[1], MessageID: parts[2]}, nil
}
