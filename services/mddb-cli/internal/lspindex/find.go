package lspindex

import (
	"context"
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
