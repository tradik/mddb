package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestZeroCopyManagerNew(t *testing.T) {
	zcm := NewZeroCopyManager()
	if zcm == nil {
		t.Fatal("expected non-nil ZeroCopyManager")
		return
	}
	if !zcm.enabled {
		t.Error("expected enabled")
	}
}

func TestZeroCopyManagerCopyFile(t *testing.T) {
	zcm := NewZeroCopyManager()
	dir := t.TempDir()

	// Create source file
	srcPath := filepath.Join(dir, "src.txt")
	content := []byte("hello world zero copy test data")
	if err := os.WriteFile(srcPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	// Create destination file
	dstPath := filepath.Join(dir, "dst.txt")

	src, err := os.Open(srcPath) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(dstPath) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dst.Close() }()

	n, err := zcm.CopyFile(dst, src, int64(len(content)))
	if err != nil {
		t.Fatalf("CopyFile error: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("expected %d bytes copied, got %d", len(content), n)
	}

	// Verify
	got, err := os.ReadFile(dstPath) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}

	// Check stats
	stats := zcm.Stats()
	if stats.Transfers != 1 {
		t.Errorf("expected 1 transfer, got %d", stats.Transfers)
	}
	if stats.BytesCopy != uint64(len(content)) {
		t.Errorf("expected %d bytes, got %d", len(content), stats.BytesCopy)
	}
}

func TestZeroCopyManagerCopyFileRange(t *testing.T) {
	zcm := NewZeroCopyManager()
	dir := t.TempDir()

	// Create source file with known content
	srcPath := filepath.Join(dir, "src.txt")
	content := []byte("0123456789ABCDEF")
	if err := os.WriteFile(srcPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	// Create destination file
	dstPath := filepath.Join(dir, "dst.txt")
	// Pre-fill destination with zeros
	dstContent := make([]byte, 20)
	if err := os.WriteFile(dstPath, dstContent, 0600); err != nil {
		t.Fatal(err)
	}

	src, err := os.Open(srcPath) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(dstPath, os.O_RDWR, 0600) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dst.Close() }()

	// Copy 5 bytes from offset 5 in src to offset 2 in dst
	n, err := zcm.CopyFileRange(dst, src, 5, 2, 5)
	if err != nil {
		t.Fatalf("CopyFileRange error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes copied, got %d", n)
	}

	// Verify destination
	got, err := os.ReadFile(dstPath) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	// Bytes at offset 2-6 should be "56789"
	if string(got[2:7]) != "56789" {
		t.Errorf("expected '56789' at offset 2, got %q", got[2:7])
	}
}

func TestZeroCopyManagerCopyFileRangeBeyondEOF(t *testing.T) {
	zcm := NewZeroCopyManager()
	dir := t.TempDir()

	srcPath := filepath.Join(dir, "src.txt")
	content := []byte("short")
	if err := os.WriteFile(srcPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	dstPath := filepath.Join(dir, "dst.txt")
	dst, err := os.Create(dstPath) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dst.Close() }()

	src, err := os.Open(srcPath) //nolint:gosec // G304: temp file in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()

	// Try to copy 100 bytes from a 5-byte file
	n, err := zcm.CopyFileRange(dst, src, 0, 0, 100)
	if err != nil {
		t.Fatalf("expected no error for short read, got: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("expected %d bytes, got %d", len(content), n)
	}
}

func TestZeroCopyManagerStreamCopy(t *testing.T) {
	zcm := NewZeroCopyManager()

	src := strings.NewReader("hello stream copy")
	var dst bytes.Buffer

	n, err := zcm.StreamCopy(&dst, src)
	if err != nil {
		t.Fatalf("StreamCopy error: %v", err)
	}
	if n != 17 {
		t.Errorf("expected 17 bytes, got %d", n)
	}
	if dst.String() != "hello stream copy" {
		t.Errorf("got %q", dst.String())
	}

	stats := zcm.Stats()
	if stats.Transfers != 1 {
		t.Errorf("expected 1 transfer, got %d", stats.Transfers)
	}
}

func TestZeroCopyManagerStreamCopyEmpty(t *testing.T) {
	zcm := NewZeroCopyManager()

	src := strings.NewReader("")
	var dst bytes.Buffer

	n, err := zcm.StreamCopy(&dst, src)
	if err != nil {
		t.Fatalf("StreamCopy error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes, got %d", n)
	}
}

func TestZeroCopyManagerStats(t *testing.T) {
	zcm := NewZeroCopyManager()

	stats := zcm.Stats()
	if !stats.Enabled {
		t.Error("expected enabled")
	}
	if stats.Transfers != 0 {
		t.Errorf("expected 0 transfers, got %d", stats.Transfers)
	}
	if stats.BytesCopy != 0 {
		t.Errorf("expected 0 bytes, got %d", stats.BytesCopy)
	}
}

// --- BufferPool tests ---

func TestBufferPoolNew(t *testing.T) {
	bp := NewBufferPool(4096)
	if bp == nil {
		t.Fatal("expected non-nil BufferPool")
		return
	}
	if bp.size != 4096 {
		t.Errorf("expected size 4096, got %d", bp.size)
	}
}

func TestBufferPoolGetPut(t *testing.T) {
	bp := NewBufferPool(1024)

	buf := bp.Get()
	if len(buf) != 1024 {
		t.Errorf("expected buffer size 1024, got %d", len(buf))
	}

	// Get another - the pool's New function creates fresh buffers
	buf2 := bp.Get()
	if len(buf2) != 1024 {
		t.Errorf("expected buffer size 1024, got %d", len(buf2))
	}

	// Note: Put stores &buf (pointer to slice) which is incompatible with
	// Get's type assertion for []byte. We only test Put doesn't panic;
	// we do NOT call Get after Put since the source code has a type mismatch
	// that would cause a panic.
	bp.Put(buf)
}

func TestBufferPoolPutWrongSize(t *testing.T) {
	bp := NewBufferPool(1024)

	// Buffer with wrong size should be silently ignored
	wrongBuf := make([]byte, 512)
	bp.Put(wrongBuf) // should not panic
}

// --- ZeroCopyReader tests ---

func TestZeroCopyReaderNew(t *testing.T) {
	r := strings.NewReader("hello")
	zcr := NewZeroCopyReader(r, 1024)
	if zcr == nil {
		t.Fatal("expected non-nil ZeroCopyReader")
	}
	defer func() { _ = zcr.Close() }()
}

func TestZeroCopyReaderRead(t *testing.T) {
	content := "hello world from zero copy reader"
	r := strings.NewReader(content)
	zcr := NewZeroCopyReader(r, 1024)
	defer func() { _ = zcr.Close() }()

	buf := make([]byte, 100)
	n, err := zcr.Read(buf)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if string(buf[:n]) != content {
		t.Errorf("got %q, want %q", string(buf[:n]), content)
	}
}

func TestZeroCopyReaderReadSmallBuffer(t *testing.T) {
	content := "abcdefghij"
	r := strings.NewReader(content)
	zcr := NewZeroCopyReader(r, 1024)
	defer func() { _ = zcr.Close() }()

	// Read in small chunks
	var result []byte
	buf := make([]byte, 3)
	for {
		n, err := zcr.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
		if len(result) >= len(content) {
			break
		}
	}

	if string(result) != content {
		t.Errorf("got %q, want %q", string(result), content)
	}
}

func TestZeroCopyReaderReadSmallInternalBuffer(t *testing.T) {
	content := "abcdefghijklmnop"
	r := strings.NewReader(content)
	// Small internal buffer forces multiple refills
	zcr := NewZeroCopyReader(r, 4)
	defer func() { _ = zcr.Close() }()

	var result []byte
	buf := make([]byte, 100)
	for {
		n, err := zcr.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
		if len(result) >= len(content) {
			break
		}
	}

	if string(result) != content {
		t.Errorf("got %q, want %q", string(result), content)
	}
}

func TestZeroCopyReaderClose(t *testing.T) {
	r := strings.NewReader("data")
	zcr := NewZeroCopyReader(r, 1024)

	// Read to allocate buffer
	buf := make([]byte, 10)
	_, _ = zcr.Read(buf)

	// Close should release buffer
	err := zcr.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if zcr.buffer != nil {
		t.Error("expected buffer to be nil after Close")
	}
}

func TestZeroCopyReaderCloseWithoutRead(t *testing.T) {
	r := strings.NewReader("data")
	zcr := NewZeroCopyReader(r, 1024)

	// Close without reading (buffer is nil)
	err := zcr.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

// --- ZeroCopyWriter tests ---

func TestZeroCopyWriterNew(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 1024)
	if zcw == nil {
		t.Fatal("expected non-nil ZeroCopyWriter")
	}
	defer func() { _ = zcw.Close() }()
}

func TestZeroCopyWriterWrite(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 1024)

	n, err := zcw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}

	// Data is buffered, not yet flushed
	if buf.Len() != 0 {
		t.Logf("buffer may have been flushed early, len=%d", buf.Len())
	}

	// Flush
	err = zcw.Flush()
	if err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if buf.String() != "hello" {
		t.Errorf("got %q, want %q", buf.String(), "hello")
	}
}

func TestZeroCopyWriterWriteLargerThanBuffer(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 8) // small internal buffer

	data := []byte("hello world from zero copy writer")
	n, err := zcw.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}

	// Close flushes remaining
	err = zcw.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	if buf.String() != string(data) {
		t.Errorf("got %q, want %q", buf.String(), string(data))
	}
}

func TestZeroCopyWriterFlushEmpty(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 1024)

	// Flush without writing anything
	err := zcw.Flush()
	if err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty buffer, got len %d", buf.Len())
	}
}

func TestZeroCopyWriterClose(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 1024)

	_, _ = zcw.Write([]byte("data"))

	err := zcw.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
	if buf.String() != "data" {
		t.Errorf("got %q, want %q", buf.String(), "data")
	}
	if zcw.buffer != nil {
		t.Error("expected buffer to be nil after Close")
	}
}

func TestZeroCopyWriterCloseWithoutWrite(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 1024)

	err := zcw.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestZeroCopyWriterMultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 1024)

	_, _ = zcw.Write([]byte("hello "))
	_, _ = zcw.Write([]byte("world"))

	err := zcw.Close()
	if err != nil {
		t.Fatalf("Close error: %v", err)
	}

	if buf.String() != "hello world" {
		t.Errorf("got %q, want %q", buf.String(), "hello world")
	}
}

func TestZeroCopyWriterFlushOnFullBuffer(t *testing.T) {
	var buf bytes.Buffer
	zcw := NewZeroCopyWriter(&buf, 4) // 4-byte buffer

	// Write exactly 4 bytes - should fill buffer and flush
	n, err := zcw.Write([]byte("abcd"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 bytes, got %d", n)
	}

	// Buffer should have been flushed
	if buf.Len() != 4 {
		t.Errorf("expected buffer flushed with 4 bytes, got %d", buf.Len())
	}

	_ = zcw.Close()
}

// GO-020. Two defects hid behind the fact that nothing used these: every
// reader and writer built its own pool, so no buffer was ever reused, and the
// reader dropped data that arrived together with an error.

func TestBufferPoolIsSharedBetweenReaders(t *testing.T) {
	const size = 4096

	a := NewZeroCopyReader(strings.NewReader("first"), size)
	b := NewZeroCopyReader(strings.NewReader("second"), size)

	if a.bufferPool != b.bufferPool {
		t.Error("two readers of the same size hold different pools, so no buffer is ever reused")
	}

	w := NewZeroCopyWriter(io.Discard, size)
	if w.bufferPool != a.bufferPool {
		t.Error("a writer and a reader of the same size hold different pools")
	}

	if NewZeroCopyReader(strings.NewReader(""), size*2).bufferPool == a.bufferPool {
		t.Error("readers of different sizes share one pool, so Get would return the wrong length")
	}
}

func TestPooledBufferIsActuallyReused(t *testing.T) {
	const size = 8192
	pool := bufferPoolFor(size)

	first := pool.Get()
	first[0] = 0x7f
	pool.Put(first)

	// sync.Pool may drop entries, so this asserts the mechanism works rather
	// than that a specific buffer comes back.
	second := pool.Get()
	if len(second) != size {
		t.Fatalf("Get returned %d bytes, want %d", len(second), size)
	}
	pool.Put(second)

	// A buffer of the wrong size must not enter the pool: a later Get would
	// hand a caller less than it asked for.
	pool.Put(make([]byte, size/2))
	if got := pool.Get(); len(got) != size {
		t.Errorf("a short buffer was pooled and came back: %d bytes", len(got))
	}
}

// A reader that returns data and io.EOF in one call is allowed to, and used to
// have its last bytes discarded here.
type dataWithEOFReader struct {
	data []byte
	done bool
}

func (r *dataWithEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), io.EOF
}

func TestReaderKeepsDataThatArrivesWithEOF(t *testing.T) {
	const payload = "the last bytes of the stream"

	zr := NewZeroCopyReader(&dataWithEOFReader{data: []byte(payload)}, 4096)
	defer func() { _ = zr.Close() }()

	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != payload {
		t.Errorf("read %q, want %q", got, payload)
	}
}

func TestReaderSurfacesARealError(t *testing.T) {
	want := errors.New("disk is on fire")
	zr := NewZeroCopyReader(iotest.ErrReader(want), 4096)
	defer func() { _ = zr.Close() }()

	if _, err := io.ReadAll(zr); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

// copyFile reads through the pooled reader; a copy that loses its tail would
// corrupt every backup.
func TestCopyFileIsByteExactAcrossBufferBoundaries(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	// Deliberately not a multiple of the buffer size.
	payload := make([]byte, copyBufferSize*2+1237)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	if err := os.WriteFile(src, payload, 0600); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("the copy is %d bytes, the source is %d", len(got), len(payload))
	}
}
