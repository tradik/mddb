package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"mddb/internal/automationlog"
	"mddb/internal/spell"
	"mddb/internal/temporal"
)

// The catalogue lists a switchable subsystem's endpoints exactly when the
// subsystem is on, and the corpus endpoints always (AGT-005).

func TestCatalogueFollowsTheSubsystems(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	paths := func() map[string]bool {
		out := map[string]bool{}
		for _, ep := range s.httpEndpointCatalogue(false) {
			out[ep.Path] = true
		}
		return out
	}
	off := paths()
	for _, p := range []string{"/v1/automation", "/v1/automation-logs", "/v1/temporal/hot", "/v1/spell-suggest"} {
		if off[p] {
			t.Errorf("%s listed with its subsystem off", p)
		}
	}
	for _, p := range []string{"/v1/corpus", "/v1/corpus/build", "/v1/corpus/install", "/v1/corpus/remove"} {
		if !off[p] {
			t.Errorf("%s is not listed", p)
		}
	}

	s.AutomationManager = NewAutomationManager(s.DB)
	s.AutomationLogStore = automationlog.NewStore(s.DB, 0)
	s.TemporalManager = temporal.NewTemporalManager(s.DB)
	s.SpellManager = spell.NewSpellManager(s.DB)
	on := paths()
	for _, p := range []string{"/v1/automation", "/v1/automation-logs", "/v1/temporal/hot", "/v1/spell-suggest"} {
		if !on[p] {
			t.Errorf("%s not listed with its subsystem on", p)
		}
	}
}

// brokenResponse is a response whose body cannot be written.
type brokenResponse struct{ *httptest.ResponseRecorder }

func (brokenResponse) Write([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestEndpointsReportsAnUnwritableAnswer(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	w := brokenResponse{httptest.NewRecorder()}
	s.handleEndpoints(w, httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 once the body cannot be written", w.Code)
	}
}
