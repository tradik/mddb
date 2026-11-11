package main

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Answer feedback (#406): a rating, up or down, and an optional comment on
// one assistant message, stored with that message. Rating again replaces
// the earlier rating. An ended conversation can still be rated: a visitor
// often rates after closing the chat.

// ChatFeedbackRequest rates a message of a conversation of sub.
type ChatFeedbackRequest struct {
	Sub       string `json:"sub"`
	MessageID string `json:"messageId"`
	Rating    string `json:"rating"`
	Comment   string `json:"comment,omitempty"`
}

// ChatFeedbackResult is the stored rating.
type ChatFeedbackResult struct {
	ConversationID string       `json:"conversationId"`
	MessageID      string       `json:"messageId"`
	Feedback       ChatFeedback `json:"feedback"`
}

// validChatFeedback checks a rating and returns the comment as stored:
// trimmed, at most chatMaxCommentRunes characters, without control
// characters other than line breaks.
func validChatFeedback(req ChatFeedbackRequest) (string, error) {
	comment := strings.TrimSpace(req.Comment)
	switch {
	case req.MessageID == "":
		return "", chatInvalid("missing messageId")
	case req.Rating != ChatRatingUp && req.Rating != ChatRatingDown:
		return "", chatInvalid("rating must be up or down")
	case !utf8.ValidString(comment):
		return "", chatInvalid("comment is not valid UTF-8")
	case utf8.RuneCountInString(comment) > chatMaxCommentRunes:
		return "", chatInvalid("comment is longer than %d characters", chatMaxCommentRunes)
	case strings.IndexFunc(comment, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0:
		return "", chatInvalid("comment holds control characters")
	}
	return comment, nil
}

// chatFeedback stores a rating on an assistant message of a conversation of
// sub. A message that is not there, or is not an answer, is not found.
func (s *Server) chatFeedback(ctx context.Context, id string, req ChatFeedbackRequest) (*ChatFeedbackResult, error) {
	comment, err := validChatFeedback(req)
	if err != nil {
		return nil, err
	}
	out := &ChatFeedbackResult{ConversationID: id, MessageID: req.MessageID}
	_, err = s.chatModify(ctx, id, req.Sub, func(conv *ChatConversation) error {
		for i := len(conv.Messages) - 1; i >= 0; i-- {
			m := &conv.Messages[i]
			if m.ID != req.MessageID || m.Role != "assistant" {
				continue
			}
			if conv.Redact {
				comment = redactChatText(comment)
			}
			m.Feedback = &ChatFeedback{Rating: req.Rating, Comment: comment, At: chatNow()}
			out.Feedback = *m.Feedback
			return nil
		}
		return errChatMessageNotFound
	}, 0)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// errChatMessageNotFound is a rating of a message the conversation does
// not hold as an answer (404, NOT_FOUND).
var errChatMessageNotFound = fmt.Errorf("message %w", errNotFound)
