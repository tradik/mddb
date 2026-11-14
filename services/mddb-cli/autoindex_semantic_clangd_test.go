package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mddb-cli/internal/lspindex"
)

// autoindex --semantic for C and C++ (#418): the fake language server is
// installed as clangd on PATH, so the table's command line is used.

const clangdLangs = `{"extensions":{".c":"c",".h":"c",".cpp":"cpp"},"languages":["c","cpp"],"semantic":{"c":true,"cpp":true,"go":true}}`

// cTree is a C file, its header and a C++ file; the database is written by
// the test where it wants one.
func clangdTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{
		"net.c":     []byte("#include \"net.h\"\n\nstatic int init(void)\n{\n\treturn 0;\n}\n\nint start(void) { return init(); }\n"),
		"net.h":     []byte("int start(void);\n"),
		"shape.cpp": []byte("int area() { return 1; }\n"),
	})
	return dir
}

// useFakeClangd puts the fake on PATH as clangd, playing script, and
// returns the file it records what it saw in.
func useFakeClangd(t *testing.T, script map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "record.json")
	script["record"] = rec
	data, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	exe := "clangd" + filepath.Ext(fakeLSP(t)) // .exe on Windows
	src, err := os.ReadFile(fakeLSP(t))        // a copy, not a symlink: Windows CI may not allow links
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, exe), src, 0o700); err != nil { // #nosec G306 -- a test program must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("FAKELSP_SCRIPT", path)
	for _, v := range []string{"MDDB_LSP_C", "MDDB_LSP_CPP"} {
		t.Setenv(v, "")
	}
	return rec
}

func clangdScript() map[string]any {
	static := lspSym("init", 12, 2, 5)
	static["tags"] = []int{8, 18, 19}
	start := lspSym("start", 12, 7, 7)
	start["tags"] = []int{18, 19}
	proto := lspSym("start", 12, 0, 0)
	proto["tags"] = []int{18} // a prototype: no definition
	return map[string]any{
		"serverInfo":    map[string]string{"name": "clangd", "version": "clangd version 23.1.0 (https://github.com/llvm/llvm-project ea7d852a)"},
		"progressToken": "backgroundIndexProgress",
		"symbols": map[string]any{
			"net.c": []any{static, start}, "net.h": []any{proto}, "shape.cpp": []any{lspSym("area", 12, 0, 0)},
		},
		"references": map[string]any{"net.c:7": []map[string]any{{"path": "net.h", "line": 0, "char": 4}}},
	}
}

func recordedArgv(t *testing.T, rec string) []string {
	t.Helper()
	data, err := os.ReadFile(rec) // #nosec G304 -- the fake's record in this test's temp dir
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r.Argv
}

func TestSemantic_ClangdOneServerForCAndCpp(t *testing.T) {
	dir := clangdTree(t)
	writeTree(t, dir, map[string][]byte{"build/compile_commands.json": []byte("[]")})
	rec := useFakeClangd(t, clangdScript())
	srv := newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) { f.langs = clangdLangs })

	rep := runReport(t, srv.URL, "--semantic=c,cpp", dir)
	s := rep.Semantic
	if len(s.Languages) != 1 || s.Languages[0].Language != "c+cpp" || s.Languages[0].Server != "clangd/23.1.0" || s.Languages[0].Files != 3 {
		t.Fatalf("one clangd for both: %+v", s)
	}
	argv := recordedArgv(t, rec)
	want := "--compile-commands-dir=" + filepath.Join(dir, "build")
	if !slices.Equal(argv, []string{"--background-index", "--log=info", want}) {
		t.Errorf("argv = %q, want %s last", argv, want)
	}
	// The prototype is no definition; the call in the header is a reference.
	if got := storedMeta(srv, "net.h"); len(got["symDefs"]) != 0 || strings.Join(got["symRefs"], "|") != "net.c#start 1" {
		t.Errorf("net.h meta: %v", got)
	}
}

func TestSemantic_ClangdNamedDatabase(t *testing.T) {
	dir := clangdTree(t)
	elsewhere := filepath.Join(t.TempDir(), "out")
	writeTree(t, elsewhere, map[string][]byte{"compile_commands.json": []byte("[]")})
	rec := useFakeClangd(t, clangdScript())
	srv := newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) { f.langs = clangdLangs })

	rep := runReport(t, srv.URL, "--semantic=c", "--compile-commands", filepath.Join(elsewhere, "compile_commands.json"), dir)
	if l := rep.Semantic.Languages; len(l) != 1 || l[0].Language != "c" || l[0].Files != 2 {
		t.Fatalf("c alone: %+v", rep.Semantic)
	}
	if argv := recordedArgv(t, rec); argv[len(argv)-1] != "--compile-commands-dir="+elsewhere {
		t.Errorf("argv = %q", argv)
	}
}

func TestSemantic_ClangdWithoutDatabase(t *testing.T) {
	dir := clangdTree(t)
	useFakeClangd(t, clangdScript())

	srv := newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) { f.langs = clangdLangs })
	rep := runReport(t, srv.URL, "--semantic", dir)
	s := rep.Semantic
	if len(s.Languages) != 0 || len(s.Warnings) != 2 || !strings.Contains(s.Warnings[0], "no compilation database") ||
		!strings.Contains(s.Warnings[0], "--compile-commands") || !strings.HasSuffix(s.Warnings[0], "; c files keep the heuristics") {
		t.Fatalf("auto without a database: %+v", s)
	}
	if _, ok := storedMeta(srv, "net.c")["symIndex"]; ok || rep.Added != 3 {
		t.Fatalf("documents go without facts: %+v", rep)
	}

	srv = newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) { f.langs = clangdLangs })
	_, err := runCLI(t, srv.URL, "autoindex", "--semantic=c", dir)
	if !errors.Is(err, lspindex.ErrNoCompileCommands) || writes(srv) != 0 {
		t.Fatalf("a named language without a database: %v, %d writes", err, writes(srv))
	}
}

func TestJoinPlans(t *testing.T) {
	c, _ := lspindex.Spec("c")
	cpp, _ := lspindex.Spec("cpp")
	goSpec, _ := lspindex.Spec("go")
	plans := []semanticPlan{
		{language: "c", spec: c, files: []string{"a.c"}, argv: []string{"clangd"}, compileCommands: "/b"},
		{language: "go", spec: goSpec, files: []string{"a.go"}, argv: []string{"gopls"}},
		// MDDB_LSP_CPP names another clangd: two runs.
		{language: "cpp", spec: cpp, files: []string{"a.cpp"}, argv: []string{"/opt/clangd"}, compileCommands: "/b"},
	}
	if got := joinPlans(plans); len(got) != 3 {
		t.Errorf("different commands joined: %+v", got)
	}
	plans[2].argv = []string{"clangd"}
	got := joinPlans(plans)
	if len(got) != 2 || got[0].language != "c+cpp" || strings.Join(got[0].files, ",") != "a.c,a.cpp" || got[1].language != "go" {
		t.Errorf("joined: %+v", got)
	}
}
