package main

import (
	"fmt"
	"net/http"
	"strings"

	json "mddb/internal/jsonx"
)

// handleAutomation dispatches GET (list) and POST (create) for /v1/automation.
func (s *Server) handleAutomation(w http.ResponseWriter, r *http.Request) {
	if s.AutomationManager == nil {
		bad(w, fmt.Errorf("automation not initialized"))
		return
	}

	// Check admin permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		if s.Metrics != nil {
			s.Metrics.IncOp("automation_list", "")
		}
		filterType := r.URL.Query().Get("type")
		rules := s.AutomationManager.List(filterType)
		ok(w, map[string]interface{}{
			"rules": rules,
			"total": len(rules),
		})

	case http.MethodPost:
		if s.Mode == ModeRead {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		if s.Metrics != nil {
			s.Metrics.IncOp("automation_create", "")
		}
		var rule AutomationRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			bad(w, err)
			return
		}
		created, err := s.AutomationManager.Create(rule)
		if err != nil {
			bad(w, err)
			return
		}
		// Reload cron scheduler if a cron was created
		if created.Type == "cron" && s.CronScheduler != nil {
			s.CronScheduler.Reload()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(created)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleAutomationDetail dispatches GET/PUT/DELETE for /v1/automation/{id}
// and POST for /v1/automation/{id}/test.
func (s *Server) handleAutomationDetail(w http.ResponseWriter, r *http.Request) {
	if s.AutomationManager == nil {
		bad(w, fmt.Errorf("automation not initialized"))
		return
	}

	// Check admin permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	// Parse path: /v1/automation/{id} or /v1/automation/{id}/test
	path := strings.TrimPrefix(r.URL.Path, "/v1/automation/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		bad(w, fmt.Errorf("missing rule id"))
		return
	}

	// Check for /test suffix
	if len(parts) == 2 && parts[1] == "test" {
		s.handleAutomationTest(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if s.Metrics != nil {
			s.Metrics.IncOp("automation_get", id)
		}
		rule := s.AutomationManager.Get(id)
		if rule == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		ok(w, rule)

	case http.MethodPut:
		if s.Mode == ModeRead {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		var update AutomationRule
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			bad(w, err)
			return
		}
		if s.Metrics != nil {
			s.Metrics.IncOp("automation_update", id)
		}
		updated, err := s.AutomationManager.Update(id, update)
		if err != nil {
			bad(w, err)
			return
		}
		// Reload cron scheduler if a cron was updated
		if updated.Type == "cron" && s.CronScheduler != nil {
			s.CronScheduler.Reload()
		}
		ok(w, updated)

	case http.MethodDelete:
		if s.Mode == ModeRead {
			http.Error(w, `{"error":"read-only mode"}`, http.StatusForbidden)
			return
		}
		if s.Metrics != nil {
			s.Metrics.IncOp("automation_delete", id)
		}
		existing := s.AutomationManager.Get(id)
		if existing == nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		if err := s.AutomationManager.Delete(id); err != nil {
			bad(w, err)
			return
		}
		// Reload cron scheduler if a cron was deleted
		if existing.Type == "cron" && s.CronScheduler != nil {
			s.CronScheduler.Reload()
		}
		ok(w, map[string]string{"status": "deleted", "id": id})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
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

	if s.Metrics != nil {
		s.Metrics.IncOp("automation_test", id)
	}

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
