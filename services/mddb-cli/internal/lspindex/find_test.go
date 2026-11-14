package lspindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeScript writes an executable shell script that prints its arguments
// after body's output.
func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { // #nosec G306 -- a test program must be executable
		t.Fatal(err)
	}
	return p
}

// ContentHash is the hash Encode puts in symIndex.
func TestContentHashMatchesSymIndex(t *testing.T) {
	idx := strings.Fields(Encode("package x\n", "gopls/1", nil)[KeyIndex][0])
	if got := ContentHash("package x\n"); got != idx[1] || len(got) != 12 {
		t.Errorf("ContentHash = %q, symIndex %v", got, idx)
	}
}

func TestFindUsesTheTableAndKeepsVersionArgs(t *testing.T) {
	spec := goSpec(t)
	cand, err := Find(spec, Options{Getenv: func(string) string { return "" },
		LookPath: func(name string) (string, error) { return "/usr/local/bin/" + name, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if cand.Program() != "gopls" || !reflect.DeepEqual(cand.VersionArgs, []string{"version"}) {
		t.Errorf("candidate = %+v", cand)
	}
	// An override naming the table's program keeps its version command.
	cand, err = Find(spec, Options{Getenv: func(string) string { return "/opt/gopls serve" },
		LookPath: func(name string) (string, error) { return name, nil }})
	if err != nil || !reflect.DeepEqual(cand.VersionArgs, []string{"version"}) {
		t.Errorf("override = %+v, %v", cand, err)
	}
}

func TestFindDefaultsToTheRealEnvironment(t *testing.T) {
	spec, _ := Spec("php")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MDDB_LSP_PHP", "")
	if _, err := Find(spec, Options{}); !errors.Is(err, ErrNoServer) {
		t.Errorf("err = %v", err)
	}
}

func TestVersionPrintsTheFirstLine(t *testing.T) {
	prog := writeScript(t, "srv", `echo; echo "  srv $1 "; echo second`)
	if got := Version(context.Background(), Candidate{Argv: []string{prog}}); got != "srv --version" {
		t.Errorf("default args: %q", got)
	}
	if got := Version(context.Background(), Candidate{Argv: []string{prog}, VersionArgs: []string{"version"}}); got != "srv version" {
		t.Errorf("table args: %q", got)
	}
	long := writeScript(t, "long", "echo "+strings.Repeat("x", 400))
	if got := Version(context.Background(), Candidate{Argv: []string{long}}); len(got) != 303 {
		t.Errorf("long line kept %d bytes", len(got))
	}
	silent := writeScript(t, "silent", "exit 1")
	if got := Version(context.Background(), Candidate{Argv: []string{silent}}); got != "" {
		t.Errorf("silent: %q", got)
	}
	if Version(context.Background(), Candidate{}) != "" || (Candidate{}).Program() != "" {
		t.Error("an empty candidate has no version and no program")
	}
}
