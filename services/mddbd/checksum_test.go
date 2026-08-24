package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestHandleChecksum(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	t.Run("empty collection returns zero checksum", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/checksum?collection=empty", nil)
		w := httptest.NewRecorder()
		s.handleChecksum(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Collection    string `json:"collection"`
			Checksum      string `json:"checksum"`
			DocumentCount int    `json:"documentCount"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Checksum != "00000000" {
			t.Fatalf("expected zero checksum, got %s", resp.Checksum)
		}
		if resp.DocumentCount != 0 {
			t.Fatalf("expected 0 docs, got %d", resp.DocumentCount)
		}
	})

	// Add a document
	addDocChecksum(t, s, `{"collection":"blog","key":"p1","lang":"en","contentMd":"hello world","meta":{"tag":["go"]}}`)

	var checksumAfterAdd string
	t.Run("checksum after add", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/checksum?collection=blog", nil)
		w := httptest.NewRecorder()
		s.handleChecksum(w, req)

		var resp struct {
			Checksum      string `json:"checksum"`
			DocumentCount int    `json:"documentCount"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Checksum == "00000000" {
			t.Fatal("checksum should not be zero after adding a doc")
		}
		if resp.DocumentCount != 1 {
			t.Fatalf("expected 1 doc, got %d", resp.DocumentCount)
		}
		checksumAfterAdd = resp.Checksum
	})

	// Add another document — checksum should change
	addDocChecksum(t, s, `{"collection":"blog","key":"p2","lang":"en","contentMd":"second doc","meta":{"tag":["rust"]}}`)

	t.Run("checksum changes on add", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/checksum?collection=blog", nil)
		w := httptest.NewRecorder()
		s.handleChecksum(w, req)

		var resp struct {
			Checksum      string `json:"checksum"`
			DocumentCount int    `json:"documentCount"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Checksum == checksumAfterAdd {
			t.Fatal("checksum should change after adding another doc")
		}
		if resp.DocumentCount != 2 {
			t.Fatalf("expected 2 docs, got %d", resp.DocumentCount)
		}
	})

	// Different collection has different checksum
	t.Run("different collections have independent checksums", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/checksum?collection=other", nil)
		w := httptest.NewRecorder()
		s.handleChecksum(w, req)

		var resp struct {
			Checksum      string `json:"checksum"`
			DocumentCount int    `json:"documentCount"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Checksum != "00000000" {
			t.Fatalf("other collection should have zero checksum, got %s", resp.Checksum)
		}
	})

	t.Run("missing collection returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/checksum", nil)
		w := httptest.NewRecorder()
		s.handleChecksum(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("wrong method returns 405", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/checksum?collection=blog", nil)
		w := httptest.NewRecorder()
		s.handleChecksum(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", w.Code)
		}
	})

	// Stats endpoint includes checksum
	t.Run("stats includes checksum", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/stats", nil)
		w := httptest.NewRecorder()
		s.handleStats(w, req)

		var resp struct {
			Collections []struct {
				Name     string `json:"name"`
				Checksum string `json:"checksum"`
			} `json:"collections"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range resp.Collections {
			if c.Name == "blog" {
				found = true
				if c.Checksum == "" || c.Checksum == "00000000" {
					t.Fatalf("blog collection should have non-zero checksum in stats, got %s", c.Checksum)
				}
			}
		}
		if !found {
			t.Fatal("blog collection not found in stats")
		}
	})
}

func addDocChecksum(t *testing.T, s *Server, body string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/add", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleAdd(w, req)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("addDoc failed: %d %s", w.Code, w.Body.String())
	}
}
