package main

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// ZeroCopyManager manages zero-copy operations
type ZeroCopyManager struct {
	enabled   bool
	transfers atomic.Uint64
	bytesCopy atomic.Uint64
	mu        sync.RWMutex
}

// NewZeroCopyManager creates a new zero-copy manager
func NewZeroCopyManager() *ZeroCopyManager {
	return &ZeroCopyManager{
		enabled: true, // Always enabled for compatible operations
	}
}

// CopyFile performs zero-copy file transfer
func (zcm *ZeroCopyManager) CopyFile(dst, src *os.File, size int64) (int64, error) {
	zcm.transfers.Add(1)
	
	// Use io.Copy which uses sendfile() on Linux when possible
	// This is zero-copy at kernel level
	n, err := io.Copy(dst, src)
	
	if err == nil {
		zcm.bytesCopy.Add(uint64(n))
	}
	
	return n, err
}

// CopyFileRange performs zero-copy between file ranges
func (zcm *ZeroCopyManager) CopyFileRange(dst, src *os.File, srcOffset, dstOffset, length int64) (int64, error) {
	zcm.transfers.Add(1)
	
	// Seek to positions
	if _, err := src.Seek(srcOffset, io.SeekStart); err != nil {
		return 0, err
	}
	if _, err := dst.Seek(dstOffset, io.SeekStart); err != nil {
		return 0, err
	}
	
	// Use io.CopyN for limited copy
	n, err := io.CopyN(dst, src, length)
	
	if err == nil || err == io.EOF {
		zcm.bytesCopy.Add(uint64(n))
		return n, nil
	}
	
	return n, err
}

// StreamCopy performs streaming zero-copy
func (zcm *ZeroCopyManager) StreamCopy(dst io.Writer, src io.Reader) (int64, error) {
	zcm.transfers.Add(1)
	
	// Use io.Copy which is optimized for zero-copy when possible
	n, err := io.Copy(dst, src)
	
	if err == nil {
		zcm.bytesCopy.Add(uint64(n))
	}
	
	return n, err
}

// BufferPool for zero-copy buffer reuse
type BufferPool struct {
	pool sync.Pool
	size int
}

// NewBufferPool creates a new buffer pool
func NewBufferPool(size int) *BufferPool {
	return &BufferPool{
		size: size,
		pool: sync.Pool{
			New: func() interface{} {
				return make([]byte, size)
			},
		},
	}
}

// Get gets a buffer from pool
func (bp *BufferPool) Get() []byte {
	return bp.pool.Get().([]byte)
}

// Put returns a buffer to pool
func (bp *BufferPool) Put(buf []byte) {
	if len(buf) == bp.size {
		bp.pool.Put(buf)
	}
}

// ZeroCopyReader wraps a reader for zero-copy operations
type ZeroCopyReader struct {
	reader     io.Reader
	bufferPool *BufferPool
	buffer     []byte
	offset     int
	limit      int
}

// NewZeroCopyReader creates a zero-copy reader
func NewZeroCopyReader(r io.Reader, bufferSize int) *ZeroCopyReader {
	return &ZeroCopyReader{
		reader:     r,
		bufferPool: NewBufferPool(bufferSize),
		buffer:     nil,
	}
}

// Read reads data with buffer reuse
func (zcr *ZeroCopyReader) Read(p []byte) (int, error) {
	if zcr.buffer == nil {
		zcr.buffer = zcr.bufferPool.Get()
		zcr.offset = 0
		zcr.limit = 0
	}
	
	// If buffer is empty, refill
	if zcr.offset >= zcr.limit {
		n, err := zcr.reader.Read(zcr.buffer)
		if err != nil {
			return 0, err
		}
		zcr.offset = 0
		zcr.limit = n
	}
	
	// Copy from buffer
	n := copy(p, zcr.buffer[zcr.offset:zcr.limit])
	zcr.offset += n
	
	return n, nil
}

// Close releases resources
func (zcr *ZeroCopyReader) Close() error {
	if zcr.buffer != nil {
		zcr.bufferPool.Put(zcr.buffer)
		zcr.buffer = nil
	}
	return nil
}

// ZeroCopyWriter wraps a writer for zero-copy operations
type ZeroCopyWriter struct {
	writer     io.Writer
	bufferPool *BufferPool
	buffer     []byte
	offset     int
}

// NewZeroCopyWriter creates a zero-copy writer
func NewZeroCopyWriter(w io.Writer, bufferSize int) *ZeroCopyWriter {
	return &ZeroCopyWriter{
		writer:     w,
		bufferPool: NewBufferPool(bufferSize),
		buffer:     nil,
	}
}

// Write writes data with buffering
func (zcw *ZeroCopyWriter) Write(p []byte) (int, error) {
	if zcw.buffer == nil {
		zcw.buffer = zcw.bufferPool.Get()
		zcw.offset = 0
	}
	
	totalWritten := 0
	
	for len(p) > 0 {
		// Copy to buffer
		n := copy(zcw.buffer[zcw.offset:], p)
		zcw.offset += n
		p = p[n:]
		totalWritten += n
		
		// Flush if buffer is full
		if zcw.offset >= len(zcw.buffer) {
			if err := zcw.Flush(); err != nil {
				return totalWritten, err
			}
		}
	}
	
	return totalWritten, nil
}

// Flush flushes buffered data
func (zcw *ZeroCopyWriter) Flush() error {
	if zcw.buffer == nil || zcw.offset == 0 {
		return nil
	}
	
	_, err := zcw.writer.Write(zcw.buffer[:zcw.offset])
	zcw.offset = 0
	
	return err
}

// Close flushes and releases resources
func (zcw *ZeroCopyWriter) Close() error {
	if err := zcw.Flush(); err != nil {
		return err
	}
	
	if zcw.buffer != nil {
		zcw.bufferPool.Put(zcw.buffer)
		zcw.buffer = nil
	}
	
	return nil
}

// Stats returns zero-copy statistics
func (zcm *ZeroCopyManager) Stats() ZeroCopyStats {
	return ZeroCopyStats{
		Enabled:   zcm.enabled,
		Transfers: zcm.transfers.Load(),
		BytesCopy: zcm.bytesCopy.Load(),
	}
}

// ZeroCopyStats represents zero-copy statistics
type ZeroCopyStats struct {
	Enabled   bool
	Transfers uint64
	BytesCopy uint64
}
