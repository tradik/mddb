package vector

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"mddb/internal/binlog"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ChunkEmbedding holds a single chunk's embedding vector.
type ChunkEmbedding struct {
	ChunkIndex int
	Vector     []float32
	// ChunkHash identifies this chunk's text (RAG-003). Stored so a reindex
	// can reuse the vector of a chunk whose content did not change — editing
	// one paragraph of a fifty-chunk document used to re-embed all fifty,
	// because the document hash changed.
	ChunkHash string
}

// EmbeddingRecord stores a document's embedding vector alongside metadata.
type EmbeddingRecord struct {
	DocID       string    `json:"docId"`
	Vector      []float32 `json:"vector"`
	Model       string    `json:"model"`
	Dimensions  int       `json:"dimensions"`
	CreatedAt   int64     `json:"createdAt"`
	ContentHash string    `json:"contentHash"`
	// ChunkHash is this chunk's own content hash (RAG-003), as opposed to
	// ContentHash which covers the whole document. Empty on records written
	// before v2.12.0, which simply means no per-chunk reuse for them.
	ChunkHash string `json:"chunkHash,omitempty"`
}

// VectorStore handles persistence of embedding vectors in BoltDB.
type VectorStore struct {
	db         *bolt.DB
	bucketName []byte
	binlog     *binlog.Binlog
}

// NewVectorStore creates a new vector store backed by BoltDB.
func NewVectorStore(db *bolt.DB) *VectorStore {
	return &VectorStore{
		db:         db,
		bucketName: []byte("vectors"),
	}
}

// SetBinlog sets the binlog for replication logging.
func (vs *VectorStore) SetBinlog(bl *binlog.Binlog) {
	vs.binlog = bl
}

// EnsureBucket creates the vectors bucket if it doesn't exist.
func (vs *VectorStore) EnsureBucket() error {
	return vs.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(vs.bucketName)
		return err
	})
}

// PutQuantized stores a single embedding record with optional quantization.
func (vs *VectorStore) PutQuantized(collection, docID string, vector []float32, model string, contentHash string, qt QuantizationType) error {
	if qt == QuantNone || qt == "" {
		return vs.Put(collection, docID, vector, model, contentHash)
	}
	key := buildVecKey(collection, docID)
	rec := &EmbeddingRecord{
		DocID:       docID,
		Vector:      vector,
		Model:       model,
		Dimensions:  len(vector),
		CreatedAt:   time.Now().Unix(),
		ContentHash: contentHash,
	}
	data := marshalEmbeddingRecordQuantized(rec, qt)

	err := vs.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return fmt.Errorf("vectors bucket not found")
		}
		return b.Put(key, data)
	})
	if err == nil && vs.binlog != nil {
		_ = vs.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "vectors", Key: bytes.Clone(key), Value: bytes.Clone(data)})
	}
	return err
}

// PutChunksQuantized stores multiple chunk embeddings with optional quantization.
func (vs *VectorStore) PutChunksQuantized(collection, docID string, chunks []ChunkEmbedding, model string, contentHash string, qt QuantizationType) error {
	if qt == QuantNone || qt == "" {
		return vs.PutChunks(collection, docID, chunks, model, contentHash)
	}
	now := time.Now().Unix()

	return vs.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return fmt.Errorf("vectors bucket not found")
		}

		for _, chunk := range chunks {
			chunkKey := buildChunkKey(collection, docID, chunk.ChunkIndex)
			rec := &EmbeddingRecord{
				DocID:       docID,
				Vector:      chunk.Vector,
				Model:       model,
				Dimensions:  len(chunk.Vector),
				CreatedAt:   now,
				ContentHash: contentHash,
				ChunkHash:   chunk.ChunkHash,
			}
			data := marshalEmbeddingRecordQuantized(rec, qt)

			if err := b.Put(chunkKey, data); err != nil {
				return err
			}

			if vs.binlog != nil {
				_ = vs.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "vectors", Key: bytes.Clone(chunkKey), Value: bytes.Clone(data)})
			}
		}
		return nil
	})
}

// Put stores a single embedding record for a document (backward-compatible).
func (vs *VectorStore) Put(collection, docID string, vector []float32, model string, contentHash string) error {
	key := buildVecKey(collection, docID)
	data := MarshalEmbeddingRecord(&EmbeddingRecord{
		DocID:       docID,
		Vector:      vector,
		Model:       model,
		Dimensions:  len(vector),
		CreatedAt:   time.Now().Unix(),
		ContentHash: contentHash,
	})

	err := vs.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return fmt.Errorf("vectors bucket not found")
		}
		return b.Put(key, data)
	})
	if err == nil && vs.binlog != nil {
		_ = vs.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "vectors", Key: bytes.Clone(key), Value: bytes.Clone(data)})
	}
	return err
}

// PutChunks stores multiple chunk embeddings for a document.
// Keys: vec|collection|docID#0, vec|collection|docID#1, etc.
func (vs *VectorStore) PutChunks(collection, docID string, chunks []ChunkEmbedding, model string, contentHash string) error {
	now := time.Now().Unix()

	return vs.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return fmt.Errorf("vectors bucket not found")
		}

		for _, chunk := range chunks {
			chunkKey := buildChunkKey(collection, docID, chunk.ChunkIndex)
			data := MarshalEmbeddingRecord(&EmbeddingRecord{
				DocID:       docID,
				Vector:      chunk.Vector,
				Model:       model,
				Dimensions:  len(chunk.Vector),
				CreatedAt:   now,
				ContentHash: contentHash,
				ChunkHash:   chunk.ChunkHash,
			})

			if err := b.Put(chunkKey, data); err != nil {
				return err
			}

			if vs.binlog != nil {
				_ = vs.binlog.Append(&binlog.BinlogEntry{Type: binlog.BinlogPut, BucketName: "vectors", Key: bytes.Clone(chunkKey), Value: bytes.Clone(data)})
			}
		}

		return nil
	})
}

// CleanStaleChunks removes chunk keys beyond the current chunk count from BoltDB and the in-memory index.
func (vs *VectorStore) CleanStaleChunks(collection, docID string, currentChunkCount int, index *VectorIndex) {
	prefix := []byte("vec|" + collection + "|" + docID + "#")

	var bo binlog.BinlogOps
	_ = vs.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, _ = c.Next() {
			suffix := string(k[len(prefix):])
			idx, err := strconv.Atoi(suffix)
			if err != nil {
				continue
			}
			if idx >= currentChunkCount {
				_ = b.Delete(k)
				bo.Delete("vectors", k)
				if index != nil {
					chunkKey := fmt.Sprintf("%s#%d", docID, idx)
					index.Remove(collection, chunkKey)
				}
			}
		}
		return nil
	})
	bo.FlushTo(vs.binlog)

	// Also clean the old non-chunked key if chunks > 1
	if currentChunkCount > 1 {
		oldKey := buildVecKey(collection, docID)
		var bo2 binlog.BinlogOps
		_ = vs.db.Update(func(tx *bolt.Tx) error {
			b := tx.Bucket(vs.bucketName)
			if b == nil {
				return nil
			}
			if v := b.Get(oldKey); v != nil {
				_ = b.Delete(oldKey)
				bo2.Delete("vectors", oldKey)
				if index != nil {
					index.Remove(collection, docID)
				}
			}
			return nil
		})
		bo2.FlushTo(vs.binlog)
	}
}

// Get retrieves the first embedding record for a document (chunk 0 or legacy single record).
func (vs *VectorStore) Get(collection, docID string) (*EmbeddingRecord, error) {
	chunkKey := buildChunkKey(collection, docID, 0)
	var rec *EmbeddingRecord

	err := vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		v := b.Get(chunkKey)
		if v == nil {
			// Fallback to legacy non-chunked key
			v = b.Get(buildVecKey(collection, docID))
		}
		if v == nil {
			return nil
		}
		var err error
		if isQuantizedRecord(v) {
			rec, _, err = unmarshalEmbeddingRecordQuantized(v)
		} else {
			rec, err = UnmarshalEmbeddingRecord(v)
		}
		return err
	})

	return rec, err
}

// Delete removes all embedding records for a document (all chunks + legacy key).
func (vs *VectorStore) Delete(collection, docID string) error {
	legacyKey := buildVecKey(collection, docID)
	prefix := []byte("vec|" + collection + "|" + docID + "#")

	var bo binlog.BinlogOps
	err := vs.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		if b.Get(legacyKey) != nil {
			_ = b.Delete(legacyKey)
			bo.Delete("vectors", legacyKey)
		}

		c := b.Cursor()
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, _ = c.Next() {
			if err := b.Delete(k); err != nil {
				return err
			}
			bo.Delete("vectors", k)
		}
		return nil
	})
	if err == nil {
		bo.FlushTo(vs.binlog)
	}
	return err
}

// LoadCollection loads all embedding records for a collection.
// Returns records keyed by their full suffix (docID or docID#N).
// Supports both v1 (float32) and v2 (quantized) storage formats.
func (vs *VectorStore) LoadCollection(collection string) (map[string]*EmbeddingRecord, error) {
	prefix := []byte("vec|" + collection + "|")
	records := make(map[string]*EmbeddingRecord)

	err := vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, v = c.Next() {
			var rec *EmbeddingRecord
			var err error
			if isQuantizedRecord(v) {
				rec, _, err = unmarshalEmbeddingRecordQuantized(v)
			} else {
				rec, err = UnmarshalEmbeddingRecord(v)
			}
			if err != nil {
				continue
			}
			suffix := string(k[len(prefix):])
			records[suffix] = rec
		}
		return nil
	})

	return records, err
}

// LoadCollectionQuantized loads all embedding records and their quantized vectors.
// Returns both the EmbeddingRecords (with dequantized float32 vectors) and the raw QuantizedVectors.
func (vs *VectorStore) LoadCollectionQuantized(collection string) (map[string]*EmbeddingRecord, map[string]*QuantizedVector, error) {
	prefix := []byte("vec|" + collection + "|")
	records := make(map[string]*EmbeddingRecord)
	quantized := make(map[string]*QuantizedVector)

	err := vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix); k, v = c.Next() {
			suffix := string(k[len(prefix):])
			if isQuantizedRecord(v) {
				rec, qv, err := unmarshalEmbeddingRecordQuantized(v)
				if err != nil {
					continue
				}
				records[suffix] = rec
				quantized[suffix] = qv
			} else {
				rec, err := UnmarshalEmbeddingRecord(v)
				if err != nil {
					continue
				}
				records[suffix] = rec
			}
		}
		return nil
	})

	return records, quantized, err
}

// GetVectors fetches the stored vectors for specific index keys (docID or
// docID#N suffixes) in a single read transaction. Used by disk-only
// collections to rescore quantized candidates against the full-precision
// vectors kept on disk. Missing keys are simply absent from the result.
func (vs *VectorStore) GetVectors(collection string, ids []string) map[string][]float32 {
	out := make(map[string][]float32, len(ids))
	_ = vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		for _, id := range ids {
			v := b.Get([]byte("vec|" + collection + "|" + id))
			if v == nil {
				continue
			}
			var rec *EmbeddingRecord
			var err error
			if isQuantizedRecord(v) {
				rec, _, err = unmarshalEmbeddingRecordQuantized(v)
			} else {
				rec, err = UnmarshalEmbeddingRecord(v)
			}
			if err != nil || rec == nil {
				continue
			}
			out[id] = rec.Vector
		}
		return nil
	})
	return out
}

// CountByCollection counts embeddings per collection (counting unique docIDs, not chunks).
// Handles keys like vec|coll|docID and vec|coll|docID#N where docID may contain | characters.
func (vs *VectorStore) CountByCollection() (map[string]int, error) {
	counts := make(map[string]int)
	seen := make(map[string]bool)

	err := vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			ks := string(k)
			// Key format: vec|collection|docID or vec|collection|docID#N
			// Find first | after "vec|"
			if len(ks) < 5 || ks[:4] != "vec|" {
				continue
			}
			rest := ks[4:] // "collection|..."
			pipeIdx := strings.IndexByte(rest, '|')
			if pipeIdx < 0 {
				continue
			}
			coll := rest[:pipeIdx]
			docIDPart := rest[pipeIdx+1:]
			// Strip chunk suffix (#N) - keys are always built with buildChunkKey
			// which appends #chunkIndex. Only strip the very last #N where N >= 0.
			if hashIdx := strings.LastIndexByte(docIDPart, '#'); hashIdx >= 0 {
				suffix := docIDPart[hashIdx+1:]
				if n, err := strconv.Atoi(suffix); err == nil && n >= 0 {
					docIDPart = docIDPart[:hashIdx]
				}
			}
			dedupKey := coll + "\x00" + docIDPart
			if !seen[dedupKey] {
				seen[dedupKey] = true
				counts[coll]++
			}
		}
		return nil
	})
	return counts, err
}

// CountChunksByCollection counts total chunk embeddings per collection (including multi-chunk docs).
func (vs *VectorStore) CountChunksByCollection() (map[string]int, error) {
	counts := make(map[string]int)

	err := vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			ks := string(k)
			if len(ks) < 5 || ks[:4] != "vec|" {
				continue
			}
			rest := ks[4:]
			pipeIdx := strings.IndexByte(rest, '|')
			if pipeIdx < 0 {
				continue
			}
			counts[rest[:pipeIdx]]++
		}
		return nil
	})
	return counts, err
}

// buildVecKey builds key: vec|collection|docID (legacy, non-chunked)
func buildVecKey(collection, docID string) []byte {
	return []byte("vec|" + collection + "|" + docID)
}

// buildChunkKey builds key: vec|collection|docID#N
func buildChunkKey(collection, docID string, chunkIndex int) []byte {
	return []byte(fmt.Sprintf("vec|%s|%s#%d", collection, docID, chunkIndex))
}

// ContentHash computes SHA256 hash of content for staleness detection.
func ContentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:8]) // first 8 bytes = 16 hex chars
}

// marshalEmbeddingRecordQuantized serializes an EmbeddingRecord with quantization.
// Format v2: [1B version=2][1B quantType][4B model_len][model][4B dims][quantized_vector_data][8B created_at][4B hash_len][hash][4B docid_len][docid]
func marshalEmbeddingRecordQuantized(rec *EmbeddingRecord, qt QuantizationType) []byte {
	qv := QuantizeFloat32(rec.Vector, qt)
	if qv == nil {
		// fallback to float32 if quantization fails
		return MarshalEmbeddingRecord(rec)
	}

	qvData := MarshalQuantizedVector(qv)
	modelBytes := []byte(rec.Model)
	hashBytes := []byte(rec.ContentHash)
	docIDBytes := []byte(rec.DocID)

	chunkHashBytes := []byte(rec.ChunkHash)

	size := 1 + 1 + // version + quantType
		4 + len(modelBytes) + // model
		4 + len(qvData) + // quantized vector data (length-prefixed)
		8 + // created_at
		4 + len(hashBytes) + // content hash
		4 + len(docIDBytes) + // docID
		4 + len(chunkHashBytes) // chunk hash (v2.12.0+, RAG-003)

	buf := make([]byte, size)
	offset := 0

	// version
	buf[offset] = 2
	offset++

	// quantization type
	switch qt {
	case QuantInt8:
		buf[offset] = 1
	case QuantInt4:
		buf[offset] = 2
	default:
		buf[offset] = 0
	}
	offset++

	// model
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(modelBytes))) // #nosec G115
	offset += 4
	copy(buf[offset:], modelBytes)
	offset += len(modelBytes)

	// quantized vector data (length-prefixed)
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(qvData))) // #nosec G115
	offset += 4
	copy(buf[offset:], qvData)
	offset += len(qvData)

	// created_at
	binary.LittleEndian.PutUint64(buf[offset:], uint64(rec.CreatedAt)) // #nosec G115
	offset += 8

	// content hash
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(hashBytes))) // #nosec G115
	offset += 4
	copy(buf[offset:], hashBytes)
	offset += len(hashBytes)

	// docID
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(docIDBytes))) // #nosec G115
	offset += 4
	copy(buf[offset:], docIDBytes)
	offset += len(docIDBytes)

	// chunk hash (RAG-003), appended past every field the v2 format had —
	// same forward/backward compatibility argument as the float32 record.
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(chunkHashBytes))) // #nosec G115
	offset += 4
	copy(buf[offset:], chunkHashBytes)

	return buf
}

// unmarshalEmbeddingRecordQuantized deserializes a v2 quantized embedding record.
// Returns the EmbeddingRecord (with dequantized float32 vector) and the raw QuantizedVector.
func unmarshalEmbeddingRecordQuantized(data []byte) (*EmbeddingRecord, *QuantizedVector, error) {
	if len(data) < 14 {
		return nil, nil, fmt.Errorf("quantized embedding record too short")
	}

	offset := 2 // skip version + quantType bytes
	rec := &EmbeddingRecord{}

	// model
	modelLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if offset+modelLen > len(data) {
		return nil, nil, fmt.Errorf("invalid model length")
	}
	rec.Model = string(data[offset : offset+modelLen])
	offset += modelLen

	// quantized vector data
	qvDataLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if offset+qvDataLen > len(data) {
		return nil, nil, fmt.Errorf("invalid quantized vector data")
	}
	qv, err := UnmarshalQuantizedVector(data[offset : offset+qvDataLen])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal quantized vector: %w", err)
	}
	offset += qvDataLen

	rec.Dimensions = qv.Dims
	rec.Vector = DequantizeToFloat32(qv)
	if rec.Vector == nil {
		return nil, nil, fmt.Errorf("failed to dequantize vector: data length mismatch")
	}

	// created_at
	if offset+8 > len(data) {
		return nil, nil, fmt.Errorf("invalid created_at")
	}
	rec.CreatedAt = int64(binary.LittleEndian.Uint64(data[offset:])) // #nosec G115
	offset += 8

	// content hash
	if offset+4 > len(data) {
		return nil, nil, fmt.Errorf("invalid hash length")
	}
	hashLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if offset+hashLen > len(data) {
		return nil, nil, fmt.Errorf("invalid hash data")
	}
	rec.ContentHash = string(data[offset : offset+hashLen])
	offset += hashLen

	// docID
	if offset+4 > len(data) {
		return nil, nil, fmt.Errorf("invalid docID length")
	}
	docIDLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if offset+docIDLen > len(data) {
		return nil, nil, fmt.Errorf("invalid docID data")
	}
	rec.DocID = string(data[offset : offset+docIDLen])
	offset += docIDLen

	// chunk hash (RAG-003), absent before v2.12.0.
	if offset+4 <= len(data) {
		chunkHashLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		if chunkHashLen > 0 && offset+chunkHashLen <= len(data) {
			rec.ChunkHash = string(data[offset : offset+chunkHashLen])
		}
	}

	return rec, qv, nil
}

// isQuantizedRecord checks if a binary record uses the v2 quantized format.
// V2 records start with version byte = 2.
func isQuantizedRecord(data []byte) bool {
	return len(data) > 0 && data[0] == 2
}

// Binary serialization for embedding records (compact, no JSON overhead).
// Format v1: [4B model_len][model][4B dims][4B*dims float32s][8B created_at][4B hash_len][hash][4B docid_len][docid]
func MarshalEmbeddingRecord(rec *EmbeddingRecord) []byte {
	modelBytes := []byte(rec.Model)
	hashBytes := []byte(rec.ContentHash)
	docIDBytes := []byte(rec.DocID)

	chunkHashBytes := []byte(rec.ChunkHash)

	size := 4 + len(modelBytes) + // model
		4 + // dimensions
		4*len(rec.Vector) + // vectors
		8 + // created_at
		4 + len(hashBytes) + // content hash
		4 + len(docIDBytes) + // docID
		4 + len(chunkHashBytes) // chunk hash (v2.12.0+, RAG-003)

	buf := make([]byte, size)
	offset := 0

	// model
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(modelBytes))) // #nosec G115 -- model name length always small
	offset += 4
	copy(buf[offset:], modelBytes)
	offset += len(modelBytes)

	// dimensions
	binary.LittleEndian.PutUint32(buf[offset:], uint32(rec.Dimensions)) // #nosec G115 -- dimensions always positive and bounded
	offset += 4

	// vectors
	for _, v := range rec.Vector {
		binary.LittleEndian.PutUint32(buf[offset:], math.Float32bits(v))
		offset += 4
	}

	// created_at
	binary.LittleEndian.PutUint64(buf[offset:], uint64(rec.CreatedAt)) // #nosec G115 -- timestamp always non-negative
	offset += 8

	// content hash
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(hashBytes))) // #nosec G115 -- hash length always small
	offset += 4
	copy(buf[offset:], hashBytes)
	offset += len(hashBytes)

	// docID
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(docIDBytes))) // #nosec G115 -- docID length always small
	offset += 4
	copy(buf[offset:], docIDBytes)
	offset += len(docIDBytes)

	// chunk hash (RAG-003). Appended after every field the original format
	// had, and the reader stops at docID when nothing follows — so a new
	// reader handles an old record and an old reader ignores this trailing
	// field. That is what makes the change safe for a database in place and
	// for a replica still running the previous version.
	binary.LittleEndian.PutUint32(buf[offset:], uint32(len(chunkHashBytes))) // #nosec G115 -- hash length always small
	offset += 4
	copy(buf[offset:], chunkHashBytes)

	return buf
}

func UnmarshalEmbeddingRecord(data []byte) (*EmbeddingRecord, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("embedding record too short")
	}

	offset := 0
	rec := &EmbeddingRecord{}

	// model
	modelLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if offset+modelLen > len(data) {
		return nil, fmt.Errorf("invalid model length")
	}
	rec.Model = string(data[offset : offset+modelLen])
	offset += modelLen

	// dimensions
	rec.Dimensions = int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4

	// vectors
	if offset+rec.Dimensions*4 > len(data) {
		return nil, fmt.Errorf("invalid vector data")
	}
	rec.Vector = make([]float32, rec.Dimensions)
	for i := 0; i < rec.Dimensions; i++ {
		rec.Vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
	}

	// created_at
	if offset+8 > len(data) {
		return nil, fmt.Errorf("invalid created_at")
	}
	rec.CreatedAt = int64(binary.LittleEndian.Uint64(data[offset:])) // #nosec G115 -- timestamp within int64 range
	offset += 8

	// content hash
	if offset+4 > len(data) {
		return nil, fmt.Errorf("invalid hash length")
	}
	hashLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if offset+hashLen > len(data) {
		return nil, fmt.Errorf("invalid hash data")
	}
	rec.ContentHash = string(data[offset : offset+hashLen])
	offset += hashLen

	// docID
	if offset+4 > len(data) {
		return nil, fmt.Errorf("invalid docID length")
	}
	docIDLen := int(binary.LittleEndian.Uint32(data[offset:]))
	offset += 4
	if offset+docIDLen > len(data) {
		return nil, fmt.Errorf("invalid docID data")
	}
	rec.DocID = string(data[offset : offset+docIDLen])
	offset += docIDLen

	// chunk hash (RAG-003), absent in records written before v2.12.0 — an
	// empty hash simply means this chunk cannot be reused on reindex.
	if offset+4 <= len(data) {
		chunkHashLen := int(binary.LittleEndian.Uint32(data[offset:]))
		offset += 4
		if chunkHashLen > 0 && offset+chunkHashLen <= len(data) {
			rec.ChunkHash = string(data[offset : offset+chunkHashLen])
		}
	}

	return rec, nil
}

// SplitKey splits a BoltDB key by '|' separator.
func SplitKey(key []byte) []string {
	var parts []string
	start := 0
	for i, b := range key {
		if b == '|' {
			parts = append(parts, string(key[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, string(key[start:]))
	return parts
}

// ChunkVectorsByHash returns a document's stored chunk vectors keyed by their
// content hash (RAG-003).
//
// Keyed by hash rather than by index because a chunk that keeps its text but
// moves — anything inserted above it shifts every index below — is still the
// same chunk and its vector is still correct. Index-keyed reuse would miss
// exactly the common edit.
//
// Chunks written before v2.12.0 carry no hash and are skipped, so an old
// database degrades to today's behaviour rather than reusing the wrong vector.
func (vs *VectorStore) ChunkVectorsByHash(collection, docID string) map[string][]float32 {
	prefix := []byte("vec|" + collection + "|" + docID + "#")
	out := make(map[string][]float32)

	_ = vs.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(vs.bucketName)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
			var rec *EmbeddingRecord
			var err error
			if isQuantizedRecord(v) {
				// A quantized record's vector is dequantized on read, so
				// reusing it would silently replace a full-precision vector
				// with a lossy one. Not reusable.
				continue
			}
			rec, err = UnmarshalEmbeddingRecord(v)
			if err != nil || rec == nil || rec.ChunkHash == "" || len(rec.Vector) == 0 {
				continue
			}
			out[rec.ChunkHash] = rec.Vector
		}
		return nil
	})
	return out
}
