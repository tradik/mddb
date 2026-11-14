package lspindex

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// wantServerError checks err is a *ServerError and returns it.
func wantServerError(t *testing.T, res *Result, err error) *ServerError {
	t.Helper()
	if res != nil {
		t.Errorf("facts returned with an error: %+v", res)
	}
	var se *ServerError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want *ServerError", err, err)
	}
	return se
}

func TestIndexServerDiesMidRun(t *testing.T) {
	for _, at := range []string{"documentSymbol a.go", "references a.go:12", "implementation a.go:2"} {
		t.Run(at, func(t *testing.T) {
			before := runtime.NumGoroutine()
			spec := goSpec(t)
			sc := goScript()
			sc.Die = at
			sc.Stderr = "panic: scripted crash\n"
			f := newFakeRun(t, goTree, sc)
			res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{Concurrency: 1}))
			se := wantServerError(t, res, err)
			if !errors.Is(err, errServerGone) || !strings.Contains(se.Stderr, "scripted crash") ||
				!strings.Contains(err.Error(), "server stderr:\npanic: scripted crash") {
				t.Errorf("err = %v", err)
			}
			f.record(t)
			assertNoGoroutineLeak(t, before)
		})
	}
}

func TestIndexRetriesDocumentSymbolOnce(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.FailOnce = []string{"documentSymbol a.go", "documentSymbol b.go"}
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Files, goWant) || res.Stats.Skipped != 0 {
		t.Errorf("Stats = %+v", res.Stats)
	}
	f.record(t)
}

func TestIndexServerDiesOnRetry(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.FailOnce = []string{"documentSymbol a.go"}
	sc.Die = "documentSymbol a.go"
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{}))
	_ = wantServerError(t, res, err)
	if !errors.Is(err, errServerGone) {
		t.Errorf("err = %v", err)
	}
	f.record(t)
}

// TestIndexServerDiesWhileFilesOpen: a file larger than a pipe buffer makes
// didOpen block until the dead server's end of the pipe is closed.
func TestIndexServerDiesWhileFilesOpen(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.Die = "initialize"
	tree := map[string]string{"big.go": "package big\n//" + strings.Repeat("x", 4<<20) + "\n"}
	f := newFakeRun(t, tree, sc)
	res, err := Index(context.Background(), f.root, []string{"big.go"}, spec, f.options(spec, Options{}))
	_ = wantServerError(t, res, err)
	if !errors.Is(err, errServerGone) {
		t.Errorf("err = %v", err)
	}
	f.record(t)
}

func TestIndexGarbageHeader(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.Garbage = "initialize"
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{}))
	_ = wantServerError(t, res, err)
	if !errors.Is(err, errProtocol) || !strings.Contains(err.Error(), "initialize") {
		t.Errorf("err = %v", err)
	}
	f.record(t)
}

func TestIndexOverallTimeout(t *testing.T) {
	spec := goSpec(t)
	sc := goScript()
	sc.Hang = []string{"documentSymbol a.go"}
	f := newFakeRun(t, goTree, sc)
	res, err := Index(context.Background(), f.root, goFiles, spec,
		f.options(spec, Options{Timeout: 500 * time.Millisecond, RequestTimeout: time.Minute}))
	_ = wantServerError(t, res, err)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "indexing go stopped") {
		t.Errorf("err = %v", err)
	}
}

func TestIndexKillsServerIgnoringExit(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 200 * time.Millisecond
	defer func() { shutdownTimeout = old }()
	spec := goSpec(t)
	sc := goScript()
	sc.IgnoreExit = true
	f := newFakeRun(t, goTree, sc)
	if _, err := Index(context.Background(), f.root, goFiles, spec, f.options(spec, Options{})); err != nil {
		t.Fatal(err)
	}
	f.record(t) // asserts the killed process was reaped
}

func TestIndexNoServer(t *testing.T) {
	spec := goSpec(t)
	missing := func(string) (string, error) { return "", errors.New("not found") }
	_, err := Index(context.Background(), t.TempDir(), nil, spec, Options{Getenv: func(string) string { return "" }, LookPath: missing})
	if !errors.Is(err, ErrNoServer) || !strings.Contains(err.Error(), "MDDB_LSP_GO") || !strings.Contains(err.Error(), "gopls serve") {
		t.Errorf("err = %v", err)
	}
	_, err = Index(context.Background(), t.TempDir(), nil, spec, Options{Getenv: func(string) string { return "nosuch --flag" }, LookPath: missing})
	if !errors.Is(err, ErrNoServer) || !strings.Contains(err.Error(), `MDDB_LSP_GO="nosuch --flag"`) {
		t.Errorf("err = %v", err)
	}
}

func TestIndexStartFailure(t *testing.T) {
	spec := goSpec(t)
	found := func(string) (string, error) { return "/nonexistent/gopls", nil }
	res, err := Index(context.Background(), t.TempDir(), nil, spec, Options{Getenv: func(string) string { return "" }, LookPath: found})
	se := wantServerError(t, res, err)
	if se.Server != "/nonexistent/gopls serve" {
		t.Errorf("Server = %q", se.Server)
	}
}

func TestIndexBadRoot(t *testing.T) {
	_, err := Index(context.Background(), "/nonexistent/root", nil, goSpec(t), Options{})
	if err == nil {
		t.Fatal("no error for a missing root")
	}
}

func TestSpecUnknownLanguage(t *testing.T) {
	if _, err := Spec("cobol"); !errors.Is(err, ErrUnsupportedLanguage) || !strings.Contains(err.Error(), "go, php, python") {
		t.Errorf("err = %v", err)
	}
}
