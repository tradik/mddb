package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// automationAllowed answers the request itself and returns false when
// automations are off or the caller is not a global administrator.
func (s *Server) automationAllowed(w http.ResponseWriter, r *http.Request) bool {
	if s.AutomationManager == nil {
		bad(w, fmt.Errorf("automation not initialized"))
		return false
	}
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return false
		}
	}
	return true
}

// automationWritable answers 403 and returns false in read-only mode.
func (s *Server) automationWritable(w http.ResponseWriter) bool {
	if s.Mode == ModeRead {
		http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
		return false
	}
	return true
}

// reloadCrons reloads the cron scheduler after a write to a cron rule.
func (s *Server) reloadCrons(ruleType string) {
	if ruleType == "cron" && s.CronScheduler != nil {
		s.CronScheduler.Reload()
	}
}

// handleAutomation dispatches GET (list) and POST (create) for /v1/automation.
func (s *Server) handleAutomation(w http.ResponseWriter, r *http.Request) {
	if !s.automationAllowed(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.Metrics.IncOp("automation_list", "")
		q := r.URL.Query()
		rules := s.automationRules(q.Get("type"), q.Get("collection"))
		ok(w, map[string]interface{}{
			"rules": rules,
			"total": len(rules),
		})
	case http.MethodPost:
		s.automationCreate(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// automationCreate handles POST /v1/automation.
func (s *Server) automationCreate(w http.ResponseWriter, r *http.Request) {
	if !s.automationWritable(w) {
		return
	}
	s.Metrics.IncOp("automation_create", "")
	var rule AutomationRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		bad(w, err)
		return
	}
	rule.LastResult = nil // read from the log, never stored
	created, err := s.AutomationManager.Create(rule)
	if err != nil {
		bad(w, err)
		return
	}
	s.reloadCrons(created.Type)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

// handleAutomationDetail dispatches GET/PUT/DELETE for /v1/automation/{id}
// and POST for /v1/automation/{id}/test and /v1/automation/{id}/run.
func (s *Server) handleAutomationDetail(w http.ResponseWriter, r *http.Request) {
	if !s.automationAllowed(w, r) {
		return
	}
	// Parse path: /v1/automation/{id}, /v1/automation/{id}/test or /run
	id, verb, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/automation/"), "/")
	switch {
	case id == "":
		bad(w, fmt.Errorf("missing rule id"))
	case verb == "test":
		s.handleAutomationTest(w, r, id)
	case verb == "run":
		s.handleAutomationRun(w, r, id)
	case r.Method == http.MethodGet:
		s.automationGet(w, id)
	case r.Method == http.MethodPut:
		s.automationUpdate(w, r, id)
	case r.Method == http.MethodDelete:
		s.automationDelete(w, id)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// automationGet handles GET /v1/automation/{id}.
func (s *Server) automationGet(w http.ResponseWriter, id string) {
	s.Metrics.IncOp("automation_get", id)
	rule := s.AutomationManager.Get(id)
	if rule == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	ok(w, rule)
}

// automationUpdate handles PUT /v1/automation/{id}.
func (s *Server) automationUpdate(w http.ResponseWriter, r *http.Request, id string) {
	if !s.automationWritable(w) {
		return
	}
	var update AutomationRule
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		bad(w, err)
		return
	}
	update.LastResult = nil
	s.Metrics.IncOp("automation_update", id)
	updated, err := s.AutomationManager.Update(id, update)
	if err != nil {
		bad(w, err)
		return
	}
	s.reloadCrons(updated.Type)
	ok(w, updated)
}

// automationDelete handles DELETE /v1/automation/{id}.
func (s *Server) automationDelete(w http.ResponseWriter, id string) {
	if !s.automationWritable(w) {
		return
	}
	s.Metrics.IncOp("automation_delete", id)
	existing := s.AutomationManager.Get(id)
	if existing == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if err := s.AutomationManager.Delete(id); err != nil {
		bad(w, err)
		return
	}
	s.reloadCrons(existing.Type)
	ok(w, map[string]string{"status": "deleted", "id": id})
}

// handleAutomationTest runs a trigger's search and returns matching docs (dry run).
func (s *Server) handleAutomationTest(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	rule := s.AutomationManager.Get(id)
	if rule == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	if rule.Type != "trigger" {
		bad(w, fmt.Errorf("can only test trigger rules, got: %s", rule.Type))
		return
	}

	s.Metrics.IncOp("automation_test", id)

	matches, err := s.AutomationManager.RunTrigger(rule)
	if err != nil {
		bad(w, err)
		return
	}

	ok(w, map[string]interface{}{
		"trigger": map[string]interface{}{
			"id":         rule.ID,
			"name":       rule.Name,
			"searchType": rule.SearchType,
			"query":      rule.Query,
			"threshold":  rule.Threshold,
		},
		"matches": matches,
		"total":   len(matches),
	})
}

// handleAutomationRun runs a cron rule's sync or backup now (#377): 202 with
// the run, 404 for no such rule, 409 while its previous run is going.
func (s *Server) handleAutomationRun(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	s.Metrics.IncOp("automation_run", id)
	var body any
	status := http.StatusAccepted
	run, err := s.runAutomationNow(id)
	if body = run; err != nil {
		status, body = automationRunStatus(err), map[string]string{"error": err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// automationRunStatus is the HTTP status of a RunNow error.
func automationRunStatus(err error) int {
	switch {
	case errors.Is(err, errRuleNotFound):
		return http.StatusNotFound
	case errors.Is(err, errActionRunning):
		return http.StatusConflict
	case errors.Is(err, errSchedulerOff):
		return http.StatusServiceUnavailable
	}
	return http.StatusBadRequest
}
