package main

import (
	"context"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// Reading the conversation log (#406) across users: every live conversation
// of the caller's chat collection, or — with a scenario — the ones the
// metadata index lists for it. The log is a view over the conversation
// documents; there is no second copy to keep in step.

// chatMaxExportTurns bounds one export.
const chatMaxExportTurns = 100_000

// chatAllConversations reads the live conversations of the caller's chat
// collection, optionally of one scenario.
func (s *Server) chatAllConversations(ctx context.Context, scenario string) ([]*ChatConversation, error) {
	coll := chatCollection(ctx)
	var docIDs []string
	err := s.DBViewC(coll, func(tx *bolt.Tx) error {
		if scenario != "" {
			if bIdx := tx.Bucket([]byte("idxmeta")); bIdx != nil {
				docIDs = prefixSuffixes(bIdx, storage.MetaKeyPrefix(coll, "scenario", scenario))
			}
			return nil
		}
		if bDocs := tx.Bucket([]byte("docs")); bDocs != nil {
			docIDs = prefixSuffixes(bDocs, []byte("doc|"+coll+"|"))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	now := chatNow()
	convs := make([]*ChatConversation, 0, len(docIDs))
	for _, docID := range docIDs {
		conv := chatFromDoc(s.loadDocByID(coll, docID, true), now)
		// The index is a prefix scan: the conversation's own scenario decides.
		if conv != nil && (scenario == "" || conv.Scenario == scenario) {
			convs = append(convs, conv)
		}
	}
	return convs, nil
}

// chatLogMatching is every logged turn the filter selects, newest first.
func (s *Server) chatLogMatching(ctx context.Context, f ChatLogFilter) ([]ChatLogTurn, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	convs, err := s.chatAllConversations(ctx, f.Scenario)
	if err != nil {
		return nil, err
	}
	return chatLogTurns(convs, f), nil
}

// chatLog is one page of the log.
func (s *Server) chatLog(ctx context.Context, f ChatLogFilter) (ChatLogPage, error) {
	turns, err := s.chatLogMatching(ctx, f)
	if err != nil {
		return ChatLogPage{}, err
	}
	return pageChatLog(turns, f.Limit, f.Cursor)
}

// chatLogExport is every turn the filter selects, at most
// chatMaxExportTurns of them; the cursor and limit are ignored.
func (s *Server) chatLogExport(ctx context.Context, f ChatLogFilter) ([]ChatLogTurn, error) {
	turns, err := s.chatLogMatching(ctx, f)
	if err != nil {
		return nil, err
	}
	return turns[:min(len(turns), chatMaxExportTurns)], nil
}
