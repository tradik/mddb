package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"mddb/internal/boltx"
	"mddb/internal/fts"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// #367: every write path reaches the full-text index through one decision —
// IndexDocs compares the document with what the index holds and writes only
// the difference. These tests drive the server's real write paths.

// ftsDump reads a collection's full-text buckets from the file that holds
// them, in either storage layout.
func ftsDump(t *testing.T, s *Server, collection string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := s.DBViewC(collection, func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			if !strings.HasPrefix(string(name), "fts") {
				return nil
			}
			return b.ForEach(func(k, v []byte) error {
				out[string(name)+"|"+string(k)] = string(v)
				return nil
			})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// changedKeys counts the keys two dumps disagree on.
func changedKeys(before, after map[string]string) int {
	n := 0
	for k, v := range after {
		if old, ok := before[k]; !ok || old != v {
			n++
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			n++
		}
	}
	return n
}

// ftsWriter writes one document version through one of the server's paths.
type ftsWriter struct {
	name  string
	write func(t *testing.T, s *Server, key, content string, meta map[string][]string)
}

func protoMetaOf(meta map[string][]string) map[string]*proto.MetaValues {
	out := make(map[string]*proto.MetaValues, len(meta))
	for k, v := range meta {
		out[k] = &proto.MetaValues{Values: v}
	}
	return out
}

// ftsAdd writes through addDocument: REST and gRPC Add, MCP, GraphQL, and
// the scheduled sync (INT-022).
func ftsAdd(t *testing.T, s *Server, key, content string, meta map[string][]string) {
	t.Helper()
	if _, _, err := s.addDocument("c", key, "en", meta, content, 0, true); err != nil {
		t.Fatal(err)
	}
}

var ftsWriters = []ftsWriter{
	{"add (REST, gRPC, MCP, GraphQL, sync)", ftsAdd},
	{"patch (REST, gRPC UpdateDocument, MCP, GraphQL)", func(t *testing.T, s *Server, key, content string, meta map[string][]string) {
		if _, err := s.updateDocument("c", key, "en", DocPatch{HasMeta: true, Meta: meta, Content: &content}); err != nil {
			if errors.Is(err, errDocNotFound) {
				if _, _, err = s.addDocument("c", key, "en", meta, content, 0, true); err == nil {
					return
				}
			}
			t.Fatal(err)
		}
	}},
	{"batch (REST, gRPC AddBatch)", func(t *testing.T, s *Server, key, content string, meta map[string][]string) {
		rec := doRequest(t, s.handleAddBatch, map[string]any{"collection": "c",
			"documents": []map[string]any{{"key": key, "lang": "en", "contentMd": content, "meta": meta}}})
		if rec.Code != http.StatusOK {
			t.Fatalf("batch: %d %s", rec.Code, rec.Body.String())
		}
	}},
	{"ingest upsert", func(t *testing.T, s *Server, key, content string, meta map[string][]string) {
		_, processed, err := s.processBatchWithDocs(context.Background(), "c", []*proto.BatchDocument{{Key: key, Lang: "en", ContentMd: content, Meta: protoMetaOf(meta)}})
		if err != nil {
			t.Fatal(err)
		}
		s.firePostBatchHooks("c", processed, postBatchOptions{})
	}},
	{"update batch (gRPC UpdateBatch, MCP)", func(t *testing.T, s *Server, key, content string, meta map[string][]string) {
		if len(meta) == 0 || content == "" {
			// An empty meta or content in a batch update means "leave it",
			// so the update cannot express this version: write it another way.
			ftsAdd(t, s, key, content, meta)
			return
		}
		resp, err := NewBatchUpdater(s, 2).ProcessBatchUpdate(context.Background(), "c", []*proto.UpdateDocument{{Key: key, Lang: "en", ContentMd: content, Meta: protoMetaOf(meta)}})
		if err != nil {
			t.Fatal(err)
		}
		if resp.NotFound > 0 {
			ftsAdd(t, s, key, content, meta)
		}
	}},
}

func titleHits(t *testing.T, s *Server, field, term string) int {
	t.Helper()
	hits, err := s.FTSIndex.SearchBM25F("c", s.FTSIndex.TokenizeQuery("c", term), 10, map[string]float64{field: 1}, fts.DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	return len(hits)
}

// Every write path: a metadata-only change rewrites that field and none of
// the content's postings, the new value is found and the old one is not,
// and resending the same document writes no full-text key at all.
func TestEveryWritePathIndexesOnlyWhatChanged(t *testing.T) {
	for _, w := range ftsWriters {
		t.Run(w.name, func(t *testing.T) {
			s, cleanup := newHandlerTestServer(t)
			defer cleanup()
			content := "the quokka smiles on rottnest island"
			w.write(t, s, "k", content, map[string][]string{"title": {"aurora"}, "url": {"example"}})
			before := ftsDump(t, s, "c")

			w.write(t, s, "k", content, map[string][]string{"title": {"zenith"}, "url": {"example"}})
			after := ftsDump(t, s, "c")
			for k, v := range before {
				isContent := strings.HasPrefix(k, "fts|") || strings.HasPrefix(k, "ftsp|") || strings.HasPrefix(k, "ftsrev|ftsrev|") || strings.HasPrefix(k, "ftsrev|ftsmeta|")
				if isContent && after[k] != v {
					t.Errorf("content key %q was rewritten by a metadata-only update", k)
				}
			}
			if titleHits(t, s, "meta.title", "zenith") != 1 || titleHits(t, s, "meta.title", "aurora") != 0 {
				t.Error("the title change did not reach the index")
			}
			if n := changedKeys(before, after); n == 0 || n > 8 {
				t.Errorf("a one-field change changed %d keys", n)
			}

			w.write(t, s, "k", content, map[string][]string{"title": {"zenith"}, "url": {"example"}})
			if n := changedKeys(after, ftsDump(t, s, "c")); n != 0 {
				t.Errorf("an identical resend changed %d keys", n)
			}

			newContent := "the wombat digs a burrow"
			w.write(t, s, "k", newContent, map[string][]string{"title": {"zenith"}, "url": {"example"}})
			if ftsHits(t, s, "c", "wombat") != 1 || ftsHits(t, s, "c", "quokka") != 0 {
				t.Error("a content change did not re-index the content")
			}
			res, err := s.searchFTS(FTSSearchRequest{Collection: "c", Query: `"wombat digs"`, Algorithm: "bm25"}, time.Now())
			if err != nil || res.Total != 1 {
				t.Errorf("phrase search after a content change: %+v %v", res, err)
			}
		})
	}
}

// The property, through the server: random updates by random write paths,
// after each of which the incrementally maintained index equals the one an
// fts-reindex rebuilds from the stored documents — and at the end equals a
// fresh server's index built from the final documents alone.
func TestServerIncrementalIndexEqualsRebuild(t *testing.T) {
	type version struct {
		content string
		meta    map[string][]string
	}
	words := strings.Fields("alpha beta gamma river stone zenith aurora quokka wombat running the of")
	text := func(r *rand.Rand, n int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = words[r.Intn(len(words))]
		}
		return strings.Join(out, " ")
	}
	seeds := 6
	if testing.Short() {
		seeds = 2
	}
	for seed := int64(1); seed <= int64(seeds); seed++ {
		r := rand.New(rand.NewSource(seed)) // #nosec G404 -- a seeded, reproducible test sequence, not a secret
		s, cleanup := newHandlerTestServer(t)
		docs := map[string]version{}
		for _, key := range []string{"a", "b"} {
			docs[key] = version{text(r, 6), map[string][]string{"title": {text(r, 2)}}}
			ftsAdd(t, s, key, docs[key].content, docs[key].meta)
		}
		var history []string
		for step := 0; step < 15; step++ {
			key := []string{"a", "b"}[r.Intn(2)]
			v := docs[key]
			v.meta = maps.Clone(v.meta)
			switch op := r.Intn(6); op {
			case 0:
				v.content = text(r, 1+r.Intn(8))
			case 5: // GO-048: content emptied; the meta fields stay indexed
				v.content = ""
			case 1, 2:
				v.meta[[]string{"title", "tags", "license"}[r.Intn(3)]] = []string{text(r, 1+r.Intn(3))}
			case 3:
				for k := range v.meta {
					delete(v.meta, k)
					break
				}
			}
			docs[key] = v
			w := ftsWriters[r.Intn(len(ftsWriters))]
			history = append(history, key+":"+w.name)
			w.write(t, s, key, v.content, v.meta)

			incremental := ftsDump(t, s, "c")
			if _, err := s.reindexCollectionFTS("c"); err != nil {
				t.Fatal(err)
			}
			if rebuilt := ftsDump(t, s, "c"); !reflect.DeepEqual(incremental, rebuilt) {
				t.Fatalf("seed %d after %v: incremental and rebuilt index differ in %d keys", seed, history, changedKeys(incremental, rebuilt))
			}
		}

		final := ftsDump(t, s, "c")
		fresh, freshCleanup := newHandlerTestServer(t)
		keys := []string{"a", "b"}
		sort.Strings(keys)
		for _, key := range keys {
			ftsAdd(t, fresh, key, docs[key].content, docs[key].meta)
		}
		if got := ftsDump(t, fresh, "c"); !reflect.DeepEqual(final, got) {
			t.Fatalf("seed %d: the index differs from a fresh build in %d keys", seed, changedKeys(final, got))
		}
		for _, q := range []string{"alpha", "zenith aurora", "running"} {
			for _, algo := range []string{"bm25", "bm25f", "tfidf"} {
				a, err1 := s.searchFTS(FTSSearchRequest{Collection: "c", Query: q, Algorithm: algo}, time.Now())
				b, err2 := fresh.searchFTS(FTSSearchRequest{Collection: "c", Query: q, Algorithm: algo}, time.Now())
				if err1 != nil || err2 != nil || rankedIDs(a) != rankedIDs(b) {
					t.Fatalf("seed %d %s %q: %s vs %s", seed, algo, q, rankedIDs(a), rankedIDs(b))
				}
			}
		}
		freshCleanup()
		cleanup()
	}
}

// excludeMetaKeys keeps a meta key out of FTS: no postings for it, so
// changing it writes nothing, while filterMeta and facets — which read the
// metadata index — still work. It reaches existing documents through an
// fts-reindex, and new ones from their first write.
func TestExcludeMetaKeys(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "old", "en", "ports tree", map[string][]string{"url": {"freshports"}, "title": {"zenith"}})

	rec := doRequest(t, s.handleCollectionConfigSet, map[string]any{"collection": "c", "fts": map[string]any{"excludeMetaKeys": []string{"url", "license"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("config: %d %s", rec.Code, rec.Body.String())
	}
	// Written before the list: still indexed until a reindex.
	if titleHits(t, s, "meta.url", "freshports") != 1 {
		t.Fatal("the list changed postings that were already written")
	}
	if res, err := s.reindexCollectionFTS("c"); err != nil || res.Reindexed != 1 {
		t.Fatalf("reindex: %+v %v", res, err)
	}
	if titleHits(t, s, "meta.url", "freshports") != 0 || titleHits(t, s, "meta.title", "zenith") != 1 {
		t.Fatal("the reindex did not apply the exclusion list")
	}

	addTestDoc(t, s, "c", "new", "en", "ports tree", map[string][]string{"url": {"freshports"}, "license": {"MIT"}})
	for k := range ftsDump(t, s, "c") {
		if strings.Contains(k, "|meta.url|") || strings.Contains(k, "|meta.license|") {
			t.Errorf("an excluded key has a posting: %q", k)
		}
	}
	before := ftsDump(t, s, "c")
	addTestDoc(t, s, "c", "new", "en", "ports tree", map[string][]string{"url": {"daemonports"}, "license": {"BSD"}})
	if n := changedKeys(before, ftsDump(t, s, "c")); n != 0 {
		t.Errorf("changing excluded keys wrote %d full-text keys", n)
	}

	res, err := s.searchFTS(FTSSearchRequest{Collection: "c", Query: "ports", Algorithm: "bm25",
		FilterMeta: map[string][]string{"license": {"BSD"}}, FacetBy: []string{"url"}}, time.Now())
	if err != nil || res.Total != 1 || len(res.Facets["url"]) != 1 {
		t.Fatalf("filterMeta and facets on an excluded key: %+v %v", res, err)
	}

	got, ok := s.CollectionManager.Get("c")
	if !ok || got.FTS == nil || !reflect.DeepEqual(got.FTS.ExcludeMetaKeys, []string{"url", "license"}) || !s.ftsEnabledFor("c") {
		t.Errorf("stored setting: %+v", got.FTS)
	}
	if s.ftsExcludedMetaKeys("none") != nil || (&Server{}).ftsExcludedMetaKeys("c") != nil {
		t.Error("a collection without a list excludes something")
	}
}

// The list travels gRPC and MCP like the rest of the fts block.
func TestExcludeMetaKeysTravelsEveryTransport(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	if _, err := gs.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{
		Collection: "g", Fts: &proto.CollectionFTSProto{ExcludeMetaKeys: []string{"url"}},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := gs.GetCollectionConfig(context.Background(), &proto.GetCollectionConfigRequest{Collection: "g"})
	if !reflect.DeepEqual(got.GetConfig().GetFts().GetExcludeMetaKeys(), []string{"url"}) || !s.ftsEnabledFor("g") {
		t.Errorf("gRPC round trip: %v", got.GetConfig().GetFts())
	}

	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	set := func(fts map[string]interface{}) {
		t.Helper()
		if _, err := ts.toolSetCollectionConfig(context.Background(), map[string]interface{}{"collection": "m", "fts": fts}); err != nil {
			t.Fatal(err)
		}
	}
	set(map[string]interface{}{"exclude_meta_keys": []interface{}{"url", "license"}})
	if ex := s.ftsExcludedMetaKeys("m"); !ex["url"] || !ex["license"] || !s.ftsEnabledFor("m") {
		t.Errorf("MCP list: %v", ex)
	}
	set(map[string]interface{}{"exclude_meta_keys": []interface{}{}})
	if ex := s.ftsExcludedMetaKeys("m"); ex != nil {
		t.Errorf("an empty MCP list did not clear it: %v", ex)
	}
}

// UpdateBatch never re-indexed FTS; it does now, and drops cached searches.
func TestUpdateBatchReindexesFTS(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "k", "en", "quokka island", map[string][]string{"title": {"aurora"}})
	if ftsHits(t, s, "c", "quokka") != 1 {
		t.Fatal("setup")
	}
	resp, err := NewBatchUpdater(s, 2).ProcessBatchUpdate(context.Background(), "c", []*proto.UpdateDocument{
		{Key: "k", Lang: "en", ContentMd: "wombat burrow"},
		{Key: "missing", Lang: "en", ContentMd: "nothing"},
	})
	if err != nil || resp.Updated != 1 || resp.NotFound != 1 {
		t.Fatalf("update: %+v %v", resp, err)
	}
	if ftsHits(t, s, "c", "wombat") != 1 || ftsHits(t, s, "c", "quokka") != 0 {
		t.Error("UpdateBatch left the full-text index describing the old content")
	}
	if titleHits(t, s, "meta.title", "aurora") != 1 {
		t.Error("UpdateBatch lost the unchanged metadata's postings")
	}
}

// rankedIDs is a search's documents and scores, ties in document order.
func rankedIDs(r *FTSSearchResponse) string {
	out := make([]string, len(r.Results))
	for i, h := range r.Results {
		out[i] = fmt.Sprintf("%s=%.12g", h.Document.ID, h.Score)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// writeRefusingRouter reads like the server and refuses every write.
type writeRefusingRouter struct{ boltx.Router }

func (writeRefusingRouter) ForWrite(string) (*bolt.DB, error) {
	return nil, errors.New("write refused")
}

// A document the index refuses is counted, and the reindex is partial.
func TestReindexFTSReportsFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "a", "en", "wombat burrow", nil)
	s.FTSIndex.SetRouter(writeRefusingRouter{s.router()})
	res, err := s.reindexCollectionFTS("c")
	if err != nil || res.Status != "partial" || res.Failed != 1 || res.Reindexed != 0 {
		t.Errorf("reindex: %+v %v", res, err)
	}
}
