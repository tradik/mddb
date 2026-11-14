package main

import (
	"context"
	"strings"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"

	"mddb-cli/internal/lspindex"
)

// Keeping facts in step with the checkout (CODE-011).
//
// Ingest skips a document whose content is unchanged (skipDuplicates) and
// looks at nothing else, yet a file's facts change whenever another file
// starts calling it. So the run compares facts itself: symIndex carries the
// content's hash and a digest of the facts, and one facet request over
// symIndex returns every stored pair at once — the facet reads the metadata
// index, which holds symIndex although search results and listings hide it.
//
//   - pair stored             → content and facts unchanged: not sent;
//   - otherwise               → sent with ingest, which writes it when the
//     content changed and skips it when only the facts did;
//   - still missing afterwards (a second facet request) → the content was
//     unchanged: the document is read and its meta rewritten with the new
//     facts (PATCH /v1/update replaces the meta, so all of it is sent).
//
// Two facet requests per run, plus a GET and a PATCH per document whose
// facts alone changed.

// semanticSync carries the facts through one run's ingest.
type semanticSync struct {
	facts  map[string]fileFacts
	stored map[string]int // contentHash+" "+factsDigest -> documents holding it
	sent   []sentFacts
	rep    *semanticReport
}

// sentFacts is a document sent with facts, kept to check it landed.
type sentFacts struct {
	key  string
	pair string
	meta map[string][]string
}

// newSemanticSync reads the facts the collection holds; nil when there are
// no facts to send. A collection that cannot be asked is treated as holding
// none: every file with facts is sent, and ingest still skips the content.
func newSemanticSync(ctx context.Context, client *mddb.Client, rep *autoindexReport, facts map[string]fileFacts) *semanticSync {
	if len(facts) == 0 {
		return nil
	}
	stored, err := storedFactPairs(ctx, client, rep.Collection)
	if err != nil {
		rep.Semantic.warn("stored facts unavailable, every file with facts is sent: " + err.Error())
		stored = map[string]int{}
	}
	return &semanticSync{facts: facts, stored: stored, rep: rep.Semantic}
}

// count folds the semantic outcome into the run's totals: a document held
// back was unchanged, and one whose facts alone were rewritten, which
// ingest counted as unchanged, was updated.
func (s *semanticSync) count(rep *autoindexReport) {
	rep.Unchanged += s.rep.Unchanged - s.rep.FactsUpdated
	rep.Updated += s.rep.FactsUpdated
}

// storedFactPairs counts the collection's symIndex values by content hash
// and facts digest. The source (server and version) is left out: a server
// upgrade that finds the same facts rewrites nothing.
func storedFactPairs(ctx context.Context, client *mddb.Client, collection string) (map[string]int, error) {
	agg, err := client.Aggregate(ctx, mddb.AggregateRequest{Collection: collection,
		Facets: []mddb.FacetRequest{{Field: lspindex.KeyIndex}}, MaxFacetSize: pruneFacetLimit})
	if err != nil {
		return nil, err
	}
	pairs := map[string]int{}
	for _, b := range agg.Facets[lspindex.KeyIndex] {
		if p := factPair(b.Value); p != "" {
			pairs[p] += b.Count
		}
	}
	return pairs, nil
}

// factPair is "<contentHash> <factsDigest>" of a symIndex value, "" when the
// value is not one of format v1.
func factPair(symIndex string) string {
	f := strings.Fields(symIndex)
	if len(f) < 4 || f[0] != "v1" {
		return ""
	}
	return f[1] + " " + f[2]
}

// attach adds a file's facts to its document's meta. It reports false when
// the collection already holds this content with these facts: the document
// need not be sent.
func (s *semanticSync) attach(rel string, content []byte, meta map[string][]string) bool {
	ff, ok := s.facts[rel]
	if !ok {
		return true
	}
	sym := lspindex.Encode(string(content), ff.source, ff.facts)
	pair := factPair(sym[lspindex.KeyIndex][0])
	if s.stored[pair] > 0 {
		s.stored[pair]--
		s.rep.Unchanged++
		return false
	}
	for k, v := range sym {
		meta[k] = v
	}
	s.sent = append(s.sent, sentFacts{key: rel, pair: pair, meta: sym})
	s.rep.Sent++
	return true
}

// reconcile finds the documents whose facts did not land — ingest skipped
// them, their content being unchanged — and rewrites their meta.
func (s *semanticSync) reconcile(ctx context.Context, client *mddb.Client, collection, lang string) {
	if len(s.sent) == 0 {
		return
	}
	stored, err := storedFactPairs(ctx, client, collection)
	if err != nil {
		s.rep.warn("facts not checked after sending: " + err.Error())
		return
	}
	for _, d := range s.sent {
		if stored[d.pair] > 0 {
			stored[d.pair]--
			continue
		}
		s.updateFacts(ctx, client, collection, lang, d)
	}
}

// updateFacts rewrites one document's meta with new facts, keeping every
// other key it has. The content it holds must be the file's: facts are only
// true of the content they came from, and the server would drop them anyway.
func (s *semanticSync) updateFacts(ctx context.Context, client *mddb.Client, collection, lang string, d sentFacts) {
	doc, err := client.Get(ctx, mddb.GetRequest{Collection: collection, Key: d.key, Lang: lang})
	if err != nil {
		s.rep.warn("facts for " + d.key + ": " + err.Error())
		return
	}
	if contentHash, _, _ := strings.Cut(d.pair, " "); lspindex.ContentHash(doc.ContentMD) != contentHash {
		s.rep.warn("facts for " + d.key + " not stored: the collection holds other content than the file")
		return
	}
	meta := make(map[string][]string, len(doc.Meta)+len(d.meta))
	for k, v := range doc.Meta {
		meta[k] = v
	}
	for _, k := range []string{lspindex.KeyIndex, lspindex.KeyDefs, lspindex.KeyRefs, lspindex.KeyImpls} {
		delete(meta, k)
	}
	for k, v := range d.meta {
		meta[k] = v
	}
	body := map[string]any{"collection": collection, "key": d.key, "lang": lang, "meta": meta}
	if _, err := client.Do(ctx, "PATCH", "/v1/update", body); err != nil {
		s.rep.warn("facts for " + d.key + ": " + err.Error())
		return
	}
	s.rep.FactsUpdated++
}
