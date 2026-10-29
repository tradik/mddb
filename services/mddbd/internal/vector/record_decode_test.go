package vector

import (
	"encoding/binary"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// v2RecordFor builds a v2 record around the given quantized-vector bytes, so a
// test can damage one field and keep the rest well formed.
func v2RecordFor(qvData []byte, tail []byte) []byte {
	buf := []byte{2, 1}
	buf = binary.LittleEndian.AppendUint32(buf, 1)
	buf = append(buf, 'm')
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(qvData))) // #nosec G115 -- test data is tiny
	buf = append(buf, qvData...)
	return append(buf, tail...)
}

// A damaged v2 record is an error naming what was wrong, never a panic or a
// half-filled record.
func TestUnmarshalQuantizedRecordRejectsDamage(t *testing.T) {
	qv := QuantizeFloat32([]float32{0.1, 0.9}, QuantInt8)
	wrongDims := *qv
	wrongDims.Dims = 99

	good := marshalEmbeddingRecordQuantized(&EmbeddingRecord{Model: "m", Vector: []float32{0.1, 0.9}, Dimensions: 2, ContentHash: "h", DocID: "d"}, QuantInt8)
	untilCreatedAt := 2 + 4 + 1 + 4 + len(MarshalQuantizedVector(qv))

	cases := map[string][]byte{
		"vector too short":         v2RecordFor([]byte{1}, nil),
		"dims do not match data":   v2RecordFor(MarshalQuantizedVector(&wrongDims), nil),
		"record ends after vector": good[:untilCreatedAt],
	}
	for name, data := range cases {
		if rec, qv, err := unmarshalEmbeddingRecordQuantized(data); err == nil {
			t.Errorf("%s: decoded %+v %+v", name, rec, qv)
		}
	}
}

// A chunk hash whose length runs past the record is dropped; the vector,
// which decoded, is kept.
func TestUnmarshalQuantizedRecordDropsABrokenChunkHash(t *testing.T) {
	data := marshalEmbeddingRecordQuantized(&EmbeddingRecord{Model: "m", Vector: []float32{0.1, 0.9}, Dimensions: 2, DocID: "d"}, QuantInt4)
	binary.LittleEndian.PutUint32(data[len(data)-4:], 100)
	rec, _, err := unmarshalEmbeddingRecordQuantized(data)
	if err != nil || rec.ChunkHash != "" || len(rec.Vector) != 2 {
		t.Fatalf("rec %+v, err %v", rec, err)
	}
}

// Without a quantization type the record is written in the float32 format.
func TestMarshalQuantizedWithoutATypeWritesFloat32(t *testing.T) {
	data := marshalEmbeddingRecordQuantized(&EmbeddingRecord{Model: "model", Vector: []float32{1}, Dimensions: 1}, QuantNone)
	if isQuantizedRecord(data) {
		t.Fatal("a record without quantization was written as quantized")
	}
	if rec, err := UnmarshalEmbeddingRecord(data); err != nil || rec.Vector[0] != 1 {
		t.Fatalf("rec %+v, err %v", rec, err)
	}
}

// Readers skip records they cannot decode, in either format, and answer
// nothing for a store without its bucket.
func TestReadersSkipUndecodableRecords(t *testing.T) {
	vs, db := openStore(t, true)
	if err := vs.PutQuantized("c", "good", []float32{0.2, 0.8}, "m", "h", QuantInt8); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("vectors"))
		if err := b.Put([]byte("vec|c|badv2"), v2RecordFor([]byte{1}, nil)); err != nil {
			return err
		}
		return b.Put([]byte("vec|c|badv1"), []byte{5, 0, 0, 0, 'x'})
	}); err != nil {
		t.Fatal(err)
	}
	records, quantized, err := vs.LoadCollectionQuantized("c")
	if err != nil || len(records) != 1 || len(quantized) != 1 || records["good"] == nil {
		t.Fatalf("LoadCollectionQuantized = %v %v %v", records, quantized, err)
	}
	if got := vs.GetVectors("c", []string{"good", "badv2", "badv1"}); len(got) != 1 || got["good"] == nil {
		t.Fatalf("GetVectors = %v", got)
	}

	empty, _ := openStore(t, false)
	if records, _, err := empty.LoadCollectionQuantized("c"); err != nil || len(records) != 0 {
		t.Errorf("LoadCollectionQuantized without bucket = %v %v", records, err)
	}
	if got := empty.ChunkVectorsByHash("c", "d"); len(got) != 0 {
		t.Errorf("ChunkVectorsByHash without bucket = %v", got)
	}
}
