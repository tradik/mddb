package lspindex

import (
	"errors"
	"fmt"
	"strings"
)

// ErrWorkspaceNotLoaded means the server answered every request yet saw only
// single files: no reference crossed a file boundary in a tree large enough
// that some must. gopls does this when the go on PATH is older than go.work
// requires — 168 reference locations instead of 2591, without one error.
// Index returns no facts then: heuristics beat false precision.
var ErrWorkspaceNotLoaded = errors.New("language server did not load the workspace")

// ErrFallbackFlags means the server compiled an indexed file with guessed
// flags instead of the project's (clangd without compile_commands.json): it
// answers anyway, from a partial view, and nothing in the protocol says so.
// Index returns no facts then.
var ErrFallbackFlags = errors.New("language server compiled files without the project's build settings")

// ServerError is a failed run, with what the server said about it.
type ServerError struct {
	Server string // the command that ran
	Err    error
	// Messages are the server's window/showMessage and window/logMessage
	// errors, then the first distinct error answers to requests.
	Messages []string
	// Stderr is the tail of the server's stderr (at most 64 KiB).
	Stderr string
}

// stderrInError is how much of the stderr tail Error quotes.
const stderrInError = 2 << 10

func (e *ServerError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %v", e.Server, e.Err)
	if len(e.Messages) > 0 {
		b.WriteString("\nserver messages:\n  " + strings.Join(e.Messages, "\n  "))
	}
	if tail := strings.TrimSpace(e.Stderr); tail != "" {
		if len(tail) > stderrInError {
			tail = "..." + tail[len(tail)-stderrInError:]
		}
		b.WriteString("\nserver stderr:\n" + tail)
	}
	return b.String()
}

func (e *ServerError) Unwrap() error { return e.Err }

// workspaceHint is what to do about ErrWorkspaceNotLoaded.
const workspaceHint = "the server most likely could not load the project. " +
	"Check that the toolchain on PATH is the one the " +
	"project needs (Go: `go version` against go.work/go.mod, set PATH or GOTOOLCHAIN; Python: the " +
	"virtualenv; PHP: composer install) and that the server's own messages below report no error, then run again"
