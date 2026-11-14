package lspindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Finding a server without starting it (CODE-011): autoindex checks that
// every language it was asked for has a server before it indexes any, and
// `mddb-cli diagnose` lists what is installed.

// versionTimeout bounds a server's --version: some start a runtime first.
const versionTimeout = 10 * time.Second

// Find returns the command Index would start for spec, without starting it;
// the error wraps ErrNoServer when neither the environment nor PATH has one.
// Only opt.Getenv and opt.LookPath are used.
func Find(spec ServerSpec, opt Options) (Candidate, error) {
	if opt.Getenv == nil {
		opt.Getenv = os.Getenv
	}
	if opt.LookPath == nil {
		opt.LookPath = exec.LookPath
	}
	return spec.resolve(opt.Getenv, opt.LookPath)
}

// Program is the candidate's program name without its directory: what a
// report may show without revealing where the operator keeps it.
func (c Candidate) Program() string {
	if len(c.Argv) == 0 {
		return ""
	}
	return filepath.Base(c.Argv[0])
}

// Version runs the candidate's version command and returns the first line it
// printed, or "" when it printed nothing.
func Version(ctx context.Context, c Candidate) string {
	if len(c.Argv) == 0 {
		return ""
	}
	args := c.VersionArgs
	if args == nil {
		args = []string{"--version"}
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	// The program comes from the server table or the operator's MDDB_LSP_*
	// variable, resolved on PATH, exactly as Index would start it.
	out, _ := exec.CommandContext(ctx, c.Argv[0], args...).CombinedOutput() // #nosec G204 -- see above
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return clipMessage(line)
		}
	}
	return ""
}

// ErrNoServer is returned when neither the environment nor PATH provides a
// server for the language.
var ErrNoServer = errors.New("language server not found")

// resolve picks the command to run: the operator's override first, then the
// first candidate on PATH.
func (s ServerSpec) resolve(getenv func(string) string, lookPath func(string) (string, error)) (Candidate, error) {
	if s.EnvVar != "" {
		if argv := strings.Fields(getenv(s.EnvVar)); len(argv) > 0 {
			prog, err := lookPath(argv[0])
			if err != nil {
				return Candidate{}, fmt.Errorf("%w: %s=%q: %v", ErrNoServer, s.EnvVar, getenv(s.EnvVar), err)
			}
			// A table program at another path keeps the table's name, so
			// symIndex says pyrefly/1.4.1 however pyrefly was found.
			found := Candidate{Argv: append([]string{prog}, argv[1:]...)}
			for _, c := range s.Candidates {
				if c.Argv[0] == filepath.Base(argv[0]) {
					found.Name, found.VersionArgs = c.Name, c.VersionArgs
				}
			}
			return found, nil
		}
	}
	var tried []string
	for _, c := range s.Candidates {
		prog, err := lookPath(c.Argv[0])
		if err == nil {
			return Candidate{Name: c.Name, Argv: append([]string{prog}, c.Argv[1:]...), VersionArgs: c.VersionArgs}, nil
		}
		tried = append(tried, strings.Join(c.Argv, " "))
	}
	return Candidate{}, fmt.Errorf("%w for %s: none of [%s] is on PATH; install one or set %s to a command line",
		ErrNoServer, s.Language, strings.Join(tried, "; "), s.EnvVar)
}

// command is the command line Index starts: the candidate's, plus the
// spec's Args when the program is the table's (Name is set then).
func (s ServerSpec) command(cand Candidate, opt Options) []string {
	argv := append([]string(nil), cand.Argv...)
	if s.Args != nil && cand.Name != "" {
		argv = append(argv, s.Args(opt)...)
	}
	return argv
}
