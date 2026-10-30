package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	bolterrors "go.etcd.io/bbolt/errors"
)

// The last errors the server answered, for the diagnose bundle (OPS-025).
//
// An entry is the error's class and the line that reported it — never its
// message. Messages wrap paths, document IDs, collection names and the
// caller's own input ("unknown field \"secret\""), which is exactly what the
// bundle promises not to carry; the class and the line are what a maintainer
// needs to find the code.

// errorRingSize is how many errors the ring keeps.
const errorRingSize = 200

// errorEntry is one recorded error.
type errorEntry struct {
	At    time.Time `json:"at"`
	Class string    `json:"class"`
	Site  string    `json:"site"` // file:line of the code that reported it
}

// errorRing keeps the last errorRingSize errors.
type errorRing struct {
	mu    sync.Mutex
	buf   [errorRingSize]errorEntry
	next  int
	total uint64
}

// diagErrors is the server's ring: errors are reported from package-level
// helpers (bad) that have no server at hand.
var diagErrors errorRing

// record adds err, reported by the function skip frames above record.
func (e *errorRing) record(err error, skip int) {
	site := "unknown"
	if _, file, line, ok := runtime.Caller(skip + 1); ok {
		site = filepath.Base(file) + ":" + strconv.Itoa(line)
	}
	entry := errorEntry{At: time.Now().UTC(), Class: errorClass(err), Site: site}
	e.mu.Lock()
	e.buf[e.next] = entry
	e.next = (e.next + 1) % errorRingSize
	e.total++
	e.mu.Unlock()
}

// snapshot returns the kept entries, oldest first, and how many errors were
// recorded in all.
func (e *errorRing) snapshot() ([]errorEntry, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := min(e.total, errorRingSize)
	out := make([]errorEntry, 0, n)
	start := (e.next - int(n) + errorRingSize) % errorRingSize
	for i := range int(n) {
		out = append(out, e.buf[(start+i)%errorRingSize])
	}
	return out, e.total
}

// errorClasses name the errors worth telling apart, by errors.Is.
var errorClasses = []struct {
	err  error
	name string
}{
	{errNotFound, "not_found"},
	{context.Canceled, "context_canceled"},
	{context.DeadlineExceeded, "deadline_exceeded"},
	{io.EOF, "eof"},
	{io.ErrUnexpectedEOF, "unexpected_eof"},
	{os.ErrNotExist, "file_not_found"},
	{os.ErrPermission, "permission"},
	{bolterrors.ErrDatabaseNotOpen, "database_not_open"},
	{bolterrors.ErrTimeout, "database_timeout"},
	{errFreelistLoading, "freelist_loading"},
	{errNoEmbeddingProvider, "no_embedding_provider"},
	{errEmbedQuery, "embed_query"},
	{errGraphDocNotFound, "graph_doc_not_found"},
}

// errorClass names err without its message: a known sentinel's name, a
// known structured error's kind, or the Go type of the innermost error.
func errorClass(err error) string {
	if err == nil {
		return "nil"
	}
	for _, c := range errorClasses {
		if errors.Is(err, c.err) {
			return c.name
		}
	}
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &syntax), errors.As(err, &typ):
		return "json"
	case errors.As(err, &tooLarge):
		return "body_too_large"
	case asQuotaError(err) != nil:
		return "quota"
	case asReadOnlyError(err) != nil:
		return "read_only"
	}
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return fmt.Sprintf("%T", err)
		}
		err = inner
	}
}
