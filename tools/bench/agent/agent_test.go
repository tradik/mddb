package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestGrepPath(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/many.go":           "budget\nbudget\nbudget\n" + strings.Repeat("x", 400),
		"b/answer.go":         "func budgetCut() {} // budget\n",
		"c/none.go":           "nothing here\n",
		".git/config":         "budget",
		"node_modules/x/i.js": "budget",
		"bin.dat":             "budget\x00binary",
	})
	files, err := textFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f, ".git") || strings.HasPrefix(f, "node_modules") {
			t.Fatalf("skipped directory listed: %s", f)
		}
	}
	best, typical := grepPath(root, files, task{Grep: "budget", Answer: "b/answer.go"})
	// Best reads grep output plus the answer; typical reads the file with
	// most matches first, then the answer.
	if !best.Found || best.Calls != 2 || !typical.Found || typical.Calls != 3 || typical.Tokens <= best.Tokens {
		t.Fatalf("best %+v typical %+v", best, typical)
	}
	if b, _ := grepPath(root, files, task{Grep: "absent", Answer: "b/answer.go"}); b.Found || b.Calls != 1 || b.Tokens != 0 {
		t.Fatalf("no match: %+v", b)
	}
}

func fakeMDDB(t *testing.T, status int, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRun(t *testing.T) {
	root := writeTree(t, map[string]string{"b/answer.go": "func budgetCut() {} // budget\n"})
	tasksFile := filepath.Join(t.TempDir(), "tasks.json")
	_ = os.WriteFile(tasksFile, []byte(`{"tasks":[{"id":"budget","question":"cut to budget","grep":"budget","answer":"b/answer.go"},
		{"id":"miss","question":"q","grep":"zzz","answer":"b/answer.go"}]}`), 0o600)
	srv := fakeMDDB(t, 200, `{"passages":[{"path":"b/answer.go","key":"b/answer.go"}]}`)
	var out bytes.Buffer
	if err := run(&out, srv.Client(), srv.URL, "c", root, tasksFile, "hybrid", 5, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mddb hybrid", "answer found", "grep (best) uses"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run(&out, srv.Client(), srv.URL, "c", root, tasksFile, "fts", 5, true); err != nil || !strings.Contains(out.String(), `"mddb":{"tokens"`) {
		t.Fatalf("json: %v %s", err, out.String())
	}
	// Failures: unknown search, server error, bad body, unreachable, tasks.
	for _, c := range []struct {
		srv    *httptest.Server
		search string
	}{{srv, "nope"}, {fakeMDDB(t, 500, "boom"), "fts"}, {fakeMDDB(t, 200, "[1]"), "vector"}} {
		if err := run(&out, c.srv.Client(), c.srv.URL, "c", root, tasksFile, c.search, 5, false); err == nil {
			t.Fatalf("%s accepted", c.search)
		}
	}
	if err := run(&out, http.DefaultClient, "http://127.0.0.1:1", "c", root, tasksFile, "fts", 5, false); err == nil {
		t.Fatal("unreachable")
	}
	empty := filepath.Join(t.TempDir(), "empty.json")
	_ = os.WriteFile(empty, []byte(`{"tasks":[]}`), 0o600)
	bad := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(bad, []byte(`{`), 0o600)
	for _, f := range []string{empty, bad, filepath.Join(t.TempDir(), "missing.json")} {
		if err := run(&out, srv.Client(), srv.URL, "c", root, f, "fts", 5, false); err == nil {
			t.Fatalf("%s accepted", f)
		}
	}
	if err := run(&out, srv.Client(), srv.URL, "c", filepath.Join(root, "missing"), tasksFile, "fts", 5, false); err == nil {
		t.Fatal("missing root")
	}
}

func TestCLIAndGitFiles(t *testing.T) {
	root := writeTree(t, map[string]string{"tracked.go": "budget\n", "ignored.log": "budget\n", ".gitignore": "*.log\n"})
	for _, args := range [][]string{{"init", "-q"}, {"add", "tracked.go", ".gitignore"}} {
		if err := exec.Command("git", append([]string{"-C", root}, args...)...).Run(); err != nil {
			t.Skip("git not available")
		}
	}
	files, err := textFiles(root)
	if err != nil || strings.Join(files, ",") != ".gitignore,tracked.go" {
		t.Fatalf("git files: %v %v", files, err)
	}
	tasksFile := filepath.Join(t.TempDir(), "tasks.json")
	_ = os.WriteFile(tasksFile, []byte(`{"tasks":[{"id":"b","question":"q","grep":"budget","answer":"tracked.go"}]}`), 0o600)
	srv := fakeMDDB(t, 200, `{"passages":[]}`)
	var out, errOut bytes.Buffer
	if code := cli([]string{"-url", srv.URL, "-root", root, "-tasks", tasksFile, "-search", "fts"}, &out, &errOut); code != 0 {
		t.Fatalf("cli: %d %s", code, errOut.String())
	}
	if code := cli([]string{"-nope"}, &out, &errOut); code != 2 {
		t.Fatal("bad flag")
	}
	if code := cli([]string{"-tasks", "missing.json"}, &out, &errOut); code != 1 {
		t.Fatal("missing tasks")
	}
}
