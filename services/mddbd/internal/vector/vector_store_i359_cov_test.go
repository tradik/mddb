package vector

import (
	"path/filepath"
	"testing"

	"mddb/internal/boltx"

	bolt "go.etcd.io/bbolt"
)

// putRaw stores value under key in the vectors bucket, bypassing the store.
func putRaw(t *testing.T, db *bolt.DB, key string, value []byte) {
	t.Helper()
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("vectors")).Put([]byte(key), value)
	}); err != nil {
		t.Fatal(err)
	}
}

// A store given a router (#336, #359) reads and writes the file it names,
// not the one it was built on.
func TestVectorStoreSetRouterUsesTheRoutedFile(t *testing.T) {
	vs, _ := openStore(t, true)
	other, err := bolt.Open(filepath.Join(t.TempDir(), "other.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if err := NewVectorStore(other).EnsureBucket(); err != nil {
		t.Fatal(err)
	}

	vs.SetRouter(boltx.Single{DB: other})
	if err := vs.Put("c", "d", []float32{1, 2}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	if rec, err := NewVectorStore(other).Get("c", "d"); err != nil || rec == nil {
		t.Fatalf("the record did not land in the routed file: %+v, %v", rec, err)
	}
}

// Delete removes a record stored under the bare pre-chunking key.
func TestDeleteRemovesABareLegacyKey(t *testing.T) {
	vs, db := openStore(t, true)
	putRaw(t, db, "vec|c|old", MarshalEmbeddingRecord(&EmbeddingRecord{DocID: "old", Vector: []float32{1}, Dimensions: 1}))
	if err := vs.Delete("c", "old"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := vs.Get("c", "old"); rec != nil {
		t.Errorf("the legacy record survived Delete: %+v", rec)
	}
}

// Readers without the vectors bucket answer with nothing.
func TestReadersWithoutTheBucket(t *testing.T) {
	vs, _ := openStore(t, false)
	if recs, qvs, err := vs.LoadCollectionQuantized("c"); err != nil || len(recs) != 0 || len(qvs) != 0 {
		t.Errorf("LoadCollectionQuantized = %v, %v, %v", recs, qvs, err)
	}
	if got := vs.ChunkVectorsByHash("c", "d"); len(got) != 0 {
		t.Errorf("ChunkVectorsByHash = %v", got)
	}
}

// Records that do not decode, quantized or not, are skipped by the bulk
// loader and by GetVectors rather than failing the whole read.
func TestUndecodableRecordsAreSkipped(t *testing.T) {
	vs, db := openStore(t, true)
	putRaw(t, db, "vec|c|badq", []byte{2})
	putRaw(t, db, "vec|c|badl", []byte{1})
	good := marshalEmbeddingRecordQuantized(&EmbeddingRecord{DocID: "good", Vector: []float32{1, 2}}, QuantInt8)
	putRaw(t, db, "vec|c|good", good)

	recs, qvs, err := vs.LoadCollectionQuantized("c")
	if err != nil || len(recs) != 1 || len(qvs) != 1 || recs["good"] == nil {
		t.Errorf("LoadCollectionQuantized = %v, %v, %v; want only good", recs, qvs, err)
	}
	if got := vs.GetVectors("c", []string{"badq", "badl", "good"}); len(got) != 1 || got["good"] == nil {
		t.Errorf("GetVectors = %v; want only good", got)
	}
}

// Without a quantization type the quantized encoder writes a plain record.
func TestMarshalQuantizedWithoutQuantizationIsPlain(t *testing.T) {
	rec := &EmbeddingRecord{DocID: "d", Vector: []float32{1, 2}, Dimensions: 2}
	data := marshalEmbeddingRecordQuantized(rec, QuantNone)
	if isQuantizedRecord(data) {
		t.Fatal("a record without quantization was written quantized")
	}
	if got, err := UnmarshalEmbeddingRecord(data); err != nil || got.DocID != "d" {
		t.Errorf("UnmarshalEmbeddingRecord = %+v, %v", got, err)
	}
}

// Every way a quantized record can be damaged is an error, not a panic or
// a half-read record.
func TestUnmarshalQuantizedRejectsDamagedRecords(t *testing.T) {
	rec := &EmbeddingRecord{DocID: "doc", Model: "m", Vector: []float32{1, 2, 3}}
	valid := marshalEmbeddingRecordQuantized(rec, QuantInt8)
	if _, _, err := unmarshalEmbeddingRecordQuantized(valid); err != nil {
		t.Fatalf("the valid record does not decode: %v", err)
	}

	// The quantized vector starts after version, type, model length and
	// model, and its own length.
	qvStart := 2 + 4 + len(rec.Model) + 4
	unknownType := append([]byte(nil), valid...)
	unknownType[qvStart] = 0

	cases := map[string][]byte{
		"truncated header":       {2, 1},
		"vector too short":       {2, 1, 0, 0, 0, 0, 0, 0, 0, 0},
		"unknown vector type":    unknownType,
		"truncated after vector": valid[:len(valid)-6],
	}
	for name, data := range cases {
		if _, _, err := unmarshalEmbeddingRecordQuantized(data); err == nil {
			t.Errorf("%s: decoded without an error", name)
		}
	}
}
