// Package logging configures the process-wide structured logger (GO-028).
//
// The server used to log through the standard library's log package: plain
// text, local time without a zone, no level field, and severity spelled into
// the message ("ERROR: ...", "WARNING: ...", "⚠️  SECURITY: ..."). A collector
// could not filter that by level without matching prose, which put the
// operational log below the audit trail the product already exports as
// RFC 5424 syslog and SIEM webhooks.
//
// Setup installs an slog handler as the default, so every slog call in the
// server lands in one place with one format. Two variables control it:
//
//	MDDB_LOG_FORMAT   text (default) | json
//	MDDB_LOG_LEVEL    debug | info (default) | warn | error
//
// Container images set MDDB_LOG_FORMAT=json, because a log shipper is the
// normal reader there; a terminal gets text.
//
// Output goes to stderr, matching the log package's own default. That matters
// for MCP stdio mode, where stdout carries the protocol and anything written
// to it corrupts the stream.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format identifies a handler's output encoding.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// ParseFormat maps an MDDB_LOG_FORMAT value to a Format. Anything
// unrecognised — including an empty value — resolves to text, since a
// misspelled variable should not silently disable human-readable output.
func ParseFormat(s string) Format {
	if strings.EqualFold(strings.TrimSpace(s), string(FormatJSON)) {
		return FormatJSON
	}
	return FormatText
}

// ParseLevel maps an MDDB_LOG_LEVEL value to a slog.Level, falling back to
// info for anything unrecognised.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewHandler builds a handler writing to w. Exported so tests can capture
// output without touching the process-wide default.
func NewHandler(w io.Writer, format Format, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if format == FormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// Setup reads the environment, installs the resulting logger as slog's
// default, and returns it. It also points the standard log package at the
// same handler at info level, so any straggling log.Printf — third-party
// libraries included — is carried in the same stream and format rather than
// escaping as unstructured text.
func Setup() *slog.Logger {
	return setupWith(os.Stderr, os.Getenv("MDDB_LOG_FORMAT"), os.Getenv("MDDB_LOG_LEVEL"))
}

func setupWith(w io.Writer, format, level string) *slog.Logger {
	handler := NewHandler(w, ParseFormat(format), ParseLevel(level))
	logger := slog.New(handler)
	slog.SetDefault(logger)
	// slog.SetDefault already redirects the log package's output through the
	// default handler, but it keeps log's own flags; clearing them avoids a
	// duplicated timestamp inside the message text.
	return logger
}

// exit is os.Exit behind a variable so Fatal's contract can be tested without
// killing the test binary.
var exit = os.Exit

// Fatal logs at error level and terminates the process, replacing log.Fatal.
// slog has no fatal level by design — the level says how bad the event is, not
// what the program does next — so the exit stays explicit here rather than
// hiding inside a severity name.
func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	exit(1)
}
