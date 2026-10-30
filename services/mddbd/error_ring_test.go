package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	bolterrors "go.etcd.io/bbolt/errors"
)

// OPS-025: the error ring keeps the class and the line, never the message.

func TestErrorRingKeepsTheLastEntriesOldestFirst(t *testing.T) {
	var r errorRing
	if entries, total := r.snapshot(); len(entries) != 0 || total != 0 {
		t.Fatalf("empty ring: %v %d", entries, total)
	}
	for i := range errorRingSize + 5 {
		r.record(fmt.Errorf("item %d: %w", i, errNotFound), 0)
	}
	entries, total := r.snapshot()
	if total != errorRingSize+5 || len(entries) != errorRingSize {
		t.Fatalf("%d entries of %d", len(entries), total)
	}
	if !entries[0].At.Before(entries[len(entries)-1].At) && !entries[0].At.Equal(entries[len(entries)-1].At) {
		t.Error("entries are not oldest first")
	}
	if e := entries[0]; e.Class != "not_found" || !strings.HasPrefix(e.Site, "error_ring_test.go:") {
		t.Errorf("entry %+v", e)
	}
	r.record(errors.New("x"), 99) // no such frame
	if entries, _ := r.snapshot(); entries[len(entries)-1].Site != "unknown" {
		t.Errorf("deep frame: %+v", entries[len(entries)-1])
	}
}

func TestErrorClassNamesWithoutMessages(t *testing.T) {
	var syntax *json.SyntaxError
	jsonErr := json.Unmarshal([]byte("{secret"), &struct{}{})
	if !errors.As(jsonErr, &syntax) {
		t.Fatal("no syntax error to classify")
	}
	for err, want := range map[error]string{
		nil:                                         "nil",
		fmt.Errorf("a: %w", context.Canceled):       "context_canceled",
		os.ErrNotExist:                              "file_not_found",
		bolterrors.ErrDatabaseNotOpen:               "database_not_open",
		jsonErr:                                     "json",
		&http.MaxBytesError{Limit: 1}:               "body_too_large",
		&QuotaError{Resource: "documents"}:          "quota",
		&ReadOnlyError{Collection: "secret"}:        "read_only",
		errors.New("secret detail"):                 "*errors.errorString",
		fmt.Errorf("wrapping %w", io.ErrNoProgress): "*errors.errorString",
	} {
		if got := errorClass(err); got != want {
			t.Errorf("errorClass(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestBadRecordsItsCaller(t *testing.T) {
	_, before := diagErrors.snapshot()
	bad(httptest.NewRecorder(), errors.New("the caller's secret input"))
	entries, after := diagErrors.snapshot()
	if after != before+1 || !strings.HasPrefix(entries[len(entries)-1].Site, "error_ring_test.go:") {
		t.Fatalf("bad() recorded %+v (%d → %d)", entries[len(entries)-1], before, after)
	}
}
