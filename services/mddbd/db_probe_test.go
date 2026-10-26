package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func healthOf(t *testing.T, s *Server) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleHealth(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// /health answers while a read transaction cannot begin, and fails only
// after the stall limit.
func TestHealthAnswersWhileTheDatabaseIsBusy(t *testing.T) {
	s, cleanup := newTestServer(t)
	defer cleanup()
	s.Ready = true
	old := healthProbeWait
	healthProbeWait = 20 * time.Millisecond
	defer func() { healthProbeWait = old }()

	s.Ready = false
	if code, _ := healthOf(t, s); code != 503 {
		t.Errorf("warming up: %d", code)
	}
	s.Ready = true
	if code, body := healthOf(t, s); code != 200 || body["status"] != "healthy" {
		t.Fatalf("idle: %d %v", code, body)
	}

	s.restoreMu.Lock() // no read transaction can begin
	start := time.Now()
	code, body := healthOf(t, s)
	if code != 200 || body["status"] != "busy" || time.Since(start) > time.Second {
		t.Errorf("busy: %d %v after %s", code, body, time.Since(start))
	}
	if h, err := (&DirectClient{server: s}).Health(context.Background()); err != nil || h.Status != "busy" {
		t.Errorf("MCP health while busy: %+v %v", h, err)
	}
	t.Setenv("MDDB_HEALTH_STALL_LIMIT", "0")
	if code, body := healthOf(t, s); code != 503 || body["status"] != "stalled" {
		t.Errorf("stalled: %d %v", code, body)
	}
	s.restoreMu.Unlock()

	// The probe that waited finishes; the next check is healthy again.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, body := healthOf(t, s); body["status"] == "healthy" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never healthy again")
		}
	}
	if h, err := (&DirectClient{server: s}).Health(context.Background()); err != nil || h.Status != "healthy" {
		t.Errorf("MCP health: %+v %v", h, err)
	}

	_ = s.DB.Close()
	if code, _ := healthOf(t, s); code != 503 {
		t.Errorf("closed database: %d", code)
	}
	if _, err := (&DirectClient{server: s}).Health(context.Background()); err == nil {
		t.Error("MCP health on a closed database")
	}
}

func TestShutdownExitsWhenItCannotFinish(t *testing.T) {
	exited := make(chan int, 2)
	old := shutdownExit
	shutdownExit = func(code int) { exited <- code }
	defer func() { shutdownExit = old }()

	s, cleanup := newTestServer(t)
	defer cleanup()
	s.finishShutdown(context.Background()) // finishes: closes the files, no exit
	select {
	case c := <-exited:
		t.Fatalf("exited %d after a complete shutdown", c)
	default:
	}
	if err := s.DB.View(func(*bolt.Tx) error { return nil }); err == nil {
		t.Error("the database is still open after shutdown")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.finishShutdown(ctx)
	if c := <-exited; c != 1 {
		t.Errorf("exit code %d", c)
	}

	if shutdownTimeout() <= 0 {
		t.Error("shutdown budget")
	}
	sigCh := make(chan os.Signal, 1)
	sigCh <- syscall.SIGTERM
	exitOnSecondSignal(sigCh)
	if c := <-exited; c != 1 {
		t.Errorf("second signal exit code %d", c)
	}
}
