package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Memory namespaces (AGT-008): the memory tools for a key limited to
// collections.
//
// The memory store is three collections every session shares, so a key
// limited to collections used to be refused every memory_* tool: it would
// have read every user's sessions. In the "one key per project" layout that
// left an agent no way to hand a session over to the next one.
//
// Now a limited key works in a namespace of its own. The tools stamp every
// session, message and summary the key writes with the namespace, list and
// recall only what carries it, and refuse a session ID from outside it as
// if the session did not exist. Sessions stored before 2.19.11 carry no
// namespace: they are the global namespace, which an unscoped key and a
// main-auth user still see whole (they see every namespace too, as before).
//
// The namespace is derived from the key, so a deleted key leaves its memory
// in place and a new key never inherits it; a key's explicit
// memoryNamespace lets two keys of one project share one. The two forms
// carry different prefixes, so an explicit name can never equal a derived
// one.

// memoryNamespaceMeta is the meta field that holds a document's namespace.
const memoryNamespaceMeta = "memoryNamespace"

// Namespace prefixes: one derived from a key, one named on keys.
const (
	memoryNamespaceKeyPrefix   = "key:"
	memoryNamespaceNamedPrefix = "ns:"
)

// errMemorySessionNotFound answers a namespaced call on a session outside
// its namespace exactly as on one that does not exist.
var errMemorySessionNotFound = errors.New("session not found")

// keyMemoryNamespace is the namespace of the key: its explicit name when
// set, else 128 bits of the key's SHA-256, which identify it without
// revealing anything usable as the key.
func keyMemoryNamespace(key, explicit string) string {
	if explicit != "" {
		return memoryNamespaceNamedPrefix + explicit
	}
	sum := sha256.Sum256([]byte(key))
	return memoryNamespaceKeyPrefix + hex.EncodeToString(sum[:16])
}

// memoryNamespaceFromContext is the namespace the request's memory calls are
// confined to: ok only for a key limited to collections. keyScopeMayCall has
// refused such a key every memory tool when its namespace is empty.
func memoryNamespaceFromContext(ctx context.Context) (string, bool) {
	k, ok := keyScopeFromContext(ctx)
	if !ok || !k.limited() {
		return "", false
	}
	return k.memoryNamespace, true
}

// stampMemoryNamespace writes the request's namespace into meta, over any
// value the caller's own meta set, so a key cannot write into another
// namespace. meta is left alone outside a namespace.
func stampMemoryNamespace(ctx context.Context, meta map[string][]string) {
	if ns, ok := memoryNamespaceFromContext(ctx); ok {
		meta[memoryNamespaceMeta] = []string{ns}
	}
}

// checkMemorySession refuses a namespaced call on a session that is not one
// of its namespace's, with errMemorySessionNotFound whether the session is
// another namespace's or does not exist. Outside a namespace it lets every
// session through, as before.
func (s *MCPToolServer) checkMemorySession(ctx context.Context, sessionID string) error {
	ns, ok := memoryNamespaceFromContext(ctx)
	if !ok {
		return nil
	}
	doc, err := s.client.Get(ctx, &MCPGetRequest{Collection: memorySessionsCollection, Key: sessionID, Lang: "en"})
	if err != nil || metaFirst(doc.Meta, memoryNamespaceMeta) != ns {
		return fmt.Errorf("%w: %s", errMemorySessionNotFound, sessionID)
	}
	return nil
}
