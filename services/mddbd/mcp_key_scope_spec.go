package main

import (
	"errors"
	"fmt"
	"strings"
)

// The checks a new key's readOnlyCollections (AGT-007) and memoryNamespace
// (AGT-008) pass before it is stored.

// maxMemoryNamespaceLen bounds an explicit namespace: it is stored on every
// memory document the key writes.
const maxMemoryNamespaceLen = 128

var (
	errReadOnlyCollectionsNeedCollections = errors.New("readOnlyCollections needs collections")
	errMemoryNamespaceNeedsLimit          = errors.New("memoryNamespace needs collections or a scenario")
	errMemoryNamespaceInvalid             = fmt.Errorf("memoryNamespace must be 1 to %d characters without surrounding spaces", maxMemoryNamespaceLen)
)

// validateKeyScopeSpec checks a key's readOnlyCollections are among its
// collections — by name, or resolved through aliases and "role:" targets as
// collections are — and that a memoryNamespace is set only on a key limited
// to collections, which alone has a namespace. readOnlyCollections naming
// every one of collections is the same key as readOnly: true, and is stored
// so, so tools/list hides the write tools from it too.
func (s *Server) validateKeyScopeSpec(spec *MCPAPIKey) error {
	if ns := spec.MemoryNamespace; ns != "" {
		switch {
		case len(ns) > maxMemoryNamespaceLen || strings.TrimSpace(ns) != ns:
			return errMemoryNamespaceInvalid
		case len(spec.Collections) == 0 && spec.Scenario == "":
			return errMemoryNamespaceNeedsLimit
		}
	}
	if len(spec.ReadOnlyCollections) == 0 {
		return nil
	}
	if len(spec.Collections) == 0 {
		return errReadOnlyCollectionsNeedCollections
	}
	named := map[string]bool{}
	for _, c := range spec.Collections {
		named[c] = true
	}
	reach := s.collectionSet(spec.Collections)
	covered := map[string]bool{}
	for _, c := range spec.ReadOnlyCollections {
		if !named[c] && !subsetOf(s.collectionSet([]string{c}), reach) {
			return fmt.Errorf("readOnlyCollections: %q is not among collections", c)
		}
		covered[c] = true
	}
	for c := range named {
		if !covered[c] {
			return nil
		}
	}
	spec.ReadOnly, spec.ReadOnlyCollections = true, nil
	return nil
}

// subsetOf reports whether set is non-empty and every member is in of.
func subsetOf(set, of map[string]bool) bool {
	for c := range set {
		if !of[c] {
			return false
		}
	}
	return len(set) > 0
}
