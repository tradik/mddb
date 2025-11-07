package main

import (
	"sync"
)

// GlobalBufferPool provides pre-allocated buffers for various operations
var GlobalBufferPool = NewBufferPoolManager()

// BufferPoolManager manages multiple buffer pools of different sizes
type BufferPoolManager struct {
	small  *sync.Pool // 1KB buffers
	medium *sync.Pool // 4KB buffers
	large  *sync.Pool // 16KB buffers
	xlarge *sync.Pool // 64KB buffers
}

// NewBufferPoolManager creates a new buffer pool manager
func NewBufferPoolManager() *BufferPoolManager {
	return &BufferPoolManager{
		small: &sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 1024) // 1KB
				return &buf
			},
		},
		medium: &sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 4096) // 4KB
				return &buf
			},
		},
		large: &sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 16384) // 16KB
				return &buf
			},
		},
		xlarge: &sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 65536) // 64KB
				return &buf
			},
		},
	}
}

// Get returns a buffer of appropriate size
func (bpm *BufferPoolManager) Get(size int) []byte {
	var bufPtr *[]byte
	
	switch {
	case size <= 1024:
		bufPtr = bpm.small.Get().(*[]byte)
	case size <= 4096:
		bufPtr = bpm.medium.Get().(*[]byte)
	case size <= 16384:
		bufPtr = bpm.large.Get().(*[]byte)
	case size <= 65536:
		bufPtr = bpm.xlarge.Get().(*[]byte)
	default:
		// Too large for pool, allocate directly
		buf := make([]byte, size)
		return buf
	}
	
	// Return slice of requested size
	return (*bufPtr)[:size]
}

// Put returns a buffer to the pool
func (bpm *BufferPoolManager) Put(buf []byte) {
	if buf == nil {
		return
	}
	
	capacity := cap(buf)
	
	// Reset length to capacity
	buf = buf[:capacity]
	
	// Return to appropriate pool
	switch capacity {
	case 1024:
		bpm.small.Put(&buf)
	case 4096:
		bpm.medium.Put(&buf)
	case 16384:
		bpm.large.Put(&buf)
	case 65536:
		bpm.xlarge.Put(&buf)
	// Don't pool buffers of other sizes
	}
}

// GetSmall returns a 1KB buffer
func (bpm *BufferPoolManager) GetSmall() []byte {
	bufPtr := bpm.small.Get().(*[]byte)
	return (*bufPtr)[:1024]
}

// GetMedium returns a 4KB buffer
func (bpm *BufferPoolManager) GetMedium() []byte {
	bufPtr := bpm.medium.Get().(*[]byte)
	return (*bufPtr)[:4096]
}

// GetLarge returns a 16KB buffer
func (bpm *BufferPoolManager) GetLarge() []byte {
	bufPtr := bpm.large.Get().(*[]byte)
	return (*bufPtr)[:16384]
}

// GetXLarge returns a 64KB buffer
func (bpm *BufferPoolManager) GetXLarge() []byte {
	bufPtr := bpm.xlarge.Get().(*[]byte)
	return (*bufPtr)[:65536]
}

// SlicePool provides pooled slices for various types
type SlicePool struct {
	stringSlices sync.Pool
	byteSlices   sync.Pool
}

// NewSlicePool creates a new slice pool
func NewSlicePool() *SlicePool {
	return &SlicePool{
		stringSlices: sync.Pool{
			New: func() interface{} {
				s := make([]string, 0, 100)
				return &s
			},
		},
		byteSlices: sync.Pool{
			New: func() interface{} {
				s := make([][]byte, 0, 100)
				return &s
			},
		},
	}
}

// GetStringSlice returns a pooled string slice
func (sp *SlicePool) GetStringSlice() []string {
	slicePtr := sp.stringSlices.Get().(*[]string)
	*slicePtr = (*slicePtr)[:0] // Reset length
	return *slicePtr
}

// PutStringSlice returns a string slice to pool
func (sp *SlicePool) PutStringSlice(s []string) {
	if cap(s) <= 100 {
		sp.stringSlices.Put(&s)
	}
}

// GetByteSlice returns a pooled byte slice
func (sp *SlicePool) GetByteSlice() [][]byte {
	slicePtr := sp.byteSlices.Get().(*[][]byte)
	*slicePtr = (*slicePtr)[:0] // Reset length
	return *slicePtr
}

// PutByteSlice returns a byte slice to pool
func (sp *SlicePool) PutByteSlice(s [][]byte) {
	if cap(s) <= 100 {
		sp.byteSlices.Put(&s)
	}
}

// Global slice pool
var GlobalSlicePool = NewSlicePool()
