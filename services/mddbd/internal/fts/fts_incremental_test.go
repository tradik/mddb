package fts

import (
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Incremental re-indexing (#367) must leave the index exactly as indexing
// the same documents into an empty index would. The property test drives
// random sequences of updates and compares after every step.

// propDoc is a document as a writer sees it: what ftsBulkDoc in the server
// turns into a BulkDoc.
type propDoc struct {
	id, content, lang, kind string
	meta                    map[string]string
}

func (d propDoc) bulk() BulkDoc {
	fields := map[string]string{"content": d.content}
	for k, v := range d.meta {
		fields["meta."+k] = v
	}
	return BulkDoc{DocID: d.id, Content: d.content, Lang: d.lang, Kind: d.kind, Fields: fields}
}

var propWords = strings.Fields("alpha beta gamma delta river stone forest cloud night zenith aurora " +
	"running runner runs parse_config ParseConfig http_server the and of zażółć gęślą jaźń kot koty")

func propText(r *rand.Rand, n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = propWords[r.Intn(len(propWords))]
	}
	return strings.Join(words, " ")
}

// propStep applies one random update to d and names it.
func propStep(r *rand.Rand, d *propDoc) string {
	keys := []string{"title", "tags", "license", "url", "vulnerable"}
	switch r.Intn(8) {
	case 0:
		d.content = propText(r, 1+r.Intn(12))
		return "content"
	case 7: // GO-048: the content postings go, the meta fields stay
		d.content = ""
		return "content emptied"
	case 1, 2:
		d.meta[keys[r.Intn(len(keys))]] = propText(r, 1+r.Intn(4))
		return "meta add/change"
	case 3:
		for k := range d.meta {
			delete(d.meta, k)
			return "meta remove"
		}
		return "meta remove (none)"
	case 4:
		d.lang = []string{"en", "pl", "xx"}[r.Intn(3)]
		return "lang"
	case 5:
		d.kind = []string{"", KindCode}[r.Intn(2)]
		return "kind"
	default:
		return "identical resend"
	}
}

func newPropIndex(t testing.TB) *FTSIndex {
	t.Helper()
	db, err := bolt.Open(t.TempDir()+"/fts.db", 0600, &bolt.Options{NoSync: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	idx := NewFTSIndex(db)
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	reg := NewLangRegistry("en")
	RegisterDefaultLanguages(reg)
	idx.SetStemmer(NewPorterStemmer())
	idx.SetLangRegistry(reg)
	return idx
}

func bulkOf(docs []propDoc) []BulkDoc {
	out := make([]BulkDoc, len(docs))
	for i, d := range docs {
		out[i] = d.bulk()
	}
	return out
}

// assertSameIndex fails unless two indexes hold the same bytes and answer
// every scorer the same way.
func assertSameIndex(t *testing.T, step string, got, want *FTSIndex) {
	t.Helper()
	g, w := dumpBuckets(t, got), dumpBuckets(t, want)
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("after %s the index differs from a rebuild:\n%s", step, strings.Join(bucketDiff(g, w), "\n"))
	}
	weights := map[string]float64{"content": 1, "meta.title": 2, "meta.tags": 1.5, "meta.license": 1, "meta.url": 1, "meta.vulnerable": 1}
	for _, q := range []string{"alpha", "running", "zenith aurora", "parse_config", "koty", "river stone"} {
		gs, err1 := got.SearchBM25("c", q, 0, DefaultBM25)
		ws, err2 := want.SearchBM25("c", q, 0, DefaultBM25)
		assertSameRanking(t, step, "BM25", q, rankedPair{gs, ws, err1, err2})
		tokens := got.TokenizeQuery("c", q)
		gs, err1 = got.SearchBM25F("c", tokens, 0, weights, DefaultBM25)
		ws, err2 = want.SearchBM25F("c", tokens, 0, weights, DefaultBM25)
		assertSameRanking(t, step, "BM25F", q, rankedPair{gs, ws, err1, err2})
		gs, err1 = got.SearchPhrase("c", q, 0)
		ws, err2 = want.SearchPhrase("c", q, 0)
		assertSameRanking(t, step, "phrase", q, rankedPair{gs, ws, err1, err2})
	}
}

// bucketDiff lists, sorted, every key the incremental dump g and the
// rebuilt dump w disagree on.
func bucketDiff(g, w map[string]string) []string {
	var diff []string
	for k, v := range w {
		if g[k] != v {
			diff = append(diff, fmt.Sprintf("  %q: incremental %q, rebuild %q", k, g[k], v))
		}
	}
	for k, v := range g {
		if _, ok := w[k]; !ok {
			diff = append(diff, fmt.Sprintf("  %q: incremental %q, rebuild has none", k, v))
		}
	}
	sort.Strings(diff)
	return diff
}

// rankedPair is one search run on the incremental index (got) and on the
// rebuilt one (want), with the error each returned.
type rankedPair struct {
	got, want       []FTSResult
	gotErr, wantErr error
}

// assertSameRanking fails unless both searches succeeded and ranked alike.
func assertSameRanking(t *testing.T, step, scorer, q string, p rankedPair) {
	t.Helper()
	if p.gotErr != nil || p.wantErr != nil || !sameResults(p.got, p.want) {
		t.Fatalf("after %s %s %q: incremental %v, rebuild %v", step, scorer, q, p.got, p.want)
	}
}

// sameResults compares ranked results by document, score and order; ties
// are ordered by document so map iteration cannot fail the comparison.
func sameResults(a, b []FTSResult) bool {
	if len(a) != len(b) {
		return false
	}
	norm := func(rs []FTSResult) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = fmt.Sprintf("%s=%.12g", r.DocID, r.Score)
		}
		sort.Strings(out)
		return out
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// TestIncrementalIndexEqualsRebuild is the property: after any sequence of
// updates, the incrementally maintained index is byte-for-byte the index a
// from-scratch build of the final documents produces — postings, positions,
// field postings, document lengths, collection and field statistics (so
// avgdl and df), reverse indexes and signatures — and every scorer ranks
// the same.
func TestIncrementalIndexEqualsRebuild(t *testing.T) {
	seeds, steps := 40, 40
	if testing.Short() {
		seeds = 5
	}
	for seed := int64(1); seed <= int64(seeds); seed++ {
		r := rand.New(rand.NewSource(seed)) // #nosec G404 -- a seeded, reproducible test sequence, not a secret
		background := []propDoc{
			{id: "bg1", content: "alpha river stone running", lang: "en", meta: map[string]string{"title": "zenith", "tags": "alpha"}},
			{id: "bg2", content: "koty zażółć gęślą jaźń", lang: "pl", meta: map[string]string{"license": "MIT"}},
			{id: "bg3", content: "parse_config http_server", lang: "en", kind: KindCode, meta: map[string]string{}},
		}
		docs := []propDoc{
			{id: "d1", content: propText(r, 8), lang: "en", meta: map[string]string{"title": propText(r, 2)}},
			{id: "d2", content: propText(r, 5), lang: "en", meta: map[string]string{}},
		}
		inc := newPropIndex(t)
		if err := inc.IndexDocs("c", bulkOf(append(append([]propDoc{}, background...), docs...))); err != nil {
			t.Fatal(err)
		}
		var history []string
		for step := 0; step < steps; step++ {
			i := r.Intn(len(docs))
			before := docs[i]
			before.meta = maps.Clone(before.meta)
			docs[i].meta = maps.Clone(docs[i].meta)
			what := propStep(r, &docs[i])
			history = append(history, fmt.Sprintf("%s:%s", docs[i].id, what))

			batch := []BulkDoc{docs[i].bulk()}
			switch r.Intn(4) {
			case 0: // the same document twice in one batch: another version first
				other := before
				other.meta = maps.Clone(before.meta)
				propStep(r, &other)
				batch = []BulkDoc{other.bulk(), docs[i].bulk()}
			case 1: // the other document, unchanged, in the same batch
				batch = append(batch, docs[1-i].bulk())
			}
			if err := inc.IndexDocs("c", batch); err != nil {
				t.Fatal(err)
			}

			fresh := newPropIndex(t)
			if err := fresh.IndexDocs("c", bulkOf(append(append([]propDoc{}, background...), docs...))); err != nil {
				t.Fatal(err)
			}
			assertSameIndex(t, fmt.Sprintf("seed %d, steps %v", seed, history), inc, fresh)
		}
	}
}

// A metadata-only change writes that field and nothing of the content.
func TestMetaOnlyUpdateLeavesContentAlone(t *testing.T) {
	idx := newPropIndex(t)
	d := propDoc{id: "d", content: "alpha beta gamma", lang: "en", meta: map[string]string{"title": "zenith", "url": "example"}}
	if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
		t.Fatal(err)
	}
	before := dumpBuckets(t, idx)
	d.meta["title"] = "aurora"
	delete(d.meta, "url")
	d.meta["license"] = "mit"
	if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
		t.Fatal(err)
	}
	after := dumpBuckets(t, idx)
	for k, v := range before {
		content := strings.HasPrefix(k, "fts|") || strings.HasPrefix(k, "ftsp|") || strings.HasPrefix(k, "ftsrev|ftsrev|") || strings.HasPrefix(k, "ftsrev|ftsmeta|")
		if content && after[k] != v {
			t.Errorf("content key %q changed on a metadata-only update", k)
		}
	}
	titleOnly := map[string]float64{"meta.title": 1}
	for term, want := range map[string]int{"aurora": 1, "zenith": 0} {
		hits, err := idx.SearchBM25F("c", map[string]int{term: 1}, 10, titleOnly, DefaultBM25)
		if err != nil || len(hits) != want {
			t.Errorf("meta.title %q: %d hits (%v), want %d", term, len(hits), err, want)
		}
	}
	for k := range after {
		if strings.Contains(k, "|meta.url|") {
			t.Errorf("a removed field kept %q", k)
		}
	}
	if _, ok := after["ftsfstat|ftsfstat|c|meta.url"]; ok {
		t.Error("a field no document has kept its statistics")
	}
}

// GO-048: a document whose content is emptied loses its content postings,
// positions and BM25 length, and keeps its meta fields; writing content back
// restores them. Each step leaves the index as a fresh build would.
func TestEmptiedContentLeavesOnlyMetaFields(t *testing.T) {
	bg := propDoc{id: "bg", content: "river stone", lang: "en", meta: map[string]string{}}
	// Alone, the document takes the collection's statistics with its content.
	for name, others := range map[string][]propDoc{"alone": nil, "with another document": {bg}} {
		t.Run(name, func(t *testing.T) {
			idx := newPropIndex(t)
			d := propDoc{id: "d", content: "alpha beta gamma", lang: "en", meta: map[string]string{"title": "zenith"}}
			if err := idx.IndexDocs("c", bulkOf(append(append([]propDoc{}, others...), d))); err != nil {
				t.Fatal(err)
			}
			for _, content := range []string{"", "", "aurora alpha"} {
				d.content = content
				assertContentStep(t, idx, others, d)
			}
		})
	}
}

// assertContentStep writes d (whose content was just set) over idx and
// checks the index equals a fresh build, finds alpha only while there is
// content, and always finds the title.
func assertContentStep(t *testing.T, idx *FTSIndex, others []propDoc, d propDoc) {
	t.Helper()
	content := d.content
	if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
		t.Fatal(err)
	}
	fresh := newPropIndex(t)
	if err := fresh.IndexDocs("c", bulkOf(append(append([]propDoc{}, others...), d))); err != nil {
		t.Fatal(err)
	}
	assertSameIndex(t, fmt.Sprintf("content %q", content), idx, fresh)

	wantContent := 0
	if content != "" {
		wantContent = 1
	}
	if hits, _ := idx.Search("c", "alpha", 10); len(hits) != wantContent {
		t.Errorf("content %q: alpha found in %d documents, want %d", content, len(hits), wantContent)
	}
	if hits, _ := idx.SearchBM25("c", "alpha", 10, DefaultBM25); len(hits) != wantContent {
		t.Errorf("content %q: BM25 alpha found in %d documents, want %d", content, len(hits), wantContent)
	}
	hits, err := idx.SearchBM25F("c", map[string]int{"zenith": 1}, 10, map[string]float64{"meta.title": 1}, DefaultBM25)
	if err != nil || len(hits) != 1 {
		t.Errorf("content %q: the title is no longer found: %v %v", content, hits, err)
	}
}

// Rebuild re-indexes even when the signature matches: an fts-reindex after
// a tokeniser change must reach documents that did not change.
func TestRebuildIgnoresSignature(t *testing.T) {
	idx := newPropIndex(t)
	d := BulkDoc{DocID: "d", Content: "running runners", Lang: "en", Fields: map[string]string{"content": "running runners"}}
	if err := idx.IndexDocs("c", []BulkDoc{d}); err != nil {
		t.Fatal(err)
	}
	idx.SetStemmer(nil)
	idx.SetLangRegistry(nil)
	if err := idx.IndexDocs("c", []BulkDoc{d}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search("c", "running", 10); len(hits) != 0 {
		t.Fatal("an unchanged document was re-indexed without Rebuild")
	}
	d.Rebuild = true
	if err := idx.IndexDocs("c", []BulkDoc{d}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search("c", "running", 10); len(hits) != 1 {
		t.Fatal("Rebuild did not re-index the document")
	}
}

// A document indexed before signatures existed, or whose signature or field
// reverse index cannot be read, is re-indexed from scratch.
func TestIncrementalFallsBackToFullIndex(t *testing.T) {
	d := propDoc{id: "d", content: "alpha beta", lang: "en", meta: map[string]string{"title": "zenith", "tags": "river"}}
	for name, damage := range map[string]func(tx *bolt.Tx) error{
		"no signature": func(tx *bolt.Tx) error { return tx.Bucket(bucketFTSRev).Delete(ftsSigKey("c", "d")) },
		"bad signature": func(tx *bolt.Tx) error {
			return tx.Bucket(bucketFTSRev).Put(ftsSigKey("c", "d"), []byte("{"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			idx := newPropIndex(t)
			if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
				t.Fatal(err)
			}
			if err := idx.db.Update(damage); err != nil {
				t.Fatal(err)
			}
			next := d
			next.meta = map[string]string{"title": "aurora", "tags": "river"}
			if err := idx.IndexDocs("c", []BulkDoc{next.bulk()}); err != nil {
				t.Fatal(err)
			}
			fresh := newPropIndex(t)
			if err := fresh.IndexDocs("c", []BulkDoc{next.bulk()}); err != nil {
				t.Fatal(err)
			}
			assertSameIndex(t, name, idx, fresh)
		})
	}
}

// An unreadable field reverse index cannot say which postings a field had,
// so the update falls back to a full re-index, which is what it did before
// #367: the new text is indexed, and the old postings the reverse index no
// longer lists cannot be found.
func TestIncrementalUnreadableFieldRev(t *testing.T) {
	idx := newPropIndex(t)
	d := propDoc{id: "d", content: "alpha beta", lang: "en", meta: map[string]string{"title": "zenith"}}
	if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
		t.Fatal(err)
	}
	if err := idx.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketFTSFRev).Put(ftsfRevKey("c", "d"), []byte("not json"))
	}); err != nil {
		t.Fatal(err)
	}
	d.meta["title"] = "aurora"
	if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.SearchBM25F("c", map[string]int{"aurora": 1}, 10, map[string]float64{"meta.title": 1}, DefaultBM25)
	if err != nil || len(hits) != 1 {
		t.Fatalf("the new title is not indexed: %v %v", hits, err)
	}
	if hits, _ := idx.Search("c", "alpha", 10); len(hits) != 1 {
		t.Fatal("the full re-index lost the content")
	}
}

// Removing a document removes its signature, so adding it back indexes it.
func TestRemoveDropsSignature(t *testing.T) {
	idx := newPropIndex(t)
	d := propDoc{id: "d", content: "alpha", lang: "en", meta: map[string]string{"title": "zenith"}}
	if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Remove("c", "d"); err != nil {
		t.Fatal(err)
	}
	for k := range dumpBuckets(t, idx) {
		if strings.Contains(k, "|d") {
			t.Fatalf("a removed document left %q", k)
		}
	}
	if err := idx.IndexDocs("c", []BulkDoc{d.bulk()}); err != nil {
		t.Fatal(err)
	}
	if hits, _ := idx.Search("c", "alpha", 10); len(hits) != 1 {
		t.Fatal("a document added back after removal is not indexed")
	}
}
