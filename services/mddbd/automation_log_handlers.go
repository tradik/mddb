package main

import (
	"net/http"
	"strconv"

	json "mddb/internal/jsonx"
)

// handleAutomationLogs handles GET /v1/automation-logs
func (s *Server) handleAutomationLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if s.AutomationLogStore == nil {
		http.Error(w, `{"error":"automation logs are disabled"}`, http.StatusNotFound)
		return
	}

	// Check admin permission
	if s.AuthManager != nil {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}

	if s.Metrics != nil {
		s.Metrics.IncOp("automation_logs_list", "")
	}

	q := r.URL.Query()

	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	cursor := q.Get("cursor")
	ruleID := q.Get("ruleId")
	status := q.Get("status")

	entries, nextCursor, err := s.AutomationLogStore.List(limit, cursor, ruleID, status)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	total, _ := s.AutomationLogStore.Count(ruleID, status)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"logs":       entries,
		"total":      total,
		"nextCursor": nextCursor,
		"hasMore":    nextCursor != "",
	})
}
