package main

import (
	"context"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/cache"
	"mddb/internal/schema"
	"mddb/internal/storage"
	proto "mddb/proto"
)

// metaValues is the batch form of one metadata key with one value.
func metaValues(key, value string) map[string]*proto.MetaValues {
	return map[string]*proto.MetaValues{key: {Values: []string{value}}}
}

// An update through the extreme batch path replaces the document's
// metadata index entries, writes one key-index entry whatever the spelling
// (DOC-017), and trims history to the collection's MaxRevisions.
func TestFinalBatchProcessor_UpdateReindexesAndTrims(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()
	if err := s.CollectionManager.Set("c", &CollectionConfig{MaxRevisions: 1}); err != nil {
		t.Fatal(err)
	}
	fbp := NewFinalBatchProcessor(s, 2)
	ctx := context.Background()

	for i, spelling := range []string{"README.md", "readme.md", "ReadMe.md"} {
		resp, err := fbp.ProcessBatch(ctx, "c", []*proto.BatchDocument{
			makeBatchDoc(spelling, "en", "v"+itoa(i), metaValues("tag", "t"+itoa(i)), true),
		})
		if err != nil || resp.Failed != 0 {
			t.Fatalf("write %s: %v %+v", spelling, err, resp)
		}
	}

	id := genID("c", "readme.md", "en")
	var byKey, metaEntries, revisions int
	if err := s.DBViewC("c", func(tx *bolt.Tx) error {
		count := func(bucket string, prefix []byte) int {
			n := 0
			c := tx.Bucket([]byte(bucket)).Cursor()
			for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
				n++
			}
			return n
		}
		byKey = count("bykey", storage.ByKeyCollectionPrefix("c"))
		metaEntries = count("idxmeta", []byte("meta|c|tag|"))
		revisions = count("rev", storage.RevPrefix("c", id))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if byKey != 1 {
		t.Errorf("%d key-index entries for one document written in three spellings", byKey)
	}
	if metaEntries != 1 {
		t.Errorf("%d metadata entries for tag, want only the latest value", metaEntries)
	}
	if revisions != 1 {
		t.Errorf("%d revisions kept, MaxRevisions is 1", revisions)
	}
}

func TestFinalBatchProcessor_RefusesWhatItCannotStore(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	defer s.closeCollectionFiles()
	s.SchemaManager = schema.NewSchemaManager(s.DB)
	if err := s.SchemaManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := s.SchemaManager.Set("c", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	// The extreme-mode cache takes the stored document.
	s.UseExtreme = true
	s.LockFreeCache = cache.NewLockFreeCache(10, 60)

	resp, err := NewFinalBatchProcessor(s, 2).ProcessBatch(context.Background(), "c", []*proto.BatchDocument{
		makeBatchDoc("no-author", "en", "x", nil, false),
		// Longer than bbolt's largest key: the document cannot be stored.
		makeBatchDoc(strings.Repeat("k", bolt.MaxKeySize), "en", "x", metaValues("author", "ann"), false),
		makeBatchDoc("ok", "en", "x", metaValues("author", "ann"), false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Added != 1 || resp.Failed != 2 {
		t.Fatalf("added %d failed %d (%v), want 1 and 2", resp.Added, resp.Failed, resp.Errors)
	}
	if _, ok := s.LockFreeCache.Get(cache.BuildCacheKey("c", "ok", "en")); !ok {
		t.Error("the stored document is not in the extreme-mode cache")
	}
}
