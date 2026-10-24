package vector

import (
	"testing"

	bolt "go.etcd.io/bbolt"
)

// A collection without quantization stores plain records through the
// quantized entry points, and they read back as written.
func TestQuantizedPutsWithoutQuantizationStorePlainRecords(t *testing.T) {
	vs, _ := openStore(t, true)
	if err := vs.PutQuantized("c", "one", []float32{1, 2, 3}, "m", "h", QuantNone); err != nil {
		t.Fatal(err)
	}
	if err := vs.PutChunksQuantized("c", "two", []ChunkEmbedding{{ChunkIndex: 0, Vector: []float32{4, 5, 6}}}, "m", "h", ""); err != nil {
		t.Fatal(err)
	}
	if rec, err := vs.Get("c", "one"); err != nil || rec == nil || rec.Vector[2] != 3 {
		t.Errorf("Get(one) = %+v, %v", rec, err)
	}
	if rec, err := vs.Get("c", "two"); err != nil || rec == nil || rec.Vector[0] != 4 {
		t.Errorf("Get(two) = %+v, %v", rec, err)
	}
}

// Delete removes a pre-chunking (legacy) record as well as chunk records.
func TestDeleteRemovesALegacyRecord(t *testing.T) {
	vs, _ := openStore(t, true)
	if err := vs.Put("c", "old", []float32{1}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if err := vs.Delete("c", "old"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := vs.Get("c", "old"); rec != nil {
		t.Errorf("a deleted legacy record is still readable: %+v", rec)
	}
}

// A store whose bucket was never created answers reads and deletes with
// nothing rather than failing.
func TestAStoreWithoutItsBucketReadsNothing(t *testing.T) {
	vs, _ := openStore(t, false)
	if rec, err := vs.Get("c", "x"); rec != nil || err != nil {
		t.Errorf("Get = %+v, %v", rec, err)
	}
	if err := vs.Delete("c", "x"); err != nil {
		t.Errorf("Delete = %v", err)
	}
	vs.CleanStaleChunks("c", "x", 0, nil) // must not panic
}

// One record that cannot be decoded costs that record, not the collection.
func TestLoadCollectionSkipsAnUnreadableRecord(t *testing.T) {
	vs, db := openStore(t, true)
	if err := vs.Put("c", "good", []float32{1, 2}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("vectors")).Put(buildChunkKey("c", "bad", 0), []byte{1, 2, 3})
	}); err != nil {
		t.Fatal(err)
	}
	coll, err := vs.LoadCollection("c")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := coll["good"]; !ok || len(coll) != 1 {
		t.Errorf("LoadCollection = %v, want only the readable record", coll)
	}
}
