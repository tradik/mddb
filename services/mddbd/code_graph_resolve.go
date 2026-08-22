package main

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
)

// Edge resolution for CODE-005.
//
// Every lookup here is a prefix scan of the metadata index that already backs
// `meta.*` filters — `meta|collection|defines|.hero-banner|` yields the ids of
// the documents declaring that selector. No second index, no scan of the
// collection.

// codeDocRef is the slice of a document the graph needs. Content is never
// loaded: a traversal touches many documents and none of them for their text.
type codeDocRef struct {
	DocID    string
	Key      string
	Lang     string
	Language string
	Defines  []string
	Uses     []string
	Imports  []string
}

var errGraphDocNotFound = errors.New("document not found in collection")

func refFrom(doc *storage.Doc) *codeDocRef {
	return &codeDocRef{
		DocID:    doc.ID,
		Key:      doc.Key,
		Lang:     doc.Lang,
		Language: CodeLanguage(doc),
		Defines:  doc.Meta[MetaKeyDefines],
		Uses:     doc.Meta[MetaKeyUses],
		Imports:  doc.Meta[MetaKeyImports],
	}
}

// loadCodeDocByKey finds a document by key without knowing its language.
//
// The by-key index is keyed `bykey|collection|key|lang`, and a caller asking
// about `theme/style.css` does not know or care which language variant was
// stored — a stylesheet has no translations.
func (s *Server) loadCodeDocByKey(collection, key string) (*codeDocRef, error) {
	var ref *codeDocRef
	err := s.DBView(func(tx *bolt.Tx) error {
		d := s.lookupByKeyTx(tx, collection, key)
		if d == nil {
			return fmt.Errorf("%w: %s/%s", errGraphDocNotFound, collection, key)
		}
		ref = d
		return nil
	})
	return ref, err
}

func (s *Server) lookupByKeyTx(tx *bolt.Tx, collection, key string) *codeDocRef {
	bByK := tx.Bucket(s.BucketNames.ByKey)
	bDocs := tx.Bucket(s.BucketNames.Docs)
	if bByK == nil || bDocs == nil {
		return nil
	}
	prefix := []byte("bykey|" + collection + "|" + key + "|")
	c := bByK.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		raw := bDocs.Get(storage.DocKey(collection, string(v)))
		if raw == nil {
			continue
		}
		doc, err := loadDoc(raw)
		if err != nil {
			continue
		}
		return refFrom(doc)
	}
	return nil
}

// docIDsWithMeta returns the ids indexed under one metadata key/value pair.
func docIDsWithMeta(tx *bolt.Tx, bIdx *bolt.Bucket, collection, metaKey, value string, limit int) ([]string, bool) {
	prefix := storage.MetaKeyPrefix(collection, metaKey, value)
	var ids []string
	c := bIdx.Cursor()
	for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
		if len(ids) >= limit {
			return ids, true
		}
		ids = append(ids, string(k[len(prefix):]))
	}
	return ids, false
}

type resolvedEdge struct {
	edge GraphEdge
	doc  *codeDocRef
}

// neighbours resolves one document's edges in the requested direction.
func (s *Server) neighbours(req GraphRequest, from *codeDocRef) ([]resolvedEdge, bool) {
	var out []resolvedEdge
	truncated := false

	_ = s.DBView(func(tx *bolt.Tx) error {
		bIdx := tx.Bucket(s.BucketNames.IdxMeta)
		bDocs := tx.Bucket(s.BucketNames.Docs)
		if bIdx == nil || bDocs == nil {
			return nil
		}
		r := &edgeResolver{
			srv: s, tx: tx, bIdx: bIdx, bDocs: bDocs,
			req: req, from: from, cache: map[string]*codeDocRef{},
		}
		if req.wantsOut() {
			r.outgoing()
		}
		if req.wantsIn() {
			r.incoming()
		}
		out, truncated = r.edges, r.truncated
		return nil
	})

	return dedupeEdges(out), truncated
}

type edgeResolver struct {
	srv       *Server
	tx        *bolt.Tx
	bIdx      *bolt.Bucket
	bDocs     *bolt.Bucket
	req       GraphRequest
	from      *codeDocRef
	cache     map[string]*codeDocRef
	edges     []resolvedEdge
	truncated bool
	degree    int
}

// outgoing: what this document depends on.
func (r *edgeResolver) outgoing() {
	// A class or id it applies, declared elsewhere.
	for _, sym := range r.from.Uses {
		r.matchSymbol(sym, MetaKeyDefines, EdgeUsesSelector, false)
	}
	// A path it pulls in.
	for _, target := range r.from.Imports {
		// A document importing itself is a mistake in the source, not an
		// edge; a self-loop is noise in every answer the graph gives.
		if doc := r.resolveTarget(target); doc != nil && doc.Key != r.from.Key {
			r.add(GraphEdge{
				From: r.from.Key, To: doc.Key, Kind: EdgeImports,
				Symbol: target, Direction: string(GraphOut),
			}, doc)
		}
	}
}

// incoming: what depends on this document — the "what breaks if I change it"
// direction.
func (r *edgeResolver) incoming() {
	for _, sym := range r.from.Defines {
		r.matchSymbol(sym, MetaKeyUses, EdgeUsesSelector, true)
	}
	// Documents importing this one. Import values are stored resolved
	// (`assets/render.js`), but an ES specifier may omit the extension, so
	// the extension-less form is a second, equally valid stored value.
	for _, form := range importForms(r.from.Key) {
		ids, cut := docIDsWithMeta(r.tx, r.bIdx, r.req.Collection, MetaKeyImports, form, r.req.MaxDegree)
		r.truncated = r.truncated || cut
		for _, id := range ids {
			doc := r.load(id)
			if doc == nil || doc.Key == r.from.Key {
				continue
			}
			r.add(GraphEdge{
				From: doc.Key, To: r.from.Key, Kind: EdgeImports,
				Symbol: form, Direction: string(GraphIn),
			}, doc)
		}
	}
}

// matchSymbol connects this document to every other indexed under the opposite
// meta key for the same symbol.
func (r *edgeResolver) matchSymbol(sym, otherKey string, kind EdgeKind, inbound bool) {
	ids, cut := docIDsWithMeta(r.tx, r.bIdx, r.req.Collection, otherKey, sym, r.req.MaxDegree)
	r.truncated = r.truncated || cut
	for _, id := range ids {
		doc := r.load(id)
		if doc == nil || doc.Key == r.from.Key {
			continue
		}
		e := GraphEdge{Kind: kind, Symbol: sym}
		if inbound {
			e.From, e.To, e.Direction = doc.Key, r.from.Key, string(GraphIn)
		} else {
			e.From, e.To, e.Direction = r.from.Key, doc.Key, string(GraphOut)
		}
		r.add(e, doc)
	}
}

// resolveTarget finds the document an import path names, trying the code
// extensions for a specifier that omitted one (`./helpers` → `helpers.js`).
func (r *edgeResolver) resolveTarget(target string) *codeDocRef {
	if doc := r.srv.lookupByKeyTx(r.tx, r.req.Collection, target); doc != nil {
		return doc
	}
	if path.Ext(target) != "" {
		return nil
	}
	for _, ext := range importExtensions {
		if doc := r.srv.lookupByKeyTx(r.tx, r.req.Collection, target+ext); doc != nil {
			return doc
		}
	}
	return nil
}

// importExtensions is the order in which an extension-less specifier is tried.
// Sorted and fixed, so two servers resolve an ambiguous specifier the same way.
var importExtensions = []string{".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".css", ".scss", ".less"}

// importForms lists the stored `imports` values that would point at this key.
func importForms(key string) []string {
	forms := []string{key}
	if ext := path.Ext(key); ext != "" {
		for _, known := range importExtensions {
			if ext == known {
				forms = append(forms, strings.TrimSuffix(key, ext))
				break
			}
		}
	}
	return forms
}

func (r *edgeResolver) load(docID string) *codeDocRef {
	if d, ok := r.cache[docID]; ok {
		return d
	}
	raw := r.bDocs.Get(storage.DocKey(r.req.Collection, docID))
	if raw == nil {
		r.cache[docID] = nil
		return nil
	}
	doc, err := loadDoc(raw)
	if err != nil {
		r.cache[docID] = nil
		return nil
	}
	ref := refFrom(doc)
	r.cache[docID] = ref
	return ref
}

// add records an edge, stopping at the degree limit so one popular selector
// cannot pull an entire theme into the answer.
func (r *edgeResolver) add(e GraphEdge, doc *codeDocRef) {
	if r.degree >= r.req.MaxDegree {
		r.truncated = true
		return
	}
	r.degree++
	r.edges = append(r.edges, resolvedEdge{edge: e, doc: doc})
}

// dedupeEdges collapses the duplicates a compound selector produces: a
// stylesheet declaring `.card` and `.card .title` is reached twice through the
// same relationship.
func dedupeEdges(in []resolvedEdge) []resolvedEdge {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, e := range in {
		id := e.edge.From + "\x00" + e.edge.To + "\x00" + string(e.edge.Kind) + "\x00" + e.edge.Symbol
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].edge.Symbol < out[j].edge.Symbol })
	return out
}
