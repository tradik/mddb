package main

import (
	"os"
	"time"

	mddb "mddb-client"
)

// newClient builds the shared MDDB Go client (clients/go/mddb) from the CLI's
// global flags. The CLI no longer carries its own HTTP transport (GO-015 — the
// duplicated client/request implementation was removed in favour of the SDK).
func newClient() *mddb.Client {
	opts := []mddb.Option{}
	if apiKey != "" {
		opts = append(opts, mddb.WithAPIKey(apiKey))
	} else if token != "" {
		opts = append(opts, mddb.WithToken(token))
	}
	if verbose {
		opts = append(opts, mddb.WithVerbose(os.Stderr))
	}
	return mddb.New(serverURL, opts...)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// --- safe JSON accessors (GO-005) ---
// Server responses are parsed into map[string]interface{}; bare type assertions
// (x.(float64)) panic on any missing/null/renamed field. These helpers degrade
// gracefully to a zero value instead, so the CLI prints a readable line rather
// than crashing with a stack trace.

func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

func asFloat(v interface{}) float64 {
	f, _ := v.(float64)
	return f
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// formatUnix formats a JSON number as an RFC3339 timestamp, or "-" if the value
// is missing or not a number.
func formatUnix(v interface{}) string {
	f, ok := v.(float64)
	if !ok {
		return "-"
	}
	return time.Unix(int64(f), 0).Format(time.RFC3339)
}

// shorten trims a long identifier for display, appending an ellipsis only when
// something was actually cut.
//
// GO-005 again, found by TEST-001: the api-key listing wrote keyHash[:16],
// which panics with a stack trace on any hash shorter than that — an empty
// field, a truncated response, or a server that shortens its hashes. Slicing a
// string by a length nobody checked is the same crash the safe accessors above
// exist to prevent; this one was missed because it is a string, not a type
// assertion.
func shorten(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
