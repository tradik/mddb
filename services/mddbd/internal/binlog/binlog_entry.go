package binlog

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// BinlogEntryType defines the type of binlog entry
type BinlogEntryType byte

// Binlog entry type constants.
const (
	BinlogPut          BinlogEntryType = 1
	BinlogDelete       BinlogEntryType = 2
	BinlogDeleteBucket BinlogEntryType = 3
	BinlogCheckpoint   BinlogEntryType = 4
)

// BinlogEntry represents a single replication log entry
type BinlogEntry struct {
	LSN        uint64
	Type       BinlogEntryType
	Timestamp  int64
	BucketName string
	Key        []byte
	Value      []byte
	Checksum   uint32
	// Term is the election term the entry was written in (GO-051). It is
	// not in the file: the log keeps where each term begins
	// (binlog_terms.go), and readers of the file ask it with TermAt.
	Term uint64
}

// binlogEntryHeaderSize is the fixed header size:
// lsn(8) + type(1) + timestamp(8) + bucketNameLen(2) = 19
const binlogEntryHeaderSize = 19

// MarshalBinlogEntry serializes a BinlogEntry into binary format:
// [lsn:8][type:1][timestamp:8][bucketNameLen:2][bucketName:N][keyLen:4][key:N][valueLen:4][value:N][checksum:4]
func MarshalBinlogEntry(e *BinlogEntry) []byte {
	bucketName := []byte(e.BucketName)
	totalSize := binlogEntryHeaderSize + len(bucketName) + 4 + len(e.Key) + 4 + len(e.Value) + 4
	buf := make([]byte, 0, totalSize)

	// LSN
	buf = binary.BigEndian.AppendUint64(buf, e.LSN)
	// Type
	buf = append(buf, byte(e.Type))
	// Timestamp
	buf = binary.BigEndian.AppendUint64(buf, uint64(e.Timestamp)) // #nosec G115 -- timestamp always non-negative
	// BucketName
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(bucketName))) // #nosec G115 -- bucket name length always small
	buf = append(buf, bucketName...)
	// Key
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(e.Key))) // #nosec G115 -- key length always bounded
	buf = append(buf, e.Key...)
	// Value
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(e.Value))) // #nosec G115 -- value length always bounded
	buf = append(buf, e.Value...)
	// Checksum over everything before checksum field
	checksum := crc32.ChecksumIEEE(buf)
	buf = binary.BigEndian.AppendUint32(buf, checksum)

	return buf
}

// UnmarshalBinlogEntry deserializes a BinlogEntry from binary data.
// Returns the entry and number of bytes consumed, or an error.
func UnmarshalBinlogEntry(data []byte) (*BinlogEntry, int, error) {
	if len(data) < binlogEntryHeaderSize {
		return nil, 0, fmt.Errorf("binlog entry too short: %d < %d", len(data), binlogEntryHeaderSize)
	}

	pos := 0

	// LSN
	lsn := binary.BigEndian.Uint64(data[pos:])
	pos += 8

	// Type
	entryType := BinlogEntryType(data[pos])
	pos++

	// Timestamp
	timestamp := int64(binary.BigEndian.Uint64(data[pos:])) // #nosec G115 -- timestamp within int64 range
	pos += 8

	// BucketName
	bucketNameLen := int(binary.BigEndian.Uint16(data[pos:]))
	pos += 2
	if pos+bucketNameLen > len(data) {
		return nil, 0, fmt.Errorf("binlog entry truncated at bucket name")
	}
	bucketName := string(data[pos : pos+bucketNameLen])
	pos += bucketNameLen

	// Key
	if pos+4 > len(data) {
		return nil, 0, fmt.Errorf("binlog entry truncated at key length")
	}
	keyLen := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4
	if pos+keyLen > len(data) {
		return nil, 0, fmt.Errorf("binlog entry truncated at key data")
	}
	key := make([]byte, keyLen)
	copy(key, data[pos:pos+keyLen])
	pos += keyLen

	// Value
	if pos+4 > len(data) {
		return nil, 0, fmt.Errorf("binlog entry truncated at value length")
	}
	valueLen := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4
	if pos+valueLen > len(data) {
		return nil, 0, fmt.Errorf("binlog entry truncated at value data")
	}
	value := make([]byte, valueLen)
	copy(value, data[pos:pos+valueLen])
	pos += valueLen

	// Checksum
	if pos+4 > len(data) {
		return nil, 0, fmt.Errorf("binlog entry truncated at checksum")
	}
	storedChecksum := binary.BigEndian.Uint32(data[pos:])
	computedChecksum := crc32.ChecksumIEEE(data[:pos])
	if storedChecksum != computedChecksum {
		return nil, 0, fmt.Errorf("binlog entry checksum mismatch: stored=%d computed=%d", storedChecksum, computedChecksum)
	}
	pos += 4

	return &BinlogEntry{
		LSN:        lsn,
		Type:       entryType,
		Timestamp:  timestamp,
		BucketName: bucketName,
		Key:        key,
		Value:      value,
		Checksum:   storedChecksum,
	}, pos, nil
}

// BinlogOps collects binlog entries during a BoltDB transaction. Logged
// writes them to the log as the transaction's last step; FlushTo appends
// them after a commit made some other way.
type BinlogOps struct {
	entries []*BinlogEntry
	staged  bool // Logged wrote them before the commit
}

// Put records a Put operation.
func (bo *BinlogOps) Put(bucket string, key, value []byte) {
	bo.entries = append(bo.entries, &BinlogEntry{
		Type:       BinlogPut,
		BucketName: bucket,
		Key:        copyBytes(key),
		Value:      copyBytes(value),
	})
}

// Delete records a Delete operation.
func (bo *BinlogOps) Delete(bucket string, key []byte) {
	bo.entries = append(bo.entries, &BinlogEntry{
		Type:       BinlogDelete,
		BucketName: bucket,
		Key:        copyBytes(key),
	})
}

// FlushTo appends all collected entries to the binlog.
// Safe to call with nil binlog (no-op).
func (bo *BinlogOps) FlushTo(bl *Binlog) {
	if bl == nil || len(bo.entries) == 0 {
		return
	}
	_ = bl.AppendBatch(bo.entries)
}

// Len returns the number of collected entries.
func (bo *BinlogOps) Len() int {
	return len(bo.entries)
}

func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp
}

func (t BinlogEntryType) String() string {
	switch t {
	case BinlogPut:
		return "Put"
	case BinlogDelete:
		return "Delete"
	case BinlogDeleteBucket:
		return "DeleteBucket"
	case BinlogCheckpoint:
		return "Checkpoint"
	default:
		return fmt.Sprintf("Unknown(%d)", t)
	}
}
