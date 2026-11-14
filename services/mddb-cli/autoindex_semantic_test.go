package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// autoindex --semantic end to end (CODE-011): the fake language server
// answers, the fake mddbd keeps what it is sent.

const (
	semA = "package x\n\n// Run runs.\nfunc Run() {\n}\n"
	semB = "package x\n\nfunc Main() {\n\tRun()\n\tRun()\n}\n"
)

// lspSym is a DocumentSymbol spanning lines l1..l2 (0-based), named at l1.
func lspSym(name string, kind, l1, l2 int) map[string]any {
	pos := func(line, char int) map[string]int { return map[string]int{"line": line, "character": char} }
	return map[string]any{"name": name, "kind": kind,
		"range":          map[string]any{"start": pos(l1, 0), "end": pos(l2, 1)},
		"selectionRange": map[string]any{"start": pos(l1, 5), "end": pos(l1, 5+len(name))}}
}

// goScript: b.go calls a.go's Run on the given 0-based lines.
func goScript(refLines ...int) map[string]any {
	var refs []map[string]any
	for _, l := range refLines {
		refs = append(refs, map[string]any{"path": "b.go", "line": l, "char": 1})
	}
	return map[string]any{
		"serverInfo": map[string]string{"name": "gopls", "version": "v0.24.0"},
		"symbols": map[string]any{
			"a.go": []any{lspSym("Run", 12, 3, 4)},
			"b.go": []any{lspSym("Main", 12, 2, 5)},
		},
		"references": map[string]any{"a.go:3": refs},
	}
}

// semanticTree is two Go files and a Markdown one.
func semanticTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{"a.go": []byte(semA), "b.go": []byte(semB), "README.md": []byte("# x\n")})
	return dir
}

// runReport runs autoindex with --json and decodes the report.
func runReport(t *testing.T, url string, args ...string) autoindexReport {
	t.Helper()
	out, err := runCLI(t, url, append([]string{"--json", "autoindex", "-c", "sem"}, args...)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var rep autoindexReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return rep
}

func ingestedKeys(srv *fakeMddbd) string {
	return srv.read(func(f *fakeMddbd) any { return strings.Join(f.ingested, ",") }).(string)
}

func storedMeta(srv *fakeMddbd, key string) map[string][]string {
	return srv.read(func(f *fakeMddbd) any { return f.docs[key].Meta }).(map[string][]string)
}

func TestSemantic_FullRunThenNothingThenFactsOnly(t *testing.T) {
	dir := semanticTree(t)
	srv := newFakeMddbd(t)
	useFakeGopls(t, goScript(3, 4))

	rep := runReport(t, srv.URL, "--semantic=go", dir)
	s := rep.Semantic
	if s == nil || s.Mode != "list" || len(s.Languages) != 1 || s.Sent != 2 || s.Unchanged != 0 {
		t.Fatalf("first run: %+v", s)
	}
	l := s.Languages[0]
	if l.Language != "go" || l.Server != "gopls/0.24.0" || l.Files != 2 || l.Defs != 2 || l.Refs != 2 {
		t.Fatalf("language section: %+v", l)
	}
	b := storedMeta(srv, "b.go")
	if got := strings.Join(b["symRefs"], "|"); got != "a.go#Run 4,5" || len(b["symIndex"]) != 1 || b["path"][0] != "b.go" {
		t.Fatalf("b.go meta: %v", b)
	}
	if _, ok := storedMeta(srv, "README.md")["symIndex"]; ok {
		t.Fatal("a Markdown file has no facts")
	}

	// Nothing changed: no document with facts goes out; README.md goes to
	// ingest as it always did, and ingest skips it.
	srv.set(func(f *fakeMddbd) { f.ingested = nil })
	rep = runReport(t, srv.URL, "--semantic", dir)
	if s := rep.Semantic; s.Mode != "auto" || s.Sent != 0 || s.Unchanged != 2 || s.FactsUpdated != 0 {
		t.Fatalf("second run: %+v", s)
	}
	if got := ingestedKeys(srv); got != "README.md" {
		t.Fatalf("second run ingested %q", got)
	}
	if rep.Unchanged != 3 || rep.Added+rep.Updated != 0 {
		t.Fatalf("totals: %+v", rep)
	}

	// The server now reports one reference fewer while no file changed
	// (as when another file's edit changes what a symbol resolves to): only
	// b.go's meta is rewritten — all of it, a key added by someone else too.
	srv.set(func(f *fakeMddbd) { f.ingested = nil; f.docs["b.go"].Meta["owner"] = []string{"team"} })
	useFakeGopls(t, goScript(3))
	rep = runReport(t, srv.URL, "--semantic=go", dir)
	if s := rep.Semantic; s.Sent != 1 || s.Unchanged != 1 || s.FactsUpdated != 1 {
		t.Fatalf("facts-only run: %+v", s)
	}
	if rep.Updated != 1 || rep.Unchanged != 2 {
		t.Fatalf("totals: %+v", rep)
	}
	patches := srv.read(func(f *fakeMddbd) any { return f.patches }).([]map[string]any)
	if len(patches) != 1 || patches[0]["key"] != "b.go" || patches[0]["contentMd"] != nil {
		t.Fatalf("patches: %v", patches)
	}
	b = storedMeta(srv, "b.go")
	if got := strings.Join(b["symRefs"], "|"); got != "a.go#Run 4" || b["owner"][0] != "team" || b["kind"][0] != "code" {
		t.Fatalf("meta after the facts-only update: %v", b)
	}
}

func TestSemantic_TextReportAndDryRun(t *testing.T) {
	dir := semanticTree(t)
	srv := newFakeMddbd(t)
	useFakeGopls(t, goScript(3))
	out, err := runCLI(t, srv.URL, "autoindex", "-c", "sem", "--dry-run", "--semantic=go", dir)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "  semantic go: gopls/0.24.0 — 2 files, 2 defs, 1 refs, 0 impls, 0 skipped (0 timed out), 0 unencodable in ")
	if strings.Contains(out, "documents sent with facts") {
		t.Errorf("a dry run sends nothing:\n%s", out)
	}
	if n := srv.read(func(f *fakeMddbd) any { return f.requests["POST /v1/ingest"] + f.requests["PUT /v1/collection-config"] }); n != 0 {
		t.Fatalf("dry run wrote %v times", n)
	}

	out, err = runCLI(t, srv.URL, "autoindex", "-c", "sem", "--semantic=go", "--semantic-concurrency", "2",
		"--semantic-timeout", "1m", "--semantic-memory-mb", "512", dir)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "  semantic: 2 documents sent with facts, 0 unchanged, 0 with only their facts updated")
}
