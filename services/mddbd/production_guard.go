package main

import (
	"fmt"
	"os"
	"strings"
)

// ProductionRequirement is one compliance checkbox enforced when the
// operator sets MDDB_PRODUCTION=true.
type ProductionRequirement struct {
	EnvVar  string
	Want    string
	Reason  string // ISO 27001 / SOC 2 control citation
	Checker func() bool
}

// CheckProductionGuards returns the requirements that are NOT satisfied.
// An empty slice means the process is cleared for production.
//
// The function only reads environment variables — safe to call in
// tests via t.Setenv without touching the Server struct.
func CheckProductionGuards() []ProductionRequirement {
	reqs := productionRequirements()
	var missing []ProductionRequirement
	for _, r := range reqs {
		if !r.Checker() {
			missing = append(missing, r)
		}
	}
	return missing
}

// IsProduction reports whether MDDB_PRODUCTION is set to "true".
// Any other value (including unset) means development mode.
func IsProduction() bool {
	return strings.EqualFold(os.Getenv("MDDB_PRODUCTION"), "true")
}

// EnforceProductionGuards fatals when MDDB_PRODUCTION=true and any
// requirement is missing. When the flag is unset it emits a single
// WARN pointing operators to the compliance checklist. Safe to call
// exactly once at startup.
//
// fatalf is injected so tests can assert fatal conditions without
// actually exiting the process.
// EnforceProductionGuards reports on the production requirements. It takes the
// two loggers rather than calling slog directly so tests can observe what it
// decided; both have slog's (message, key/value...) shape, so callers pass
// slog.Warn and logging.Fatal.
func EnforceProductionGuards(warn, fatal func(msg string, args ...any)) {
	if !IsProduction() {
		missing := CheckProductionGuards()
		if len(missing) > 0 {
			warn("running with insecure defaults — set MDDB_PRODUCTION=true for ISO 27001 / SOC 2 compliance",
				"missing", summariseRequirements(missing))
		}
		return
	}
	missing := CheckProductionGuards()
	if len(missing) == 0 {
		warn("production guards satisfied (ISO 27001 / SOC 2)")
		return
	}
	var lines []string
	for _, r := range missing {
		lines = append(lines, fmt.Sprintf("%s: want %s (%s)", r.EnvVar, r.Want, r.Reason))
	}
	fatal("MDDB_PRODUCTION=true but requirements are missing",
		"count", len(missing), "requirements", strings.Join(lines, "; "))
}

func productionRequirements() []ProductionRequirement {
	return []ProductionRequirement{
		{
			EnvVar:  "MDDB_AUTH_ENABLED",
			Want:    "true",
			Reason:  "ISO 27001 A.5.15 / SOC 2 CC6.1 — access control",
			Checker: func() bool { return os.Getenv("MDDB_AUTH_ENABLED") == "true" },
		},
		{
			EnvVar:  "MDDB_AUTH_JWT_SECRET",
			Want:    "≥32 bytes",
			Reason:  "ISO 27001 A.8.24 / SOC 2 CC6.7 — cryptographic key strength",
			Checker: func() bool { return len(os.Getenv("MDDB_AUTH_JWT_SECRET")) >= 32 },
		},
		{
			EnvVar: "MDDB_TLS_ENABLED",
			Want:   "true (or explicit MDDB_TLS_INSECURE_OK=true)",
			Reason: "ISO 27001 A.8.24 / SOC 2 CC6.7 — encryption in transit",
			Checker: func() bool {
				return os.Getenv("MDDB_TLS_ENABLED") == "true" ||
					os.Getenv("MDDB_TLS_INSECURE_OK") == "true"
			},
		},
		{
			EnvVar: "MDDB_CORS_ORIGINS",
			Want:   "explicit origin allowlist (not *)",
			Reason: "ISO 27001 A.8.23 / SOC 2 CC6.6 — web-origin segmentation",
			Checker: func() bool {
				// SEC-008: pass when a non-wildcard allowlist is configured via
				// either MDDB_CORS_ORIGINS (preferred) or the legacy
				// MDDB_CORS_ORIGIN. An unset/`*` value resolves to wildcard.
				return !envCORSConfig().wildcard
			},
		},
		{
			EnvVar:  "MDDB_AUDIT_ENABLED",
			Want:    "true",
			Reason:  "ISO 27001 A.8.15 / SOC 2 CC7.2 — audit trail",
			Checker: func() bool { return os.Getenv("MDDB_AUDIT_ENABLED") == "true" },
		},
		{
			EnvVar:  "MDDB_RATE_LIMIT_ENABLED",
			Want:    "true",
			Reason:  "ISO 27001 A.5.30 / SOC 2 CC6.6 — resource-exhaustion protection",
			Checker: func() bool { return os.Getenv("MDDB_RATE_LIMIT_ENABLED") == "true" },
		},
	}
}

func summariseRequirements(missing []ProductionRequirement) string {
	names := make([]string, 0, len(missing))
	for _, r := range missing {
		names = append(names, r.EnvVar)
	}
	return strings.Join(names, ", ")
}
