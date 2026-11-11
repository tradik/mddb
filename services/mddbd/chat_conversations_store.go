package main

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// The chat conversation store (#405): one document per conversation in the
// chat collection of the caller's tenant, written through addDocument like
// every other document, so encryption at rest, the binlog (replication),
// backups and quotas apply unchanged.
//
// Retention is the document's TTL: every write sets it to the conversation's
// retentionDays from now, and the TTL reaper that removes any expired
// document removes the conversation. No second reaper exists.
//
// A conversation is not embedded, full-text indexed or sent to webhooks: it
// is a transcript read back whole, and a "*" webhook must not receive users'
// conversations.

// chatLang is the language slot of a conversation document; it has none.
const chatLang = "en"

// chatWriteOpts keeps a conversation out of the indexes and the webhooks.
var chatWriteOpts = postBatchOptions{SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true}

// chatWriteMu serializes read-modify-write of conversations: two appends to
// one conversation must both land.
var chatWriteMu sync.Mutex

// chatNow is the clock, replaceable in tests.
var chatNow = func() int64 { return time.Now().Unix() }

// chatCreate creates an empty conversation for a sub in a scenario.
func (s *Server) chatCreate(ctx context.Context, req ChatCreateRequest) (*ChatConversation, error) {
	for _, f := range []struct {
		name, value string
		required    bool
	}{{"sub", req.Sub, true}, {"scenario", req.Scenario, true}, {"displayName", req.DisplayName, false}} {
		if err := validChatName(f.name, f.value, f.required); err != nil {
			return nil, err
		}
	}
	days, err := chatRetentionDays(req.RetentionDays)
	if err != nil {
		return nil, err
	}
	conv := &ChatConversation{ChatConversationSummary: ChatConversationSummary{
		ID: generateSessionID(), Scenario: req.Scenario, Sub: req.Sub,
		DisplayName: req.DisplayName, RetentionDays: days,
	}, Messages: []ChatMessage{}}
	return conv, s.chatSave(chatCollection(ctx), conv)
}

// chatGet returns a conversation of sub with its last limit messages (all
// of them when limit <= 0).
func (s *Server) chatGet(ctx context.Context, id, sub string, limit int) (*ChatConversation, error) {
	conv, err := s.chatLoad(chatCollection(ctx), id, sub)
	if err != nil {
		return nil, err
	}
	return lastChatMessages(conv, limit), nil
}

// chatAppend appends messages to a conversation of sub and returns the
// conversation holding only the messages added, with their ids.
func (s *Server) chatAppend(ctx context.Context, id string, req ChatAppendRequest) (*ChatConversation, error) {
	msgs, err := prepareChatMessages(req.Messages, chatNow())
	if err != nil {
		return nil, err
	}
	return s.chatModify(ctx, id, req.Sub, func(conv *ChatConversation) error {
		// #406: redaction is sticky, and a redirect reply is a refusal.
		conv.Redact = conv.Redact || req.Redact
		if conv.Redact {
			redactChatMessages(msgs)
		}
		markChatRefusals(msgs, s.chatRefusalText(conv.Scenario))
		return appendChatMessages(conv, msgs)
	}, len(msgs))
}

// chatEnd marks a conversation of sub ended; ending it again changes
// nothing. The conversation comes back without messages.
func (s *Server) chatEnd(ctx context.Context, id, sub string) (*ChatConversation, error) {
	return s.chatModify(ctx, id, sub, func(conv *ChatConversation) error {
		conv.Ended = true
		return nil
	}, 0)
}

// chatModify loads a conversation of sub, changes it and saves it, one
// writer at a time. It returns the conversation with its last keep messages.
func (s *Server) chatModify(ctx context.Context, id, sub string, change func(*ChatConversation) error, keep int) (*ChatConversation, error) {
	chatWriteMu.Lock()
	defer chatWriteMu.Unlock()
	coll := chatCollection(ctx)
	conv, err := s.chatLoad(coll, id, sub)
	if err != nil {
		return nil, err
	}
	if err := change(conv); err != nil {
		return nil, err
	}
	if err := s.chatSave(coll, conv); err != nil {
		return nil, err
	}
	conv.Messages = conv.Messages[len(conv.Messages)-keep:]
	return conv, nil
}

// chatDelete removes one conversation of sub.
func (s *Server) chatDelete(ctx context.Context, id, sub string) error {
	chatWriteMu.Lock()
	defer chatWriteMu.Unlock()
	coll := chatCollection(ctx)
	if _, err := s.chatLoad(coll, id, sub); err != nil {
		return err
	}
	return s.deleteDocumentInternal(coll, id, chatLang)
}

// chatDeleteForUser removes every conversation of sub (GDPR erasure) and
// returns how many there were.
func (s *Server) chatDeleteForUser(ctx context.Context, sub string) (int, error) {
	chatWriteMu.Lock()
	defer chatWriteMu.Unlock()
	coll := chatCollection(ctx)
	convs, err := s.chatOfUser(coll, sub, "")
	if err != nil {
		return 0, err
	}
	for i, c := range convs {
		if err := s.deleteDocumentInternal(coll, c.ID, chatLang); err != nil {
			return i, err
		}
	}
	return len(convs), nil
}

// chatList is one page of sub's conversations, newest first, optionally of
// one scenario.
func (s *Server) chatList(ctx context.Context, sub, scenario string, limit int, cursor string) (ChatListResponse, error) {
	convs, err := s.chatOfUser(chatCollection(ctx), sub, scenario)
	if err != nil {
		return ChatListResponse{}, err
	}
	summaries := make([]ChatConversationSummary, 0, len(convs))
	for _, c := range convs {
		summaries = append(summaries, c.ChatConversationSummary)
	}
	return pageChatSummaries(summaries, limit, cursor)
}

// chatExport is every conversation of sub with its messages (GDPR export),
// newest first.
func (s *Server) chatExport(ctx context.Context, sub string) (*ChatExport, error) {
	convs, err := s.chatOfUser(chatCollection(ctx), sub, "")
	if err != nil {
		return nil, err
	}
	out := &ChatExport{Sub: sub, ExportedAt: chatNow(), Total: len(convs), Conversations: make([]ChatConversation, 0, len(convs))}
	for _, c := range convs {
		out.Conversations = append(out.Conversations, *c)
	}
	return out, nil
}

// chatSave writes a conversation and refreshes its retention.
func (s *Server) chatSave(coll string, conv *ChatConversation) error {
	conv.MessageCount = len(conv.Messages)
	body, _ := json.Marshal(conv) // plain strings and numbers: it cannot fail
	meta := map[string][]string{
		"sub":      {conv.Sub},
		"scenario": {conv.Scenario},
		"ended":    {strconv.FormatBool(conv.Ended)},
	}
	saved, _, err := s.addDocumentWith(coll, conv.ID, chatLang, meta, string(body),
		int64(conv.RetentionDays)*secondsPerDay, false, "", chatWriteOpts)
	if err != nil {
		return err
	}
	conv.CreatedAt, conv.UpdatedAt, conv.ExpiresAt = saved.AddedAt, saved.UpdatedAt, saved.ExpiresAt
	return nil
}

// chatLoad reads a conversation of sub. A conversation that does not exist,
// has expired or belongs to another sub is the same errChatNotFound.
func (s *Server) chatLoad(coll, id, sub string) (*ChatConversation, error) {
	if err := validChatName("sub", sub, true); err != nil {
		return nil, err
	}
	if !chatIDRe.MatchString(id) {
		return nil, errChatNotFound
	}
	conv := chatFromDoc(s.loadDocByID(coll, genID(coll, id, chatLang), true), chatNow())
	if conv == nil || !sameSub(conv.Sub, sub) {
		return nil, errChatNotFound
	}
	return conv, nil
}

// chatOfUser reads the live conversations of sub, newest first, through the
// metadata index; scenario, when set, keeps only that scenario's.
func (s *Server) chatOfUser(coll, sub, scenario string) ([]*ChatConversation, error) {
	if err := validChatName("sub", sub, true); err != nil {
		return nil, err
	}
	var docIDs []string
	err := s.DBViewC(coll, func(tx *bolt.Tx) error {
		if bIdx := tx.Bucket([]byte("idxmeta")); bIdx != nil {
			docIDs = prefixSuffixes(bIdx, storage.MetaKeyPrefix(coll, "sub", sub))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	now := chatNow()
	var convs []*ChatConversation
	for _, docID := range docIDs {
		// The index is a prefix scan: the conversation's own sub decides.
		conv := chatFromDoc(s.loadDocByID(coll, docID, true), now)
		if conv != nil && sameSub(conv.Sub, sub) && (scenario == "" || conv.Scenario == scenario) {
			convs = append(convs, conv)
		}
	}
	sort.Slice(convs, func(i, j int) bool {
		return chatNewer(convs[i].ChatConversationSummary, convs[j].ChatConversationSummary)
	})
	return convs, nil
}

// chatFromDoc decodes a stored conversation; nil for a missing, expired or
// undecodable document. The document's own times win over the stored copy.
func chatFromDoc(doc *storage.Doc, now int64) *ChatConversation {
	if doc == nil || (doc.ExpiresAt > 0 && doc.ExpiresAt < now) {
		return nil
	}
	var conv ChatConversation
	if err := json.Unmarshal([]byte(doc.ContentMD), &conv); err != nil {
		return nil
	}
	if conv.Messages == nil {
		conv.Messages = []ChatMessage{}
	}
	conv.CreatedAt, conv.UpdatedAt, conv.ExpiresAt = doc.AddedAt, doc.UpdatedAt, doc.ExpiresAt
	conv.MessageCount = len(conv.Messages)
	return &conv
}
