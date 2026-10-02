package main

import (
	"os"
	"testing"
)

// TestMain configures the package test environment.
//
// The suite delivers webhooks, bulk callbacks, automation triggers and
// import-url fetches to local httptest servers (127.0.0.1), which the outbound
// SSRF guard (SEC-004) blocks by default. Enable the documented
// MDDB_OUTBOUND_ALLOW_PRIVATE opt-in for the whole test binary so those local
// deliveries succeed. The guard's blocking behaviour is verified independently
// in ssrf_guard_test.go, which sets the variable back to empty where it asserts
// that private/loopback targets are refused.
func TestMain(m *testing.M) {
	// verifyDatabase re-runs the current binary as its checker; under test
	// that binary is this one.
	if p := os.Getenv(verifyChildEnv); p != "" {
		os.Exit(runVerifyChild(p))
	}
	if os.Getenv("MDDB_OUTBOUND_ALLOW_PRIVATE") == "" {
		_ = os.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")
	}
	os.Exit(m.Run())
}
