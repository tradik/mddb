package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// #329: /health answers during startup with the phase in progress, before
// the server exists, and hands over to the server once it is ready.
func TestHealthDuringStartupNamesThePhase(t *testing.T) {
	c := newStartupClock()
	c.begin("opening database")

	rec := httptest.NewRecorder()
	c.healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d during startup, want 503", rec.Code)
	}
	var body struct {
		Status string `json:"status"`
		Phase  string `json:"phase"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "warming_up" || body.Phase != "opening database" {
		t.Errorf("health during startup = %+v", body)
	}

	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.Ready = false
	c.srv.Store(s)
	c.begin("loading")
	rec = httptest.NewRecorder()
	c.healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a server that is not ready answered %d", rec.Code)
	}

	s.Ready = true
	c.done()
	rec = httptest.NewRecorder()
	c.healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("a ready server answered %d, want 200", rec.Code)
	}
}

func TestEarlyHealthListens(t *testing.T) {
	c := newStartupClock()
	c.begin("opening database")
	srv := c.startEarlyHealth("127.0.0.1:0")
	defer func() { _ = srv.Close() }()
	if srv.Handler == nil {
		t.Fatal("no handler")
	}
}
