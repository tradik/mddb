package vector

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Bounds-checked reading of the hand-rolled embedding record formats
// (TEST-003).
//
// Both decoders used to read a length prefix with
//
//	n := int(binary.LittleEndian.Uint32(data[offset:]))
//	offset += 4
//	if offset+n > len(data) { return error }
//
// which checks the *length* after already reading four bytes that may not be
// there. Fuzzing found the panic in seconds, in both formats. These bytes
// arrive from the replication binlog, so a truncated or corrupt record decoded
// by a follower took the follower down rather than being rejected.
//
// A sticky-error reader makes the decoders total by construction: every read is
// bounds-checked, and once one fails the rest are no-ops, so the decoder can
// keep its straight-line shape and check the error once at the end.

// errRecordTruncated reports a record that ends inside a field.
var errRecordTruncated = errors.New("truncated record")

// maxRecordDimensions bounds what a record may claim before it is rejected.
//
// A length read from untrusted bytes must never reach make(). Even with the
// byte-count check below, `dims * 4` overflows int for a large enough dims,
// which would let the check pass and then allocate whatever the attacker
// asked for. No embedding model produces vectors near this size.
const maxRecordDimensions = 1 << 20

type recordReader struct {
	data []byte
	off  int
	err  error
}

func newRecordReader(data []byte) *recordReader {
	return &recordReader{data: data}
}

// fail records the first error; later reads become no-ops.
func (r *recordReader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf(format, args...)
	}
}

func (r *recordReader) skip(n int) {
	if r.err != nil {
		return
	}
	if r.off+n > len(r.data) {
		r.fail("%w: cannot skip %d bytes at offset %d of %d", errRecordTruncated, n, r.off, len(r.data))
		return
	}
	r.off += n
}

func (r *recordReader) uint32() uint32 {
	if r.err != nil {
		return 0
	}
	if r.off+4 > len(r.data) {
		r.fail("%w: want 4 bytes at offset %d of %d", errRecordTruncated, r.off, len(r.data))
		return 0
	}
	v := binary.LittleEndian.Uint32(r.data[r.off:])
	r.off += 4
	return v
}

func (r *recordReader) uint64() uint64 {
	if r.err != nil {
		return 0
	}
	if r.off+8 > len(r.data) {
		r.fail("%w: want 8 bytes at offset %d of %d", errRecordTruncated, r.off, len(r.data))
		return 0
	}
	v := binary.LittleEndian.Uint64(r.data[r.off:])
	r.off += 8
	return v
}

// bytes returns the next n bytes, or nil when they are not all present.
func (r *recordReader) bytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.off+n > len(r.data) || r.off+n < r.off {
		r.fail("%w: want %d bytes at offset %d of %d", errRecordTruncated, n, r.off, len(r.data))
		return nil
	}
	out := r.data[r.off : r.off+n]
	r.off += n
	return out
}

// lenPrefixedString reads a uint32 length followed by that many bytes.
func (r *recordReader) lenPrefixedString(field string) string {
	n := r.uint32()
	if r.err != nil {
		return ""
	}
	// A length prefix that exceeds the whole record is corruption, not a
	// long string; saying which field failed is what makes a replication
	// error diagnosable.
	if uint64(n) > uint64(len(r.data)) {
		r.fail("%w: %s claims %d bytes, record is %d", errRecordTruncated, field, n, len(r.data))
		return ""
	}
	b := r.bytes(int(n))
	if r.err != nil {
		return ""
	}
	return string(b)
}

// float32s reads n little-endian float32 values.
func (r *recordReader) float32s(n int) []float32 {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > maxRecordDimensions {
		r.fail("record claims %d dimensions, over the %d limit", n, maxRecordDimensions)
		return nil
	}
	raw := r.bytes(n * 4)
	if r.err != nil {
		return nil
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out
}

// remaining reports how many bytes are left, 0 once an error is recorded.
func (r *recordReader) remaining() int {
	if r.err != nil {
		return 0
	}
	return len(r.data) - r.off
}
