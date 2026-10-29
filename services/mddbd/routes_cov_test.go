package main

import (
	"net/http"
	"testing"
	"time"

	"mddb/internal/automationlog"
)

// Automation routes exist exactly when the automation manager does, and the
// log route only when the log store does too: served without them they would
// dereference a nil manager.
func TestCovAAutomationRoutesFollowTheirManagers(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()

	paths := []string{"/v1/automation", "/v1/automation/x", "/v1/automation-logs"}

	off := http.NewServeMux()
	srv.registerRoutes(off, false)
	if got := registeredRoutes(t, off, paths); len(got) != 0 {
		t.Fatalf("automation routes served without a manager: %v", got)
	}

	srv.AutomationManager = NewAutomationManager(srv.DB)
	noLogs := http.NewServeMux()
	srv.registerRoutes(noLogs, false)
	got := registeredRoutes(t, noLogs, paths)
	if !got["/v1/automation"] || !got["/v1/automation/x"] || got["/v1/automation-logs"] {
		t.Fatalf("with a manager and no log store got %v", got)
	}

	srv.AutomationLogStore = automationlog.NewStore(srv.DB, time.Hour)
	all := http.NewServeMux()
	srv.registerRoutes(all, false)
	got = registeredRoutes(t, all, paths)
	for _, p := range paths {
		if !got[p] {
			t.Errorf("%s is not served with automation and logs enabled", p)
		}
	}
}
