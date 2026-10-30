package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every branch of the /v1/automation handlers: no manager, a refused caller,
// read-only mode, bodies that do not decode, failing writes, the cron
// scheduler reloads and a trigger whose search cannot run.

// callAutomation sends method path body to the list or the detail handler.
func callAutomation(s *Server, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if path == "/v1/automation" {
		s.handleAutomation(w, r)
	} else {
		s.handleAutomationDetail(w, r)
	}
	return w
}

func TestAutomationHandlersRefusals(t *testing.T) {
	s, _ := runNowServer(t)
	manager := s.AutomationManager
	s.AutomationManager = nil
	for _, p := range []string{"/v1/automation", "/v1/automation/x"} {
		if w := callAutomation(s, http.MethodGet, p, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s without a manager: %d", p, w.Code)
		}
	}
	s.AutomationManager = manager

	covEDenyAll(t, s)
	for _, p := range []string{"/v1/automation", "/v1/automation/x"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, p, nil).WithContext(context.Background())
		if p == "/v1/automation" {
			s.handleAutomation(w, r)
		} else {
			s.handleAutomationDetail(w, r)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("%s for an anonymous caller: %d", p, w.Code)
		}
	}
	s.AuthManager = nil

	s.Mode = ModeRead
	for _, c := range [][2]string{{http.MethodPost, "/v1/automation"}, {http.MethodPut, "/v1/automation/x"}, {http.MethodDelete, "/v1/automation/x"}} {
		if w := callAutomation(s, c[0], c[1], "{}"); w.Code != http.StatusForbidden {
			t.Errorf("%s %s read-only: %d", c[0], c[1], w.Code)
		}
	}
	s.Mode = ModeRW

	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/automation", "{"},
		{http.MethodPost, "/v1/automation", `{"type":"bogus","name":"x"}`},
		{http.MethodPut, "/v1/automation/x", "{"},
		{http.MethodPut, "/v1/automation/missing", `{"name":"x"}`},
		{http.MethodGet, "/v1/automation/", ""},
	} {
		if w := callAutomation(s, c.method, c.path, c.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s %s %q: %d", c.method, c.path, c.body, w.Code)
		}
	}
	for _, c := range [][2]string{{http.MethodPatch, "/v1/automation"}, {http.MethodPatch, "/v1/automation/x"}, {http.MethodGet, "/v1/automation/x/test"}} {
		if w := callAutomation(s, c[0], c[1], ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", c[0], c[1], w.Code)
		}
	}
	for _, c := range [][2]string{{http.MethodGet, "/v1/automation/missing"}, {http.MethodDelete, "/v1/automation/missing"}, {http.MethodPost, "/v1/automation/missing/test"}} {
		if w := callAutomation(s, c[0], c[1], ""); w.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d", c[0], c[1], w.Code)
		}
	}
}

func TestAutomationHandlersCronLifecycle(t *testing.T) {
	s, _ := runNowServer(t)
	w := callAutomation(s, http.MethodPost, "/v1/automation",
		`{"type":"cron","name":"nightly","schedule":"0 3 * * *","enabled":true,"backup":{}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	id := s.AutomationManager.List("cron")[0].ID
	if len(s.CronScheduler.entryMap) != 1 {
		t.Errorf("create did not reload the scheduler: %d entries", len(s.CronScheduler.entryMap))
	}
	if w := callAutomation(s, http.MethodGet, "/v1/automation/"+id, ""); w.Code != http.StatusOK {
		t.Errorf("get: %d", w.Code)
	}
	if w := callAutomation(s, http.MethodPut, "/v1/automation/"+id, `{"name":"nightly","schedule":"0 4 * * *","enabled":false}`); w.Code != http.StatusOK {
		t.Errorf("update: %d %s", w.Code, w.Body)
	}
	if len(s.CronScheduler.entryMap) != 0 {
		t.Error("disabling did not reload the scheduler")
	}
	if w := callAutomation(s, http.MethodPost, "/v1/automation/"+id+"/test", ""); w.Code != http.StatusBadRequest {
		t.Errorf("testing a cron: %d", w.Code)
	}

	hook, _ := s.AutomationManager.Create(AutomationRule{Type: "webhook", Name: "h", URL: "https://example.com"})
	trig, err := s.AutomationManager.Create(AutomationRule{Type: "trigger", Name: "t", Collection: "c", WebhookID: hook.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.AutomationManager.mu.Lock()
	for i := range s.AutomationManager.rules {
		if s.AutomationManager.rules[i].ID == trig.ID {
			s.AutomationManager.rules[i].Query, s.AutomationManager.rules[i].SearchType = "q", "bogus"
		}
	}
	s.AutomationManager.mu.Unlock()
	if w := callAutomation(s, http.MethodPost, "/v1/automation/"+trig.ID+"/test", ""); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "bogus") {
		t.Errorf("a trigger whose search cannot run: %d %s", w.Code, w.Body)
	}

	if w := callAutomation(s, http.MethodDelete, "/v1/automation/"+id, ""); w.Code != http.StatusOK {
		t.Errorf("delete: %d", w.Code)
	}
	// A database that fails the write.
	_ = s.DB.Close()
	if w := callAutomation(s, http.MethodDelete, "/v1/automation/"+hook.ID, ""); w.Code != http.StatusBadRequest {
		t.Errorf("a failing delete: %d", w.Code)
	}
}
