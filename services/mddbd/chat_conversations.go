package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Persistent per-user chat conversations (#405).
//
// A host application that has already authenticated its user keeps that
// user's conversations here, so a reconnect, a new pod or a restart of
// mddb-chat resumes them. The model and its rules are in this file, the
// storage in chat_conversations_store.go; REST, gRPC and MCP all call the
// same functions, so the three cannot drift.
//
// Every conversation belongs to one `sub`: the opaque user id of the host's
// signed join token. It is never chosen by the browser, and every read and
// write names it; a conversation whose sub differs is "not found", so a host
// cannot cross users by mistake and cannot learn that another user's id
// exists.

// chatConversationsCollection is the system collection that holds the
// conversations: one document per conversation. A tenant's conversations
// live in "<tenant>/chat_conversations" (chatCollection).
const chatConversationsCollection = "chat_conversations"

// Limits of the store.
const (
	chatDefaultRetentionDays       = 30
	chatMaxRetentionDays           = 3650
	chatDefaultListLimit           = 50
	chatMaxListLimit               = 200
	chatMaxAppend                  = 100     // messages in one append
	chatMaxMessages                = 5000    // messages in one conversation
	chatMaxContentBytes            = 256_000 // bytes in one message
	chatMaxNameBytes               = 256     // sub, scenario, displayName
	chatTitleRunes                 = 80
	chatRetentionEnv               = "MDDB_CHAT_RETENTION_DAYS"
	secondsPerDay            int64 = 24 * 3600
)

// Chat errors. errChatInvalid is a request the store refuses (400,
// INVALID_ARGUMENT); errChatNotFound a conversation that does not exist, has
// expired or belongs to another sub (404, NOT_FOUND); errChatEnded an append
// to an ended conversation (409, FAILED_PRECONDITION).
var (
	errChatInvalid  = errors.New("invalid chat request")
	errChatNotFound = fmt.Errorf("conversation %w", errNotFound)
	errChatEnded    = errors.New("conversation has ended")
)

// chatIDRe is what a conversation id looks like: anything else cannot exist.
var chatIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ChatToolCall is one tool the assistant called while answering.
type ChatToolCall struct {
	Name       string `json:"name"`
	Collection string `json:"collection,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	// What a logged turn's tool searched for, the keys it found (best first)
	// and the best score (#406).
	Query      string   `json:"query,omitempty"`
	ResultKeys []string `json:"resultKeys,omitempty"`
	TopScore   float64  `json:"topScore,omitempty"`
	// Server is the external MCP server of the tool (MCP-005); empty for a
	// built-in tool.
	Server string `json:"server,omitempty"`
}

// ChatTokens is the token usage of one assistant message.
type ChatTokens struct {
	Prompt     int64 `json:"prompt"`
	Completion int64 `json:"completion"`
}

// ChatMessage is one turn of a conversation.
type ChatMessage struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"` // user, assistant or tool
	Content   string         `json:"content"`
	ToolCalls []ChatToolCall `json:"toolCalls,omitempty"`
	Model     string         `json:"model,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	Tokens    *ChatTokens    `json:"tokens,omitempty"`
	CreatedAt int64          `json:"createdAt"` // unix seconds
	// Outcome marks a logged turn's answer (#406): answered, noResults,
	// refused or error. LatencyMs is how long the turn took.
	Outcome   string        `json:"outcome,omitempty"`
	LatencyMs int64         `json:"latencyMs,omitempty"`
	Feedback  *ChatFeedback `json:"feedback,omitempty"`
	// LoopStopped marks an answer mddb-chat's loop guard cut short (#413).
	LoopStopped bool `json:"loopStopped,omitempty"`
}

// ChatConversationSummary is a conversation without its messages, as a
// listing returns it.
type ChatConversationSummary struct {
	ID            string `json:"id"`
	Scenario      string `json:"scenario"`
	Sub           string `json:"sub"`
	DisplayName   string `json:"displayName,omitempty"`
	Title         string `json:"title,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	LastMessageAt int64  `json:"lastMessageAt,omitempty"`
	Ended         bool   `json:"ended"`
	RetentionDays int    `json:"retentionDays"`
	ExpiresAt     int64  `json:"expiresAt,omitempty"`
	MessageCount  int    `json:"messageCount"`
	// Redact is set once an append asked for redaction (#406); from then on
	// every message and feedback comment of the conversation is redacted.
	Redact bool `json:"redact,omitempty"`
}

// ChatConversation is a conversation with its messages, oldest first.
type ChatConversation struct {
	ChatConversationSummary
	Messages []ChatMessage `json:"messages"`
}

// ChatCreateRequest creates a conversation for a sub in a scenario.
type ChatCreateRequest struct {
	Sub         string `json:"sub"`
	Scenario    string `json:"scenario"`
	DisplayName string `json:"displayName,omitempty"`
	// RetentionDays is the scenario's retention: the conversation is removed
	// that many days after its last write. 0 is the server default.
	RetentionDays int `json:"retentionDays,omitempty"`
}

// ChatAppendRequest appends messages to a conversation of sub.
type ChatAppendRequest struct {
	Sub      string        `json:"sub"`
	Messages []ChatMessage `json:"messages"`
	// Redact redacts these messages and every later write (#406).
	Redact bool `json:"redact,omitempty"`
}

// ChatListResponse is one page of a user's conversations, newest first.
type ChatListResponse struct {
	Conversations []ChatConversationSummary `json:"conversations"`
	NextCursor    string                    `json:"nextCursor,omitempty"`
}

// ChatExport is everything the store holds about one sub (GDPR export).
type ChatExport struct {
	Sub           string             `json:"sub"`
	ExportedAt    int64              `json:"exportedAt"`
	Total         int                `json:"total"`
	Conversations []ChatConversation `json:"conversations"`
}

// chatCollection is the collection holding the caller's conversations: the
// tenant's own for a tenant user, the global one otherwise.
func chatCollection(ctx context.Context) string {
	if t := TenantFromContext(ctx); t != "" {
		return t + TenantSeparator + chatConversationsCollection
	}
	return chatConversationsCollection
}

// chatInvalid is errChatInvalid with the reason.
func chatInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errChatInvalid, fmt.Sprintf(format, args...))
}

// validChatName checks a sub, scenario or display name: valid UTF-8 without
// control characters, at most chatMaxNameBytes long.
func validChatName(field, v string, required bool) error {
	switch {
	case v == "" && required:
		return chatInvalid("missing %s", field)
	case len(v) > chatMaxNameBytes:
		return chatInvalid("%s is longer than %d bytes", field, chatMaxNameBytes)
	case !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0:
		return chatInvalid("%s holds control characters or invalid UTF-8", field)
	}
	return nil
}

// sameSub compares two subs without leaking where they differ.
func sameSub(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// chatRetentionDays resolves a requested retention: 0 is the server default
// (MDDB_CHAT_RETENTION_DAYS, else 30 days).
func chatRetentionDays(requested int) (int, error) {
	if requested < 0 || requested > chatMaxRetentionDays {
		return 0, chatInvalid("retentionDays must be between 0 and %d", chatMaxRetentionDays)
	}
	if requested > 0 {
		return requested, nil
	}
	if n, err := strconv.Atoi(env(chatRetentionEnv, "")); err == nil && n > 0 && n <= chatMaxRetentionDays {
		return n, nil
	}
	return chatDefaultRetentionDays, nil
}

// chatTitle is the start of a message as a conversation title: one line, at
// most chatTitleRunes characters, cut between characters.
func chatTitle(content string) string {
	title := strings.Join(strings.Fields(content), " ")
	if utf8.RuneCountInString(title) <= chatTitleRunes {
		return title
	}
	return string([]rune(title)[:chatTitleRunes])
}

// prepareChatMessages validates the messages of an append and gives each its
// id and, when the host sent none, its time.
func prepareChatMessages(msgs []ChatMessage, now int64) ([]ChatMessage, error) {
	if len(msgs) == 0 || len(msgs) > chatMaxAppend {
		return nil, chatInvalid("an append carries 1 to %d messages", chatMaxAppend)
	}
	out := make([]ChatMessage, 0, len(msgs))
	for i, m := range msgs {
		switch {
		case m.Role != "user" && m.Role != "assistant" && m.Role != "tool":
			return nil, chatInvalid("message %d: role must be user, assistant or tool", i)
		case m.Content == "" && len(m.ToolCalls) == 0 && m.Outcome == "":
			return nil, chatInvalid("message %d: missing content", i)
		case len(m.Content) > chatMaxContentBytes:
			return nil, chatInvalid("message %d: content is longer than %d bytes", i, chatMaxContentBytes)
		case m.CreatedAt < 0:
			return nil, chatInvalid("message %d: createdAt is negative", i)
		}
		if err := validChatTurn(i, &m); err != nil {
			return nil, err
		}
		if m.CreatedAt == 0 {
			m.CreatedAt = now
		}
		out = append(out, m)
	}
	return out, nil
}

// appendChatMessages adds prepared messages to a conversation, updating its
// title (the first user message) and the time of its last message.
func appendChatMessages(conv *ChatConversation, msgs []ChatMessage) error {
	if conv.Ended {
		return errChatEnded
	}
	if len(conv.Messages)+len(msgs) > chatMaxMessages {
		return chatInvalid("a conversation holds at most %d messages; start a new one", chatMaxMessages)
	}
	for _, m := range msgs {
		if conv.Title == "" && m.Role == "user" {
			conv.Title = chatTitle(m.Content)
		}
		conv.LastMessageAt = max(conv.LastMessageAt, m.CreatedAt)
	}
	conv.Messages = append(conv.Messages, msgs...)
	conv.MessageCount = len(conv.Messages)
	return nil
}

// lastChatMessages is the conversation with only its last limit messages
// (the chat's max_history_length); limit <= 0 keeps all. MessageCount stays
// the whole conversation's.
func lastChatMessages(conv *ChatConversation, limit int) *ChatConversation {
	if limit > 0 && len(conv.Messages) > limit {
		conv.Messages = conv.Messages[len(conv.Messages)-limit:]
	}
	return conv
}

// chatSortAt is what a listing orders by: the last message, or the creation
// of a conversation that has none yet.
func chatSortAt(c ChatConversationSummary) int64 {
	return max(c.LastMessageAt, c.CreatedAt)
}

// chatNewer reports whether a sorts before b in a listing: newest first,
// the id breaking a tie so the order is total.
func chatNewer(a, b ChatConversationSummary) bool {
	if chatSortAt(a) != chatSortAt(b) {
		return chatSortAt(a) > chatSortAt(b)
	}
	return a.ID > b.ID
}

// chatCursor is the opaque position after c in a listing.
func chatCursor(c ChatConversationSummary) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(chatSortAt(c), 10) + "|" + c.ID))
}

// parseChatCursor reads a cursor back into the position it stands for.
func parseChatCursor(cursor string) (ChatConversationSummary, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	at, id, found := strings.Cut(string(raw), "|")
	n, numErr := strconv.ParseInt(at, 10, 64)
	if err != nil || !found || numErr != nil {
		return ChatConversationSummary{}, chatInvalid("unreadable cursor")
	}
	return ChatConversationSummary{ID: id, CreatedAt: n}, nil
}

// pageChatSummaries cuts one page out of summaries sorted newest first: the
// entries after cursor, at most limit of them, and the cursor of the next
// page when more remain.
func pageChatSummaries(sorted []ChatConversationSummary, limit int, cursor string) (ChatListResponse, error) {
	if limit <= 0 {
		limit = chatDefaultListLimit
	}
	limit = min(limit, chatMaxListLimit)
	if cursor != "" {
		after, err := parseChatCursor(cursor)
		if err != nil {
			return ChatListResponse{}, err
		}
		start := len(sorted)
		for i, c := range sorted {
			if chatNewer(after, c) {
				start = i
				break
			}
		}
		sorted = sorted[start:]
	}
	resp := ChatListResponse{Conversations: []ChatConversationSummary{}}
	if len(sorted) > limit {
		resp.NextCursor = chatCursor(sorted[limit-1])
		sorted = sorted[:limit]
	}
	resp.Conversations = append(resp.Conversations, sorted...)
	return resp, nil
}
