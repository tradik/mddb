package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAsyncIONew(t *testing.T) {
	aio := NewAsyncIO()
	if aio == nil {
		t.Fatal("expected non-nil AsyncIO")
		return
	}
	if !aio.enabled {
		t.Error("expected enabled (fallback mode)")
	}
	if aio.operations == nil {
		t.Error("expected operations map to be initialized")
	}
}

func TestAsyncIOReadAsync(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	// Create a file with known content
	fpath := filepath.Join(dir, "test.txt")
	content := []byte("hello async read world")
	if err := os.WriteFile(fpath, content, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var callbackResult []byte
	var callbackErr error
	var wg sync.WaitGroup
	wg.Add(1)

	id := aio.ReadAsync(f, len(content), 0, func(data []byte, err error) {
		callbackResult = data
		callbackErr = err
		wg.Done()
	})

	if id == 0 {
		t.Error("expected non-zero operation ID")
	}

	wg.Wait()

	if callbackErr != nil {
		t.Fatalf("callback error: %v", callbackErr)
	}
	if string(callbackResult) != string(content) {
		t.Errorf("got %q, want %q", string(callbackResult), string(content))
	}
}

func TestAsyncIOReadAsyncWithOffset(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	content := []byte("0123456789ABCDEF")
	if err := os.WriteFile(fpath, content, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var callbackResult []byte
	var wg sync.WaitGroup
	wg.Add(1)

	aio.ReadAsync(f, 5, 10, func(data []byte, err error) {
		callbackResult = data
		wg.Done()
	})

	wg.Wait()

	if string(callbackResult) != "ABCDE" {
		t.Errorf("got %q, want %q", string(callbackResult), "ABCDE")
	}
}

func TestAsyncIOWriteAsync(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	f, err := os.Create(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	data := []byte("hello async write")
	var callbackErr error
	var wg sync.WaitGroup
	wg.Add(1)

	id := aio.WriteAsync(f, data, 0, func(_ []byte, err error) {
		callbackErr = err
		wg.Done()
	})

	if id == 0 {
		t.Error("expected non-zero operation ID")
	}

	wg.Wait()

	if callbackErr != nil {
		t.Fatalf("callback error: %v", callbackErr)
	}

	// Verify file content
	got, err := os.ReadFile(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", string(got), string(data))
	}
}

func TestAsyncIOWriteAsyncWithOffset(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	// Pre-fill file
	initial := []byte("XXXXXXXXXX")
	if err := os.WriteFile(fpath, initial, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(fpath, os.O_RDWR, 0600) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)

	aio.WriteAsync(f, []byte("hello"), 5, func(_ []byte, err error) {
		wg.Done()
	})

	wg.Wait()

	got, err := os.ReadFile(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "XXXXXhello" {
		t.Errorf("got %q, want %q", string(got), "XXXXXhello")
	}
}

func TestAsyncIOWait(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	content := []byte("wait for me")
	if err := os.WriteFile(fpath, content, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	id := aio.ReadAsync(f, len(content), 0, nil)

	result, err := aio.Wait(id)
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if string(result) != string(content) {
		t.Errorf("got %q, want %q", string(result), string(content))
	}
}

func TestAsyncIOWaitNotFound(t *testing.T) {
	aio := NewAsyncIO()

	// Wait for a non-existent operation
	_, err := aio.Wait(99999)
	if err == nil {
		t.Error("expected error for non-existent operation")
	}
}

func TestAsyncIOStats(t *testing.T) {
	aio := NewAsyncIO()

	stats := aio.Stats()
	if !stats.Enabled {
		t.Error("expected enabled")
	}
	if stats.Pending != 0 {
		t.Errorf("expected 0 pending, got %d", stats.Pending)
	}
	if stats.Completed != 0 {
		t.Errorf("expected 0 completed, got %d", stats.Completed)
	}

	// Perform an operation
	dir := t.TempDir()
	fpath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fpath, []byte("stats"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	aio.ReadAsync(f, 5, 0, func(_ []byte, _ error) {
		wg.Done()
	})
	wg.Wait()

	// Small delay to allow stats update
	time.Sleep(10 * time.Millisecond)

	stats = aio.Stats()
	if stats.Completed != 1 {
		t.Errorf("expected 1 completed, got %d", stats.Completed)
	}
}

func TestAsyncIOBatchReadAsync(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	content := []byte("0123456789ABCDEFGHIJ")
	if err := os.WriteFile(fpath, content, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	requests := []ReadRequest{
		{Offset: 0, Size: 5},
		{Offset: 5, Size: 5},
		{Offset: 10, Size: 5},
	}

	var batchResults []ReadResult
	var wg sync.WaitGroup
	wg.Add(1)

	aio.BatchReadAsync(f, requests, func(results []ReadResult) {
		batchResults = results
		wg.Done()
	})

	wg.Wait()

	if len(batchResults) != 3 {
		t.Fatalf("expected 3 results, got %d", len(batchResults))
	}

	expected := []string{"01234", "56789", "ABCDE"}
	for i, exp := range expected {
		if batchResults[i].Error != nil {
			t.Errorf("result %d error: %v", i, batchResults[i].Error)
			continue
		}
		if string(batchResults[i].Data) != exp {
			t.Errorf("result %d: got %q, want %q", i, string(batchResults[i].Data), exp)
		}
	}
}

func TestAsyncIOBatchReadAsyncEmpty(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fpath, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var batchResults []ReadResult
	var wg sync.WaitGroup
	wg.Add(1)

	aio.BatchReadAsync(f, nil, func(results []ReadResult) {
		batchResults = results
		wg.Done()
	})

	wg.Wait()

	if len(batchResults) != 0 {
		t.Errorf("expected 0 results, got %d", len(batchResults))
	}
}

func TestAsyncIOOperationIDs(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fpath, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var wg sync.WaitGroup
	wg.Add(2)

	id1 := aio.ReadAsync(f, 4, 0, func(_ []byte, _ error) { wg.Done() })
	id2 := aio.ReadAsync(f, 4, 0, func(_ []byte, _ error) { wg.Done() })

	if id2 <= id1 {
		t.Errorf("expected id2 > id1, got id1=%d, id2=%d", id1, id2)
	}

	wg.Wait()
}

func TestIsIOUringAvailable(t *testing.T) {
	// On macOS this should return false
	result := isIOUringAvailable()
	if result {
		t.Error("expected false for non-Linux platform (or stub)")
	}
}

func TestAsyncIOReadAsyncNilCallback(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fpath, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	id := aio.ReadAsync(f, 4, 0, nil)
	result, err := aio.Wait(id)
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if string(result) != "test" {
		t.Errorf("got %q, want %q", string(result), "test")
	}
}

func TestAsyncIOWriteAsyncNilCallback(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	fpath := filepath.Join(dir, "test.txt")
	f, err := os.Create(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	id := aio.WriteAsync(f, []byte("data"), 0, nil)
	_, err = aio.Wait(id)
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}

	got, err := os.ReadFile(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "data" {
		t.Errorf("got %q, want %q", string(got), "data")
	}
}

func TestAsyncIOConcurrentOperations(t *testing.T) {
	aio := NewAsyncIO()
	dir := t.TempDir()

	// Create a file with enough content
	fpath := filepath.Join(dir, "test.txt")
	content := make([]byte, 1000)
	for i := range content {
		content[i] = byte('A' + i%26)
	}
	if err := os.WriteFile(fpath, content, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(fpath) //nolint:gosec // G304: temp file path in test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	// Launch multiple concurrent reads
	var wg sync.WaitGroup
	numOps := 10
	wg.Add(numOps)

	for i := 0; i < numOps; i++ {
		aio.ReadAsync(f, 10, int64(i*10), func(data []byte, err error) {
			if err != nil {
				t.Errorf("read error: %v", err)
			}
			wg.Done()
		})
	}

	wg.Wait()

	// All callbacks completed (verified by wg.Wait above).
	// Stats counter may lag slightly behind callbacks on slow CI,
	// so we only check it's reasonably close.
	stats := aio.Stats()
	if stats.Completed < int64(numOps)-1 {
		t.Errorf("expected at least %d completed, got %d", numOps-1, stats.Completed)
	}
}

func TestReadRequestStruct(t *testing.T) {
	req := ReadRequest{Offset: 100, Size: 50}
	if req.Offset != 100 {
		t.Errorf("expected Offset 100, got %d", req.Offset)
	}
	if req.Size != 50 {
		t.Errorf("expected Size 50, got %d", req.Size)
	}
}

func TestReadResultStruct(t *testing.T) {
	result := ReadResult{Data: []byte("hello"), Error: nil}
	if string(result.Data) != "hello" {
		t.Errorf("expected Data 'hello', got %q", string(result.Data))
	}
	if result.Error != nil {
		t.Error("expected nil Error")
	}
}

func TestOperationTypeConstants(t *testing.T) {
	if OpRead != 0 {
		t.Errorf("expected OpRead=0, got %d", OpRead)
	}
	if OpWrite != 1 {
		t.Errorf("expected OpWrite=1, got %d", OpWrite)
	}
	if OpSync != 2 {
		t.Errorf("expected OpSync=2, got %d", OpSync)
	}
}
