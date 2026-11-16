package vector

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
)

// The on-disk format of an index checkpoint (SRCH-023).
//
// The HNSW graph, the IVF clusters, the PQ and OPQ codebooks and the SQ and
// SQ4 scales lived in memory only, so every start built them again from the
// stored vectors: about a minute of graph inserts and one to two minutes of
// k-means for 200,000 vectors. A checkpoint is one file per collection that
// holds those structures, so a start reads them back instead.
//
// Layout, little-endian throughout:
//
//	magic   8 bytes  "MDDBANN\x00"
//	version u32      PersistFormatVersion; any other value is refused
//	frames           [u32 length][u32 CRC-32C of the payload][payload]
//	trailer          [u32 0][u64 payload bytes in all][u32 CRC-32C of those 8]
//
// The payload of the frames, concatenated, is the stream the sections are
// written to. Frames are at most persistFrameSize, so the reader verifies a
// megabyte at a time and never has to hold the file: nothing is parsed that
// did not pass its checksum, and a file cut short has no trailer. Every
// count read from the stream is checked against the bytes the file still
// has before anything is allocated for it, so a length that survived its
// checksum by design — a crafted file — cannot ask for gigabytes.

// PersistFormatVersion is the version this build writes and the only one it
// reads. A checkpoint of another version is not converted: the indexes are
// rebuilt from the stored vectors, as they were before checkpoints existed.
const PersistFormatVersion uint32 = 1

const (
	// persistMagic opens every checkpoint file.
	persistMagic = "MDDBANN\x00"
	// persistFrameSize is the largest frame payload: what the writer buffers
	// and the reader verifies at a time.
	persistFrameSize = 1 << 20
	// persistMaxString bounds a key or name read from a checkpoint.
	persistMaxString = 1 << 16
)

var (
	// ErrCheckpointCorrupt is a checkpoint whose bytes are not what was
	// written: a failed checksum, a missing trailer, a count the file cannot
	// hold, or a structure that would not be safe to search.
	ErrCheckpointCorrupt = errors.New("index checkpoint is corrupt")
	// ErrCheckpointVersion is a checkpoint written in another format version.
	ErrCheckpointVersion = errors.New("index checkpoint has another format version")
	// ErrCheckpointStale is a checkpoint that does not describe the vectors
	// the store holds now.
	ErrCheckpointStale = errors.New("index checkpoint does not match the stored vectors")

	persistCRC = crc32.MakeTable(crc32.Castagnoli)
)

// corruptf is ErrCheckpointCorrupt with what was found.
func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCheckpointCorrupt, fmt.Sprintf(format, args...))
}

// vectorFingerprint is a 64-bit FNV-1a over a vector's float32 bit patterns,
// a word at a time. A checkpoint stores it for every vector a structure was
// built from, and a structure is used only for a stored vector whose
// fingerprint still matches: the store is the truth, the checkpoint a cache
// of what was derived from it.
func vectorFingerprint(v []float32) uint64 {
	h := uint64(14695981039346656037) ^ uint64(len(v))
	for _, x := range v {
		h ^= uint64(math.Float32bits(x))
		h *= 1099511628211
	}
	return h ^ h>>32 // the multiplication only carries upwards
}

// frameWriter cuts what is written to it into checksummed frames.
type frameWriter struct {
	w     io.Writer
	buf   []byte
	total uint64
	err   error // the first write error; everything after it is dropped
}

func newFrameWriter(w io.Writer) *frameWriter {
	return &frameWriter{w: w, buf: make([]byte, 0, persistFrameSize)}
}

// Write implements io.Writer. It reports no error itself: close does.
func (f *frameWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		room := persistFrameSize - len(f.buf)
		take := min(room, len(p))
		f.buf = append(f.buf, p[:take]...)
		p = p[take:]
		if len(f.buf) == persistFrameSize {
			f.flush()
		}
	}
	return n, nil
}

// flush writes the buffered payload as one frame.
func (f *frameWriter) flush() {
	if len(f.buf) == 0 || f.err != nil {
		f.buf = f.buf[:0]
		return
	}
	var head [8]byte
	binary.LittleEndian.PutUint32(head[0:], uint32(len(f.buf))) // #nosec G115 -- at most persistFrameSize
	binary.LittleEndian.PutUint32(head[4:], crc32.Checksum(f.buf, persistCRC))
	if _, f.err = f.w.Write(head[:]); f.err == nil {
		_, f.err = f.w.Write(f.buf)
	}
	f.total += uint64(len(f.buf))
	f.buf = f.buf[:0]
}

// close writes the last frame and the trailer, and reports the first error
// of the whole stream.
func (f *frameWriter) close() error {
	f.flush()
	if f.err != nil {
		return f.err
	}
	var tail [16]byte
	binary.LittleEndian.PutUint64(tail[4:], f.total)
	binary.LittleEndian.PutUint32(tail[12:], crc32.Checksum(tail[4:12], persistCRC))
	_, f.err = f.w.Write(tail[:])
	return f.err
}

// frameReader gives back the payload of a framed stream, verified.
type frameReader struct {
	r     io.Reader
	buf   []byte
	off   int
	total uint64
	left  int64 // bytes of the file not handed out yet: the allocation budget
	done  bool
}

// newFrameReader reads frames from r, which has size bytes left.
func newFrameReader(r io.Reader, size int64) *frameReader {
	return &frameReader{r: r, left: size}
}

// Read implements io.Reader over the verified payload.
func (f *frameReader) Read(p []byte) (int, error) {
	if f.off == len(f.buf) {
		if err := f.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, f.buf[f.off:])
	f.off += n
	f.left -= int64(n)
	return n, nil
}

// next loads and verifies the next frame, or the trailer.
func (f *frameReader) next() error {
	if f.done {
		return io.EOF
	}
	var head [8]byte
	if _, err := io.ReadFull(f.r, head[:4]); err != nil {
		return corruptf("no trailer: the file ends early")
	}
	f.left -= 4
	n := binary.LittleEndian.Uint32(head[:4])
	if n == 0 {
		return f.trailer()
	}
	if n > persistFrameSize || int64(n) > f.left {
		return corruptf("frame of %d bytes", n)
	}
	if _, err := io.ReadFull(f.r, head[4:]); err != nil {
		return corruptf("frame header cut short")
	}
	f.left -= 4
	if cap(f.buf) < int(n) {
		f.buf = make([]byte, n)
	}
	f.buf = f.buf[:n]
	if _, err := io.ReadFull(f.r, f.buf); err != nil {
		return corruptf("frame cut short")
	}
	if crc32.Checksum(f.buf, persistCRC) != binary.LittleEndian.Uint32(head[4:]) {
		return corruptf("checksum mismatch")
	}
	f.off = 0
	f.total += uint64(n)
	return nil
}

// trailer verifies the end of the stream: the payload size written, its
// checksum, and that nothing follows.
func (f *frameReader) trailer() error {
	var tail [13]byte
	n, _ := io.ReadFull(f.r, tail[:])
	if n != 12 {
		return corruptf("trailer of %d bytes", n)
	}
	if crc32.Checksum(tail[:8], persistCRC) != binary.LittleEndian.Uint32(tail[8:12]) ||
		binary.LittleEndian.Uint64(tail[:8]) != f.total {
		return corruptf("trailer does not match the frames read")
	}
	f.done, f.buf, f.off = true, f.buf[:0], 0
	return io.EOF
}

// persistEncoder writes the primitives of a checkpoint stream. Errors are
// the frame writer's, reported once at the end.
type persistEncoder struct {
	w       io.Writer
	scratch []byte
}

func (e *persistEncoder) u8(v uint8) { _, _ = e.w.Write([]byte{v}) }

func (e *persistEncoder) u16(v uint16) {
	_, _ = e.w.Write(binary.LittleEndian.AppendUint16(e.scratch[:0], v))
}

func (e *persistEncoder) u32(v uint32) {
	_, _ = e.w.Write(binary.LittleEndian.AppendUint32(e.scratch[:0], v))
}

func (e *persistEncoder) u64(v uint64) {
	_, _ = e.w.Write(binary.LittleEndian.AppendUint64(e.scratch[:0], v))
}

func (e *persistEncoder) str(s string) {
	e.u32(uint32(len(s))) // #nosec G115 -- keys and names, far below 4 GiB
	_, _ = io.WriteString(e.w, s)
}

func (e *persistEncoder) bytes(b []byte) { _, _ = e.w.Write(b) }

// f32s writes the values without a count: the reader knows it.
func (e *persistEncoder) f32s(v []float32) {
	b := e.scratch[:0]
	for _, x := range v {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(x))
	}
	e.scratch = b[:0]
	_, _ = e.w.Write(b)
}

// i32s writes the values without a count.
func (e *persistEncoder) i32s(v []int32) {
	b := e.scratch[:0]
	for _, x := range v {
		b = binary.LittleEndian.AppendUint32(b, uint32(x)) // #nosec G115 -- node ids are never negative
	}
	e.scratch = b[:0]
	_, _ = e.w.Write(b)
}

// persistDecoder reads the primitives back. The first error sticks: every
// later read returns zero values, and the caller checks err once per record.
type persistDecoder struct {
	r       *frameReader
	err     error
	scratch []byte
}

// fail records the first error.
func (d *persistDecoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// read fills p, or fails.
func (d *persistDecoder) read(p []byte) {
	if d.err != nil {
		clear(p)
		return
	}
	if _, err := io.ReadFull(d.r, p); err != nil {
		clear(p)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			err = corruptf("the stream ends inside a record")
		}
		d.fail(err)
	}
}

// fixed reads n bytes into the scratch buffer; n is small and constant.
func (d *persistDecoder) fixed(n int) []byte {
	if cap(d.scratch) < n {
		d.scratch = make([]byte, n)
	}
	b := d.scratch[:n]
	d.read(b)
	return b
}

func (d *persistDecoder) u8() uint8   { return d.fixed(1)[0] }
func (d *persistDecoder) u16() uint16 { return binary.LittleEndian.Uint16(d.fixed(2)) }
func (d *persistDecoder) u32() uint32 { return binary.LittleEndian.Uint32(d.fixed(4)) }
func (d *persistDecoder) u64() uint64 { return binary.LittleEndian.Uint64(d.fixed(8)) }

// count reads nothing: it checks that n records of at least size bytes each
// can still be in the file, and returns n as an int, or 0 with the decoder
// failed. Every allocation sized by the file goes through it.
func (d *persistDecoder) count(n uint64, size int) int {
	if d.err != nil {
		return 0
	}
	if n > uint64(max(d.r.left, 0))/uint64(size) { // #nosec G115 -- both operands are positive
		d.fail(corruptf("a count of %d does not fit in the %d bytes left", n, d.r.left))
		return 0
	}
	return int(n) // #nosec G115 -- bounded by the file size just above
}

func (d *persistDecoder) str() string {
	n := d.u32()
	if n > persistMaxString {
		d.fail(corruptf("string of %d bytes", n))
		return ""
	}
	return string(d.bytes(d.count(uint64(n), 1)))
}

// bytes reads n bytes into a slice of its own; n has been through count.
func (d *persistDecoder) bytes(n int) []byte {
	b := make([]byte, n)
	d.read(b)
	return b
}

// f32s reads n values; n has been through count with size 4.
func (d *persistDecoder) f32s(n int) []float32 {
	raw := d.bytes(4 * n)
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out
}

// i32s reads n node ids; n has been through count with size 4.
func (d *persistDecoder) i32s(n int) []int32 {
	raw := d.bytes(4 * n)
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(binary.LittleEndian.Uint32(raw[4*i:])) // #nosec G115 -- validated against the node count by the caller
	}
	return out
}
