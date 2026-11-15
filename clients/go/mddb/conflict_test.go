package mddb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// conflictServer answers every request with status and body, and records the
// last request body.
func conflictServer(t *testing.T, status int, body string, got *string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readBody(r)
		*got = r.URL.Path + " " + string(b)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// A create with IfAbsent that finds the document is a 409 whose conflict
// carries the current revision (#423).
func TestAddIfAbsentConflict(t *testing.T) {
	var got string
	c := conflictServer(t, http.StatusConflict, `{"error":"revision conflict","expectedRevision":"","currentRevision":"abc"}`, &got)
	_, err := c.Add(context.Background(), AddRequest{Collection: "a", Key: "k", Lang: "en", ContentMD: "x", IfAbsent: true})
	if !strings.Contains(got, `"ifAbsent":true`) || strings.Contains(got, "expectedRevision") {
		t.Fatalf("request: %s", got)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	conflict, ok := apiErr.RevisionConflict()
	if !ok || conflict.Current != "abc" || conflict.Expected != "" {
		t.Fatalf("conflict: %+v %v", conflict, ok)
	}
}

// Delete sends the expected revision and reports a lost delete the same way
// (#424); without one it sends none.
func TestDeleteConditional(t *testing.T) {
	var got string
	c := conflictServer(t, http.StatusConflict, `{"error":"revision conflict","expectedRevision":"old","currentRevision":"new"}`, &got)
	err := c.Delete(context.Background(), DeleteRequest{Collection: "a", Key: "k", Lang: "en", ExpectedRevision: "old"})
	if got != `/v1/delete {"collection":"a","key":"k","lang":"en","expectedRevision":"old"}` {
		t.Fatalf("request: %s", got)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if conflict, ok := apiErr.RevisionConflict(); !ok || conflict.Expected != "old" || conflict.Current != "new" {
		t.Fatalf("conflict: %+v %v", conflict, ok)
	}

	ok := conflictServer(t, http.StatusOK, `{"status":"deleted"}`, &got)
	if err := ok.Delete(context.Background(), DeleteRequest{Collection: "a", Key: "k", Lang: "en"}); err != nil || strings.Contains(got, "expectedRevision") {
		t.Fatalf("unconditional delete: %v %s", err, got)
	}
}

// Search results carry the revision a get returns (#425).
func TestSearchRevision(t *testing.T) {
	var got string
	c := conflictServer(t, http.StatusOK, `[{"id":"a|k|en","key":"k","revision":"r1"}]`, &got)
	docs, err := c.Search(context.Background(), SearchRequest{Collection: "a"})
	if err != nil || len(docs) != 1 || docs[0].Revision != "r1" {
		t.Fatalf("search: %v %+v", err, docs)
	}
}

// RevisionConflict is false for anything that is not a revision conflict.
func TestRevisionConflictOtherErrors(t *testing.T) {
	for _, e := range []*APIError{
		{StatusCode: http.StatusNotFound, Body: `{"error":"document not found"}`},
		{StatusCode: http.StatusConflict, Body: `not json`},
		{StatusCode: http.StatusConflict, Body: `{"error":"version conflict"}`},
	} {
		if c, ok := e.RevisionConflict(); ok || c != nil {
			t.Errorf("%+v: reported a revision conflict", e)
		}
	}
}
