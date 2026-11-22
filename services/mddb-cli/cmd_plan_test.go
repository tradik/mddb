package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// TestPlanCLI_ImportExportRoundTrip is the ticket's round trip: a
// BACKLOG.md in, the same table out, byte for byte.
func TestPlanCLI_ImportExportRoundTrip(t *testing.T) {
	srv := newPlanServer(t)
	file := filepath.Join(t.TempDir(), "BACKLOG.md")
	if err := os.WriteFile(file, []byte(backlogFile), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, srv.URL, "plan", "-c", "c", "import", file)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Imported 3 rows into c (0 already there)")
	out, err = runCLI(t, srv.URL, "plan", "-c", "c", "export")
	if err != nil || out != backlogTable {
		t.Fatalf("export (%v):\n%s\nwant:\n%s", err, out, backlogTable)
	}
	// A second import adds nothing.
	out, _ = runCLI(t, srv.URL, "plan", "-c", "c", "import", file)
	mustContain(t, out, "Imported 0 rows into c (3 already there)")
	if d, _ := srv.doc("backlog/31-webhook-with-hmac-and-a"); d.Meta["issues"][0] != "31" || d.Meta["model"][0] != "Opus" {
		t.Fatalf("%v", d.Meta)
	}
}

// Columns the plan does not know survive the round trip as meta.
func TestPlanCLI_UnknownColumns(t *testing.T) {
	srv := newPlanServer(t)
	table := "| Owner | Prompt | Status | Size |\n|---|---|---|---|\n| ann | do a | open | S |\n|  |  |  |  |\n| bob | do b \\| c | later |  |\n"
	file := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(file, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, srv.URL, "plan", "-c", "c", "import", file); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(table, "|  |  |  |  |\n", "", 1)
	if out, _ := runCLI(t, srv.URL, "plan", "-c", "c", "export"); out != want {
		t.Fatalf("export:\n%s\nwant:\n%s", out, want)
	}
	if d, _ := srv.doc("backlog/do-a"); d.Meta["owner"][0] != "ann" || d.Meta["columnStatus"][0] != "open" || d.Meta["status"][0] != "todo" {
		t.Fatalf("%v", d.Meta)
	}
}

func TestPlanCLI_AddNextDoneList(t *testing.T) {
	srv := newPlanServer(t)
	run := func(args ...string) string {
		t.Helper()
		out, err := runCLI(t, srv.URL, append([]string{"plan", "-c", "c"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	mustContain(t, run("next"), "No todo rows in c")
	mustContain(t, run("list"), "No rows in c")
	mustContain(t, run("add", "#31: webhook with HMAC and a replay window", "--size", "m", "--issue", "40", "--slug", "webhook-hmac", "--release", "2.19.11"),
		"Added backlog/webhook-hmac (order 1) to c")
	mustContain(t, run("add", "second task", "--order", "5", "--model", "Sonnet"), "Added backlog/second-task (order 5)")
	mustContain(t, run("add", "second task"), "Already in the backlog: backlog/second-task")
	mustContain(t, run("add", "third task"), "(order 6)")

	d, _ := srv.doc("backlog/webhook-hmac")
	if strings.Join(d.Meta["issues"], ",") != "40,31" || d.Meta["size"][0] != "M" || d.Meta["release"][0] != "2.19.11" {
		t.Fatalf("%v", d.Meta)
	}
	peek := run("next", "--peek")
	mustContain(t, peek, "webhook-hmac (size M, order 1)")
	mustContain(t, peek, "#31: webhook with HMAC")
	if d, _ := srv.doc("backlog/webhook-hmac"); d.Meta["status"][0] != statusTodo {
		t.Fatal("--peek took the row")
	}
	mustContain(t, run("next"), "webhook-hmac (size M, order 1)")
	mustContain(t, run("next"), "second-task (size -, order 5)")

	out := run("done", "backlog/webhook-hmac", "--commit", "3f2a9c1", "--note", "HMAC done; replay window left")
	mustContain(t, out, "Done: webhook-hmac")
	mustContain(t, out, "Progress note: progress/")
	if d, _ := srv.doc("backlog/webhook-hmac"); d.Meta["status"][0] != statusDone || d.Meta["commit"][0] != "3f2a9c1" {
		t.Fatalf("%v", d.Meta)
	}
	mustContain(t, run("done", "second-task"), "Done: second-task")

	list := run("list")
	mustContain(t, list, "Order Status Size Slug")
	mustContain(t, list, "1     done   M    webhook-hmac")
	mustContain(t, list, "6     todo   -    third-task")
	todo := run("list", "--status", "todo")
	if strings.Contains(todo, "webhook-hmac") || !strings.Contains(todo, "third-task") {
		t.Fatalf("--status todo:\n%s", todo)
	}
	// Rows added by hand export under the default columns.
	if got := run("export"); got != "| Prompt | Size | Model |\n|---|---|---|\n| third task |  |  |\n" {
		t.Fatalf("export:\n%s", got)
	}
}

func TestPlanCLI_JSON(t *testing.T) {
	srv := newPlanServer(t)
	run := func(args ...string) string {
		t.Helper()
		out, err := runCLI(t, srv.URL, append([]string{"--json", "plan", "-c", "c"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	var doc mddb.Document
	if err := json.Unmarshal([]byte(run("add", "a task")), &doc); err != nil || doc.Key != "backlog/a-task" {
		t.Fatal(doc, err)
	}
	if strings.TrimSpace(run("next", "--peek")) == "null" {
		t.Fatal("peek found nothing")
	}
	if err := json.Unmarshal([]byte(run("next")), &doc); err != nil || doc.Meta["status"][0] != statusDoing {
		t.Fatal(doc, err)
	}
	if strings.TrimSpace(run("next")) != "null" {
		t.Fatal("an empty backlog is null")
	}
	if err := json.Unmarshal([]byte(run("done", "a-task", "--note", "n")), &doc); err != nil || doc.Key == "backlog/a-task" {
		t.Fatal(doc, err)
	}
	if err := json.Unmarshal([]byte(run("done", "a-task")), &doc); err != nil || doc.Meta["status"][0] != statusDone {
		t.Fatal(doc, err)
	}
	var docs []mddb.Document
	if err := json.Unmarshal([]byte(run("list")), &docs); err != nil || len(docs) != 1 {
		t.Fatal(docs, err)
	}
}

func TestPlanCLI_Errors(t *testing.T) {
	srv := newPlanServer(t)
	fails := func(want string, args ...string) {
		t.Helper()
		_, err := runCLI(t, srv.URL, append([]string{"plan", "-c", "c"}, args...)...)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: %v, want %q", args, err, want)
		}
	}
	fails("--size is S, M or L", "add", "x", "--size", "XL")
	fails("backlog/nope", "done", "nope")
	fails("missing.md", "import", filepath.Join(t.TempDir(), "missing.md"))
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("no table"), 0o600); err != nil {
		t.Fatal(err)
	}
	fails("no Markdown table", "import", empty)
	srv.setFail("/v1/aggregate", true)
	fails("boom", "add", "x")
	fails("boom", "next")
	srv.setFail("/v1/aggregate", false)
	srv.setFail("/v1/add", true)
	fails("boom", "add", "x", "--order", "1")
	srv.setFail("/v1/add", false)
	if _, err := runCLI(t, srv.URL, "plan", "-c", "c", "add", "x"); err != nil {
		t.Fatal(err)
	}
	srv.setFail("/v1/add", true)
	fails("boom", "done", "x", "--commit", "c")
	srv.setFail("/v1/add", false)
	srv.setFail("/v1/search", true)
	fails("boom", "list")
	fails("boom", "export")
}

// The note's write can fail after the row was marked done.
func TestPlanCLI_NoteFails(t *testing.T) {
	srv := newPlanServer(t)
	if _, err := runCLI(t, srv.URL, "plan", "-c", "c", "add", "x"); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.failKeyPrefix = "progress/"
	srv.mu.Unlock()
	if _, err := runCLI(t, srv.URL, "plan", "-c", "c", "done", "x", "--note", "n"); err == nil {
		t.Fatal("note failure ignored")
	}
}

// Without -c the collection is the one autoindex picks, plus -work.
func TestPlanFlags_DefaultCollection(t *testing.T) {
	st, err := planFlags{lang: "en"}.store()
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs(".")
	if want := autoindexCollection("", root, detectGit(root)) + "-work"; st.collection != want {
		t.Fatalf("%s, want %s", st.collection, want)
	}
	orig := workDir
	t.Cleanup(func() { workDir = orig })
	workDir = func() (string, error) { return "", errors.New("no working directory") }
	if _, err := runCLI(t, "http://127.0.0.1:1", "plan", "list"); err == nil || !strings.Contains(err.Error(), "no working directory") {
		t.Fatal(err)
	}
}
