package main

import (
	"fmt"
	"strings"
	"testing"
)

// clearProdEnv unsets every env var the guard inspects so each test
// starts from a clean slate. Using t.Setenv ensures automatic restore.
func clearProdEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"MDDB_PRODUCTION",
		"MDDB_AUTH_ENABLED",
		"MDDB_AUTH_JWT_SECRET",
		"MDDB_TLS_ENABLED",
		"MDDB_TLS_INSECURE_OK",
		"MDDB_CORS_ORIGINS",
		"MDDB_CORS_ORIGIN",
		"MDDB_AUDIT_ENABLED",
		"MDDB_RATE_LIMIT_ENABLED",
	}
	for _, v := range vars {
		t.Setenv(v, "")
	}
}

func setAllProdEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	t.Setenv("MDDB_AUTH_JWT_SECRET", strings.Repeat("x", 32))
	t.Setenv("MDDB_TLS_ENABLED", "true")
	t.Setenv("MDDB_CORS_ORIGINS", "https://app.example.com")
	t.Setenv("MDDB_AUDIT_ENABLED", "true")
	t.Setenv("MDDB_RATE_LIMIT_ENABLED", "true")
}

func TestIsProduction(t *testing.T) {
	clearProdEnv(t)
	if IsProduction() {
		t.Fatal("expected false when env unset")
	}
	t.Setenv("MDDB_PRODUCTION", "true")
	if !IsProduction() {
		t.Fatal("expected true for explicit true")
	}
	t.Setenv("MDDB_PRODUCTION", "TRUE")
	if !IsProduction() {
		t.Fatal("case-insensitive match expected")
	}
	t.Setenv("MDDB_PRODUCTION", "1")
	if IsProduction() {
		t.Fatal("only literal true/TRUE counts")
	}
}

func TestCheckProductionGuards_AllMissing(t *testing.T) {
	clearProdEnv(t)
	missing := CheckProductionGuards()
	if len(missing) != 6 {
		t.Fatalf("want 6 missing, got %d: %+v", len(missing), missing)
	}
}

func TestCheckProductionGuards_AllPresent(t *testing.T) {
	clearProdEnv(t)
	setAllProdEnv(t)
	missing := CheckProductionGuards()
	if len(missing) != 0 {
		t.Fatalf("want 0 missing, got %d: %+v", len(missing), missing)
	}
}

func TestCheckProductionGuards_JWTSecretTooShort(t *testing.T) {
	clearProdEnv(t)
	setAllProdEnv(t)
	t.Setenv("MDDB_AUTH_JWT_SECRET", "short")
	missing := CheckProductionGuards()
	if len(missing) != 1 || missing[0].EnvVar != "MDDB_AUTH_JWT_SECRET" {
		t.Fatalf("expected only JWT secret to fail: %+v", missing)
	}
}

func TestCheckProductionGuards_TLSInsecureOKOptOut(t *testing.T) {
	clearProdEnv(t)
	setAllProdEnv(t)
	t.Setenv("MDDB_TLS_ENABLED", "")
	t.Setenv("MDDB_TLS_INSECURE_OK", "true")
	missing := CheckProductionGuards()
	if len(missing) != 0 {
		t.Fatalf("INSECURE_OK should satisfy the TLS requirement: %+v", missing)
	}
}

func TestCheckProductionGuards_CORSStar(t *testing.T) {
	clearProdEnv(t)
	setAllProdEnv(t)
	t.Setenv("MDDB_CORS_ORIGINS", "*")
	missing := CheckProductionGuards()
	if len(missing) != 1 || missing[0].EnvVar != "MDDB_CORS_ORIGINS" {
		t.Fatalf("CORS=* should flag: %+v", missing)
	}
}

func TestCheckProductionGuards_CORSUnsetFlags(t *testing.T) {
	clearProdEnv(t)
	setAllProdEnv(t)
	t.Setenv("MDDB_CORS_ORIGINS", "")
	missing := CheckProductionGuards()
	if len(missing) != 1 || missing[0].EnvVar != "MDDB_CORS_ORIGINS" {
		t.Fatalf("CORS unset (would default to *) must flag: %+v", missing)
	}
}

func TestEnforceProductionGuards_DevModeWarns(t *testing.T) {
	clearProdEnv(t)
	var logs []string
	var fatals []string
	EnforceProductionGuards(
		func(m string, a ...any) { logs = append(logs, m+fmt.Sprint(a...)) },
		func(m string, a ...any) { fatals = append(fatals, m+fmt.Sprint(a...)) },
	)
	if len(fatals) != 0 {
		t.Fatalf("dev mode must not fatal: %v", fatals)
	}
	if len(logs) != 1 {
		t.Fatalf("expected one warning, got %v", logs)
	}
	if !strings.Contains(logs[0], "insecure defaults") {
		t.Errorf("warning missing expected text: %q", logs[0])
	}
}

func TestEnforceProductionGuards_DevModeFullyConfiguredNoWarn(t *testing.T) {
	clearProdEnv(t)
	setAllProdEnv(t)
	// MDDB_PRODUCTION not set — even when everything is configured, the
	// guard only warns on missing pieces. Passing == silent.
	var logs []string
	EnforceProductionGuards(
		func(m string, a ...any) { logs = append(logs, m+fmt.Sprint(a...)) },
		func(f string, a ...interface{}) { t.Fatalf("unexpected fatal: "+f, a...) },
	)
	if len(logs) != 0 {
		t.Fatalf("expected silence, got %v", logs)
	}
}

func TestEnforceProductionGuards_ProdFatalsOnMissing(t *testing.T) {
	clearProdEnv(t)
	t.Setenv("MDDB_PRODUCTION", "true")
	var fatals []string
	EnforceProductionGuards(
		func(string, ...interface{}) {},
		func(m string, a ...any) { fatals = append(fatals, m+fmt.Sprint(a...)) },
	)
	if len(fatals) != 1 {
		t.Fatalf("expected one fatal, got %v", fatals)
	}
	if !strings.Contains(fatals[0], "MDDB_AUTH_ENABLED") {
		t.Errorf("fatal should enumerate missing vars: %q", fatals[0])
	}
}

func TestEnforceProductionGuards_ProdHappyPath(t *testing.T) {
	clearProdEnv(t)
	setAllProdEnv(t)
	t.Setenv("MDDB_PRODUCTION", "true")
	var logs []string
	EnforceProductionGuards(
		func(m string, a ...any) { logs = append(logs, m+fmt.Sprint(a...)) },
		func(f string, a ...interface{}) { t.Fatalf("unexpected fatal: "+f, a...) },
	)
	if len(logs) != 1 || !strings.Contains(logs[0], "satisfied") {
		t.Fatalf("expected one 'satisfied' log, got %v", logs)
	}
}

func TestSummariseRequirements(t *testing.T) {
	out := summariseRequirements([]ProductionRequirement{
		{EnvVar: "A"},
		{EnvVar: "B"},
	})
	if out != "A, B" {
		t.Fatalf("got %q", out)
	}
}
