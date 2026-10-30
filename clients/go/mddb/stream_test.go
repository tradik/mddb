package mddb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// DoStream sends a body as it is read and hands the response back unread
// (AGT-005: corpus packs are too large to buffer).

func TestDoStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/fail":
			http.Error(w, "refused", http.StatusConflict)
		case r.Header.Get("X-API-Key") != "k":
			http.Error(w, "no key", http.StatusUnauthorized)
		default:
			_, _ = w.Write([]byte(r.Header.Get("Content-Type") + ":" + string(body)))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, WithAPIKey("k"))

	rc, err := c.DoStream(context.Background(), http.MethodPost, "/up", "application/zstd", strings.NewReader("bytes"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "application/zstd:bytes" {
		t.Fatalf("got %q", got)
	}
	rc, err = c.DoStream(context.Background(), http.MethodGet, "/down", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()

	_, err = c.DoStream(context.Background(), http.MethodPost, "/fail", "", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || !strings.Contains(apiErr.Body, "refused") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.DoStream(context.Background(), "BAD METHOD", "/x", "", nil); err == nil {
		t.Fatal("an invalid method was sent")
	}
	if _, err := New("http://127.0.0.1:1").DoStream(context.Background(), http.MethodGet, "/x", "", nil); err == nil {
		t.Fatal("a refused connection was not reported")
	}
}
