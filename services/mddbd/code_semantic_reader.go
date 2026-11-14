package main

import (
	"sort"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// Reading semantic facts across documents (CODE-012).
//
// A document's facts say what it defines and what it references; who
// references or implements a definition is answered by the metadata index, a
// prefix scan over symRefs or symImpls for "<path>#<name> ". code_symbol and
// graph expansion both read facts through factReader, so the two answer the
// same question the same way.

// factReader reads facts inside one read transaction, loading each document
// once.
type factReader struct {
	collection  string
	bIdx, bDocs *bolt.Bucket
	docs        map[string]*sourceText // by document ID; nil when unreadable
	// more is set when a scan stopped at symbolScanDocs: the answer is cut.
	more bool
}

// factUse is what one document's facts say about a definition: the lines
// that use it (refsTo), or the local type implementing it (implsOf).
type factUse struct {
	st    *sourceText
	lines []int
	local string
}

func (s *Server) newFactReader(tx *bolt.Tx, collection string) *factReader {
	return &factReader{collection: collection, bIdx: tx.Bucket(s.BucketNames.IdxMeta),
		bDocs: tx.Bucket(s.BucketNames.Docs), docs: map[string]*sourceText{}}
}

// doc loads the document with this ID, once per reader.
func (r *factReader) doc(id string) *sourceText {
	if st, seen := r.docs[id]; seen {
		return st
	}
	var st *sourceText
	if r.bDocs != nil {
		if doc, err := loadDoc(r.bDocs.Get(storage.DocKey(r.collection, id))); err == nil && doc != nil {
			st = newSourceText(doc)
		}
	}
	r.docs[id] = st
	return st
}

// load loads documents by ID, skipping the unreadable.
func (r *factReader) load(ids []string) []*sourceText {
	var out []*sourceText
	for _, id := range ids {
		if st := r.doc(id); st != nil {
			out = append(out, st)
		}
	}
	return out
}

// atPath is the document with facts whose meta.path is p: where a fact's
// "<path>#<name>" points.
func (r *factReader) atPath(p string) *sourceText {
	if r.bIdx == nil {
		return nil
	}
	ids, _ := docIDsWithMeta(nil, r.bIdx, r.collection, fts.MetaKeyPath, p, symbolScanDocs)
	for _, st := range r.load(ids) {
		if st.facts != nil {
			return st
		}
	}
	return nil
}

// scan returns the IDs of the documents holding a value of key that starts
// with prefix, and their values.
func (r *factReader) scan(key, prefix string) []metaValueHit {
	hits, more := metaValuesWithPrefix(r.bIdx, r.collection, key, prefix, symbolScanDocs)
	r.more = r.more || more
	return hits
}

// refsTo lists the documents referencing target ("<path>#<qualified name>")
// with the lines of every use, ordered by path.
func (r *factReader) refsTo(target string) []factUse {
	var out []factUse
	seen := map[string]bool{}
	for _, h := range r.scan(MetaKeySymRefs, target+" ") {
		st := r.doc(h.DocID)
		if st == nil || st.facts == nil || seen[h.DocID] {
			continue
		}
		seen[h.DocID] = true // continuation values: the decoded facts merged them
		for _, ref := range st.facts.Refs {
			if ref.Path+"#"+ref.Name == target {
				out = append(out, factUse{st: st, lines: ref.Lines})
			}
		}
	}
	sortUses(out)
	return out
}

// implsOf lists the types implementing the interface target, by the name
// each implementing document gives it.
func (r *factReader) implsOf(target string) []factUse {
	var out []factUse
	for _, h := range r.scan(MetaKeySymImpls, target+" ") {
		if st := r.doc(h.DocID); st != nil && st.facts != nil {
			out = append(out, factUse{st: st, local: h.Value[len(target)+1:]})
		}
	}
	sortUses(out)
	return out
}

func sortUses(uses []factUse) {
	sort.SliceStable(uses, func(i, j int) bool { return docPath(uses[i].st.doc) < docPath(uses[j].st.doc) })
}

// defNamed is the definition the facts call name, or nil.
func (st *sourceText) defNamed(name string) *SymbolDefFact {
	if st.facts == nil {
		return nil
	}
	for i := range st.facts.Defs {
		if st.facts.Defs[i].Name == name {
			return &st.facts.Defs[i]
		}
	}
	return nil
}

// withSemanticFacts returns a search hit's document with its semantic facts,
// when the stored document has them. Results come without facts (CODE-010),
// but a passage names the definition holding it by the facts' qualified name
// (AGT-002, CODE-012). The facts index is consulted first — keyed by the
// content hash — so a collection without facts pays one index seek per code
// hit, not a document read. The hit itself is not changed: its meta may be
// shared with the result returned beside the passage.
func (s *Server) withSemanticFacts(collection string, doc *storage.Doc) *storage.Doc {
	if doc == nil || hasSemanticFacts(doc.Meta) || DocumentKind(doc) != fts.KindCode {
		return doc
	}
	out := doc
	_ = s.DBViewC(collection, func(tx *bolt.Tx) error {
		r := s.newFactReader(tx, collection)
		prefix := semanticFactsVersion + " " + semanticContentHash(doc.ContentMD) + " "
		for _, h := range r.scan(MetaKeySymIndex, prefix) {
			if h.DocID != doc.ID {
				continue // another document with the same content
			}
			if st := r.doc(h.DocID); st != nil && st.facts != nil {
				withFacts := *doc
				withFacts.Meta = st.doc.Meta
				out = &withFacts
			}
		}
		return nil
	})
	return out
}
