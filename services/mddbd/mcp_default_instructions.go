package main

import (
	"bytes"
	"context"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// Default MCP instructions for servers that hold code.
//
// An agent learns which tool answers which question from the instructions a
// server sends at initialize. Without any, a project had to say it in its own
// rules file, at several hundred tokens per session. The default below says
// it in a few lines, and only where it applies: the operator set no
// instructions of their own, the request's key is bound to no chat scenario,
// the code tools are in its toolsets, and some collection holds code. A chat
// or a document store never sees it.

// mcpInstructionsNone, as MDDB_MCP_INSTRUCTIONS, turns the default off.
const mcpInstructionsNone = "none"

// mcpDefaultCodeInstructions is what an MCP client is told when the operator
// set no instructions and the server holds code (see defaultInstructions): the
// few lines an agent otherwise learns from a project's own rules file, at a
// cost of several hundred tokens per session. Under a budget test.
const mcpDefaultCodeInstructions = "Code collections (indexed by mddb-cli autoindex): for a known symbol call " +
	"code_symbol with format \"lines\" and include set to the relations you need (definitions, references, " +
	"callers, callees, implementations); exclude_tests leaves tests out. For anything else call code_search: " +
	"mode exact for an identifier or literal, words for keywords, meaning for a description. Both answer as " +
	"path:line lines with a count; when it says N of N the list is complete, so do not repeat the search with " +
	"grep. The other search tools return JSON and cost more."

// holdsCodeTTL is how long an answer of holdsCode is reused: initialize is
// not a hot path, but a client that reconnects in a loop should not walk
// every collection each time.
const holdsCodeTTL = time.Minute

// codeProbe caches whether the server holds code.
type codeProbe struct {
	mu    sync.Mutex
	at    time.Time
	holds bool
}

// holdsCode reports whether any collection has a document that defines a
// symbol — the mark of source indexed as code.
func (s *Server) holdsCode() bool {
	p := &s.codeProbe
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.at.IsZero() && time.Since(p.at) < holdsCodeTTL {
		return p.holds
	}
	p.at, p.holds = time.Now(), false
	for _, coll := range s.knownCollections() {
		if s.collectionDefinesSymbols(coll) {
			p.holds = true
			break
		}
	}
	return p.holds
}

// collectionDefinesSymbols reports whether the metadata index of a
// collection has any `defines` entry.
func (s *Server) collectionDefinesSymbols(collection string) bool {
	found := false
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		if bIdx := tx.Bucket(s.BucketNames.IdxMeta); bIdx != nil {
			prefix := storage.MetaValuePrefix(collection, MetaKeyDefines, "")
			k, _ := bIdx.Cursor().Seek(prefix)
			found = k != nil && bytes.HasPrefix(k, prefix)
		}
		return nil
	})
	return found
}

// defaultInstructions is the instructions sent when the operator set none:
// the code hint where it applies (see the top of this file), else nothing.
func (h *MCPHandler) defaultInstructions(ctx context.Context) string {
	dc, ok := h.client.(*DirectClient)
	if !ok || dc.server == nil || !builtinToolAllowed(ctx, "code_search") || !dc.server.holdsCode() {
		return ""
	}
	return mcpDefaultCodeInstructions
}
