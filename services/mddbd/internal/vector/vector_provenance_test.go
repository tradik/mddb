package vector

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func provStore(t *testing.T) *VectorStore {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "prov.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewVectorStore(db)
	if err := store.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestProvenanceRoundTrips(t *testing.T) {
	store := provStore(t)

	if err := store.NoteProvenance("docs", "nomic-embed-text", "search_document: "); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Provenance("docs")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("recorded provenance was not found")
	}
	if got.Model != "nomic-embed-text" || got.Variant != "search_document: " {
		t.Fatalf("stored %+v, want the model and variant that were recorded", got)
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt was not set, so there is no way to tell when the embedding changed")
	}
}

func TestProvenanceOfAnUnknownCollectionIsNotAnError(t *testing.T) {
	store := provStore(t)

	// Nothing recorded at all: the bucket does not exist yet.
	if _, found, err := store.Provenance("docs"); err != nil || found {
		t.Fatalf("got found=%v err=%v, want a clean miss", found, err)
	}

	// Bucket exists, this key does not.
	if err := store.NoteProvenance("other", "m", ""); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Provenance("docs"); err != nil || found {
		t.Fatalf("got found=%v err=%v, want a clean miss", found, err)
	}
}

// An unchanged provenance must not cost a write on every document. Proven by
// corrupting the stored record behind the cache: if the second call wrote, it
// would repair it.
func TestRepeatedProvenanceDoesNotWrite(t *testing.T) {
	store := provStore(t)
	if err := store.NoteProvenance("docs", "m", "v"); err != nil {
		t.Fatal(err)
	}

	sentinel, _ := json.Marshal(Provenance{Model: "untouched"})
	if err := store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(provenanceBucket).Put([]byte("docs"), sentinel)
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.NoteProvenance("docs", "m", "v"); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.Provenance("docs")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "untouched" {
		t.Error("recording the same provenance again opened a write transaction; it must be a cache hit")
	}
}

func TestChangedProvenanceIsWritten(t *testing.T) {
	store := provStore(t)
	if err := store.NoteProvenance("docs", "m", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.NoteProvenance("docs", "m", "search_document: "); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.Provenance("docs")
	if err != nil {
		t.Fatal(err)
	}
	if got.Variant != "search_document: " {
		t.Errorf("stored variant %q, want the new one", got.Variant)
	}
}

// The whole point of RAG-007: a collection written before provenance existed
// carries no record, and must still be reported when the variant changes.
func TestCollectionWithNoRecordIsStaleOnlyWhenTheVariantChanged(t *testing.T) {
	store := provStore(t)
	if err := store.Put("legacy", "d1", []float32{1, 2, 3}, "m", "hash"); err != nil {
		t.Fatal(err)
	}

	stale, err := store.StaleCollections("m", "search_document: ")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0] != "legacy" {
		t.Errorf("got %v, want the unrecorded collection reported as stale", stale)
	}

	stale, err = store.StaleCollections("m", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("got %v, want nothing: an unrecorded collection already holds the empty variant", stale)
	}
}

func TestStaleCollectionsFollowsTheModelAndTheVariant(t *testing.T) {
	store := provStore(t)
	for _, c := range []string{"current", "oldVariant", "otherModel"} {
		if err := store.Put(c, "d1", []float32{1, 2, 3}, "m", "hash"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.NoteProvenance("current", "m", "v2"); err != nil {
		t.Fatal(err)
	}
	if err := store.NoteProvenance("oldVariant", "m", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := store.NoteProvenance("otherModel", "other", "v2"); err != nil {
		t.Fatal(err)
	}

	stale, err := store.StaleCollections("m", "v2")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"oldVariant": true, "otherModel": true}
	if len(stale) != len(want) {
		t.Fatalf("got %v, want exactly %v", stale, want)
	}
	for _, c := range stale {
		if !want[c] {
			t.Errorf("%q was reported stale but its embedding matches", c)
		}
	}
}

// A collection that is only named in the provenance bucket, with no vectors
// left, has nothing to reindex and must not be reported.
func TestCollectionWithoutVectorsIsNotReported(t *testing.T) {
	store := provStore(t)
	if err := store.NoteProvenance("emptied", "m", "v1"); err != nil {
		t.Fatal(err)
	}
	stale, err := store.StaleCollections("m", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Errorf("got %v, want nothing: the collection holds no vectors", stale)
	}
}

func TestStaleCollectionsReportsUnreadableRecords(t *testing.T) {
	store := provStore(t)
	if err := store.NoteProvenance("docs", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(provenanceBucket).Put([]byte("docs"), []byte("{not json"))
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.StaleCollections("m", "v"); err == nil {
		t.Error("a corrupt provenance record was read as if it were valid")
	}
	if _, _, err := store.Provenance("docs"); err == nil {
		t.Error("a corrupt provenance record was read as if it were valid")
	}
}

// Chunk keys carry a "#N" suffix on the document, not an extra field, so the
// collection is still the second segment.
func TestCollectionsAreReadFromChunkKeysToo(t *testing.T) {
	store := provStore(t)
	if err := store.PutChunks("chunked", "d1", []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{1, 2}}}, "m", "hash"); err != nil {
		t.Fatal(err)
	}
	got := store.collectionsWithVectors()
	if len(got) != 1 || got[0] != "chunked" {
		t.Errorf("got %v, want the chunked collection", got)
	}
}

func TestNoteProvenanceReportsAWriteFailure(t *testing.T) {
	store := provStore(t)
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.NoteProvenance("docs", "m", "v"); err == nil {
		t.Error("recording provenance into a closed database was reported as success")
	}
}

// A store built without the constructor still has to work: the cache is an
// optimisation, not a precondition.
func TestNoteProvenanceBuildsItsCacheOnDemand(t *testing.T) {
	full := provStore(t)
	bare := &VectorStore{db: full.db, bucketName: []byte("vectors")}

	if err := bare.NoteProvenance("docs", "m", "v"); err != nil {
		t.Fatal(err)
	}
	if got, found, err := bare.Provenance("docs"); err != nil || !found || got.Model != "m" {
		t.Fatalf("got %+v found=%v err=%v, want the recorded provenance", got, found, err)
	}
}

func TestCollectionsWithoutAVectorsBucketAreEmpty(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "empty.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewVectorStore(db) // deliberately no EnsureBucket
	if got := store.collectionsWithVectors(); len(got) != 0 {
		t.Errorf("got %v, want nothing: there is no vectors bucket", got)
	}
}

func TestKeysThatAreNotVectorsAreIgnored(t *testing.T) {
	store := provStore(t)
	if err := store.Put("docs", "d1", []float32{1, 2}, "m", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(store.bucketName)
		if err := b.Put([]byte("meta|docs|thing"), []byte("x")); err != nil {
			return err
		}
		return b.Put([]byte("noseparators"), []byte("x"))
	}); err != nil {
		t.Fatal(err)
	}

	got := store.collectionsWithVectors()
	if len(got) != 1 || got[0] != "docs" {
		t.Errorf("got %v, want only the collection that holds vectors", got)
	}
}

func TestConcurrentWritersAgreeOnTheProvenance(t *testing.T) {
	store := provStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.NoteProvenance("docs", "m", "v"); err != nil {
				t.Errorf("NoteProvenance: %v", err)
			}
		}()
	}
	wg.Wait()

	got, found, err := store.Provenance("docs")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want the provenance to have been recorded once", found, err)
	}
	if got.Model != "m" || got.Variant != "v" {
		t.Errorf("stored %+v, want the value every writer agreed on", got)
	}
}
