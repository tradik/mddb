package lspindex

import (
	"context"
	"path/filepath"
)

// Phases two and three: references and implementations, each location
// turned into a fact on the file it points into.

// referencesOf records every reference to s on the file it occurs in.
func (r *run) referencesOf(ctx context.Context, s *symbol) error {
	params := referenceParams{positionParams: r.at(s)}
	r.mu.Lock()
	r.refsAsked++
	r.mu.Unlock()
	var locs []location
	if ok, err := r.request(ctx, methodReferences, params, &locs); !ok {
		return err
	}
	target := s.target()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range locs {
		rel, ok := r.indexed(l.URI)
		if !ok {
			r.stats.Dropped++
			continue
		}
		byTarget := r.refs[rel]
		if byTarget == nil {
			byTarget = map[string]map[int]bool{}
			r.refs[rel] = byTarget
		}
		if byTarget[target] == nil {
			byTarget[target] = map[int]bool{}
		}
		byTarget[target][l.Range.Start.Line+1] = true
		if rel != s.path {
			r.stats.CrossFileRefs++
		}
	}
	return nil
}

// implementationsOf records, on each implementing file, which of its
// definitions implements s.
func (r *run) implementationsOf(ctx context.Context, s *symbol) error {
	var locs []location
	if ok, err := r.request(ctx, "textDocument/implementation", r.at(s), &locs); !ok {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range locs {
		rel, ok := r.indexed(l.URI)
		var local *symbol
		if ok {
			local = innermost(r.defs[rel], l.Range.Start)
		}
		if local == nil {
			r.stats.Dropped++
			continue
		}
		if local == s {
			continue // some servers list the interface among its implementations
		}
		if r.impls[rel] == nil {
			r.impls[rel] = map[Impl]bool{}
		}
		r.impls[rel][Impl{Target: s.target(), Local: local.def.Name}] = true
	}
	return nil
}

// at is the position of s's name.
func (r *run) at(s *symbol) positionParams {
	return positionParams{TextDocument: textDocumentID{URI: fileURI(filepath.Join(r.root, filepath.FromSlash(s.path)))}, Position: s.pos}
}

// indexed maps a location's URI to an indexed file. Locations elsewhere —
// the standard library, dependencies, files not being indexed — are not
// facts about this tree.
func (r *run) indexed(uri string) (string, bool) {
	p, ok := uriPath(uri)
	if !ok {
		return "", false
	}
	// Both paths are absolute, so Rel cannot fail; a path outside the root
	// comes back as ../..., which is no indexed file.
	rel, _ := filepath.Rel(r.root, p)
	rel = filepath.ToSlash(rel)
	_, ok = r.defs[rel]
	return rel, ok
}
