package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// AsyncIO provides async I/O operations
// Note: This is a simplified implementation
// Full io_uring requires Linux 5.1+ and C bindings
type AsyncIO struct {
	enabled    bool
	pending    atomic.Int64
	completed  atomic.Int64
	mu         sync.Mutex
	operations map[uint64]*AsyncOperation
	nextID     atomic.Uint64
}

// AsyncOperation represents an async I/O operation
type AsyncOperation struct {
	ID       uint64
	Type     OperationType
	File     *os.File
	Buffer   []byte
	Offset   int64
	Callback func([]byte, error)
	Done     chan struct{}
	Result   []byte
	Error    error
}

// OperationType defines the type of I/O operation
type OperationType int

// I/O operation type constants.
const (
	OpRead OperationType = iota
	OpWrite
	OpSync
)

// NewAsyncIO creates a new async I/O manager
func NewAsyncIO() *AsyncIO {
	aio := &AsyncIO{
		enabled:    isIOUringAvailable(),
		operations: make(map[uint64]*AsyncOperation),
	}

	if !aio.enabled {
		// Fallback to goroutine-based async I/O
		aio.enabled = true // Enable fallback mode
	}

	return aio
}

// ReadAsync performs an async read operation
func (aio *AsyncIO) ReadAsync(file *os.File, size int, offset int64, callback func([]byte, error)) uint64 {
	op := &AsyncOperation{
		ID:       aio.nextID.Add(1),
		Type:     OpRead,
		File:     file,
		Buffer:   make([]byte, size),
		Offset:   offset,
		Callback: callback,
		Done:     make(chan struct{}),
	}

	aio.mu.Lock()
	aio.operations[op.ID] = op
	aio.mu.Unlock()

	aio.pending.Add(1)

	// Submit operation
	go aio.executeOperation(op)

	return op.ID
}

// WriteAsync performs an async write operation
func (aio *AsyncIO) WriteAsync(file *os.File, data []byte, offset int64, callback func([]byte, error)) uint64 {
	op := &AsyncOperation{
		ID:       aio.nextID.Add(1),
		Type:     OpWrite,
		File:     file,
		Buffer:   data,
		Offset:   offset,
		Callback: callback,
		Done:     make(chan struct{}),
	}

	aio.mu.Lock()
	aio.operations[op.ID] = op
	aio.mu.Unlock()

	aio.pending.Add(1)

	// Submit operation
	go aio.executeOperation(op)

	return op.ID
}

// executeOperation executes an I/O operation
func (aio *AsyncIO) executeOperation(op *AsyncOperation) {
	defer func() {
		aio.pending.Add(-1)
		aio.completed.Add(1)
		close(op.Done)

		// Only clean up callback-based operations here.
		// Wait()-based operations are cleaned up by Wait() itself,
		// avoiding a race where the operation is deleted before Wait() can find it.
		if op.Callback != nil {
			aio.mu.Lock()
			delete(aio.operations, op.ID)
			aio.mu.Unlock()
		}
	}()

	switch op.Type {
	case OpRead:
		n, err := op.File.ReadAt(op.Buffer, op.Offset)
		if err != nil && err.Error() != "EOF" {
			op.Error = err
		} else {
			op.Result = op.Buffer[:n]
		}

	case OpWrite:
		_, err := op.File.WriteAt(op.Buffer, op.Offset)
		op.Error = err
		op.Result = op.Buffer

	case OpSync:
		op.Error = op.File.Sync()
	}

	// Call callback if provided
	if op.Callback != nil {
		op.Callback(op.Result, op.Error)
	}
}

// Wait waits for an operation to complete
func (aio *AsyncIO) Wait(id uint64) ([]byte, error) {
	aio.mu.Lock()
	op, exists := aio.operations[id]
	aio.mu.Unlock()

	if !exists {
		return nil, fmt.Errorf("operation not found: %d", id)
	}

	<-op.Done

	// Clean up the operation from the map
	aio.mu.Lock()
	delete(aio.operations, op.ID)
	aio.mu.Unlock()

	return op.Result, op.Error
}

// WaitAll waits for all pending operations
func (aio *AsyncIO) WaitAll() {
	for aio.pending.Load() > 0 {
		// Small sleep to avoid busy wait
		// In production with real io_uring, this would be event-driven
	}
}

// Stats returns async I/O statistics
func (aio *AsyncIO) Stats() AsyncIOStats {
	return AsyncIOStats{
		Enabled:   aio.enabled,
		Pending:   aio.pending.Load(),
		Completed: aio.completed.Load(),
	}
}

// AsyncIOStats represents async I/O statistics
type AsyncIOStats struct {
	Enabled   bool
	Pending   int64
	Completed int64
}

// isIOUringAvailable checks if io_uring is available
func isIOUringAvailable() bool {
	// Simplified check - in production, check for actual io_uring support
	// This would require CGO and Linux-specific headers
	// For now, return false to use fallback goroutine-based async I/O
	return false
}

// BatchReadAsync performs multiple async reads
func (aio *AsyncIO) BatchReadAsync(file *os.File, requests []ReadRequest, callback func([]ReadResult)) {
	results := make([]ReadResult, len(requests))
	var wg sync.WaitGroup

	for i, req := range requests {
		wg.Add(1)
		idx := i
		request := req

		aio.ReadAsync(file, request.Size, request.Offset, func(data []byte, err error) {
			results[idx] = ReadResult{
				Data:  data,
				Error: err,
			}
			wg.Done()
		})
	}

	// Wait for all and callback
	go func() {
		wg.Wait()
		if callback != nil {
			callback(results)
		}
	}()
}

// ReadRequest represents a read request
type ReadRequest struct {
	Offset int64
	Size   int
}

// ReadResult represents a read result
type ReadResult struct {
	Data  []byte
	Error error
}
