package main

import (
	bolt "go.etcd.io/bbolt"
)

// Graph expansion from semantic facts (CODE-012).
//
// A seed document that carries facts is expanded by what they say: its
// callers are the documents whose symRefs point at its definitions, its
// callees the documents its own symRefs point at, its implementations the
// documents whose symImpls name its interfaces. The same factReader answers
// code_symbol, so a graph search and a symbol lookup agree. A seed without
// facts expands as before; implementations need facts and add nothing
// without them.

// factNeighbour is a document related to another through facts, and the
// qualified name relating them.
type factNeighbour struct {
	st     *sourceText
	symbol string
}

// factNeighbours lists the documents related to st by one relation.
func factNeighbours(r *factReader, st *sourceText, kind string) []factNeighbour {
	self := docPath(st.doc)
	switch kind {
	case RelCallers:
		return usesOfDefs(st, self, r.refsTo, func(*sourceText) bool { return true })
	case RelTests:
		return usesOfDefs(st, self, r.refsTo, func(u *sourceText) bool { return isTestPath(u.doc.Key) })
	case RelImplementations:
		return usesOfDefs(st, self, r.implsOf, func(*sourceText) bool { return true })
	case RelCallees:
		var out []factNeighbour
		for _, ref := range st.facts.Refs {
			if to := r.atPath(ref.Path); to != nil {
				out = append(out, factNeighbour{st: to, symbol: ref.Name})
			}
		}
		return out
	}
	return nil
}

// usesOfDefs lists, for each definition in st, the documents lookup finds for
// it (references or implementations) that keep accepts.
func usesOfDefs(st *sourceText, self string, lookup func(string) []factUse, keep func(*sourceText) bool) []factNeighbour {
	var out []factNeighbour
	for _, d := range st.facts.Defs {
		for _, use := range lookup(self + "#" + d.Name) {
			if keep(use.st) {
				out = append(out, factNeighbour{st: use.st, symbol: d.Name})
			}
		}
	}
	return out
}

// semanticNeighbours expands one seed by one relation from its facts, one
// hop. ok is false when the seed has no facts.
func (s *Server) semanticNeighbours(collection string, seed graphSeed, kind string, opts GraphExpandOptions,
	seedKeys map[string]bool) (out []GraphExpansion, ok bool) {
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		ref := s.lookupByKeyTx(tx, collection, seed.Key)
		if ref == nil {
			return nil
		}
		r := s.newFactReader(tx, collection)
		st := r.doc(ref.DocID)
		if st == nil || st.facts == nil {
			return nil
		}
		ok = true
		added := map[string]bool{}
		for _, n := range factNeighbours(r, st, kind) {
			key := n.st.doc.Key
			if key == seed.Key || seedKeys[key] || added[key] {
				continue
			}
			if len(out) >= opts.MaxNeighbours {
				break
			}
			added[key] = true
			out = append(out, GraphExpansion{Key: key, FromKey: seed.Key, Symbol: n.symbol, Kind: kind,
				Depth: 1, Score: seed.Score * opts.Decay})
		}
		return nil
	})
	return out, ok
}
