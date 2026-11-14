package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mddb-cli/internal/lspindex"
)

// What --semantic does when something is missing or fails: auto goes on
// without facts and says so; a list stops before anything is written.

// noLanguageServers leaves PATH and the MDDB_LSP_* variables empty.
func noLanguageServers(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	for _, v := range []string{"MDDB_LSP_GO", "MDDB_LSP_PYTHON", "MDDB_LSP_PHP"} {
		t.Setenv(v, "")
	}
}

// writes counts the requests that change something.
func writes(srv *fakeMddbd) int {
	return srv.read(func(f *fakeMddbd) any {
		return f.requests["POST /v1/ingest"] + f.requests["PUT /v1/collection-config"] + f.requests["PATCH /v1/update"]
	}).(int)
}

func TestSemantic_MissingServer(t *testing.T) {
	dir := semanticTree(t)
	writeTree(t, dir, map[string][]byte{"tool.py": []byte("def run():\n    pass\n")})
	noLanguageServers(t)

	srv := newFakeMddbd(t)
	rep := runReport(t, srv.URL, "--semantic", dir)
	if s := rep.Semantic; len(s.Languages) != 0 || len(s.Warnings) != 2 ||
		!strings.Contains(s.Warnings[0], "language server not found for go") || !strings.Contains(s.Warnings[0], "go files keep the heuristics") {
		t.Fatalf("auto without servers: %+v", s)
	}
	if _, ok := storedMeta(srv, "a.go")["symIndex"]; ok || rep.Added != 4 {
		t.Fatalf("documents go without facts: %+v", rep)
	}

	srv = newFakeMddbd(t)
	_, err := runCLI(t, srv.URL, "autoindex", "--semantic=go", dir)
	if !errors.Is(err, lspindex.ErrNoServer) || writes(srv) != 0 {
		t.Fatalf("a named language without a server: %v, %d writes", err, writes(srv))
	}
}

func TestSemantic_ServerFails(t *testing.T) {
	dir := semanticTree(t)
	sc := goScript(3)
	sc["die"] = "documentSymbol b.go"
	sc["stderr"] = "fake: crashed on purpose\n"
	useFakeGopls(t, sc)

	srv := newFakeMddbd(t)
	out, err := runCLI(t, srv.URL, "autoindex", "--semantic", dir)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "  semantic go: no facts")
	mustContain(t, out, "  ! semantic: go files go without semantic facts: ")
	mustContain(t, out, "crashed on purpose")
	if _, ok := storedMeta(srv, "a.go")["symIndex"]; ok {
		t.Fatal("no facts from a server that died")
	}

	srv = newFakeMddbd(t)
	_, err = runCLI(t, srv.URL, "autoindex", "--semantic=go", dir)
	var se *lspindex.ServerError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "semantic facts for go") || writes(srv) != 0 {
		t.Fatalf("a named language whose server fails: %v, %d writes", err, writes(srv))
	}
}

func TestSemantic_ServerWithoutSemantic(t *testing.T) {
	dir := semanticTree(t)
	useFakeGopls(t, goScript(3))
	old := `{"extensions":{".go":"go"},"languages":["go"]}`

	srv := newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) { f.langs = old })
	rep := runReport(t, srv.URL, "--semantic", dir)
	if s := rep.Semantic; len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "predates 2.19.3") || rep.Added != 3 {
		t.Fatalf("auto against an old server: %+v %+v", s, rep)
	}

	srv = newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) { f.langs = old })
	if _, err := runCLI(t, srv.URL, "autoindex", "--semantic=go", dir); !errors.Is(err, errNotAnnounced) || writes(srv) != 0 {
		t.Fatalf("a list against an old server: %v", err)
	}
}

func TestSemantic_PlanEdges(t *testing.T) {
	// go must resolve, so that the run reaches rust; the machine running the
	// tests need not have gopls.
	t.Setenv("MDDB_LSP_GO", fakeLSP(t))
	files := []autoFile{{Rel: "a.go", Language: "go"}, {Rel: "doc.pdf", Language: "go", Format: "pdf"}}
	announced := map[string]bool{"go": true}

	rep := &semanticReport{}
	if _, err := planSemantic(semanticOptions{langs: "go,rust"}, files, announced, rep); !errors.Is(err, lspindex.ErrUnsupportedLanguage) {
		t.Errorf("unsupported language: %v", err)
	}
	rep = &semanticReport{}
	plans, err := planSemantic(semanticOptions{langs: " PHP , php,,"}, files, map[string]bool{"php": true}, rep)
	if err != nil || len(plans) != 0 || len(rep.Warnings) != 1 || rep.Warnings[0] != "no php files to index" {
		t.Errorf("a named language with no files: %v %v %v", plans, err, rep.Warnings)
	}
	// auto skips languages the server does not keep facts for, silently.
	rep = &semanticReport{}
	plans, err = planSemantic(semanticOptions{langs: semanticAuto}, files, map[string]bool{"python": true}, rep)
	if err != nil || len(plans) != 0 || len(rep.Warnings) != 0 {
		t.Errorf("auto, not announced: %v %v %v", plans, err, rep.Warnings)
	}
	// A converted file is not source: only a.go is indexed.
	plans, err = planSemantic(semanticOptions{langs: "go", lookPath: func(n string) (string, error) { return "/bin/" + n, nil }}, files, announced, &semanticReport{})
	if err != nil || len(plans) != 1 || strings.Join(plans[0].files, ",") != "a.go" {
		t.Errorf("plans: %+v %v", plans, err)
	}
	if got := splitLanguages("go, Python ,go"); strings.Join(got, ",") != "go,python" {
		t.Errorf("splitLanguages = %v", got)
	}
	if facts, err := semanticFacts(context.Background(), "", nil, &autoindexReport{}, semanticOptions{}); facts != nil || err != nil {
		t.Error("off means nothing")
	}
}
