package lspindex

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClangdNormalize(t *testing.T) {
	cases := map[string]string{
		"init":                         "init",
		"Square::area":                 "Square.area",
		"geo::detail::Square::Square":  "geo.detail.Square.Square",
		"List<T>::push":                "List.push",
		"Map<K, std::pair<K, V>>::get": "Map.get",
		"Vec<T>::operator<<":           "Vec.operator<<",
		"operator==":                   "operator==",
		"~Shape":                       "~Shape",
		"(anonymous namespace)":        "",
		"(unnamed struct at a.c:3:1)":  "",
		"myoperator":                   "myoperator",
	}
	for in, want := range cases {
		if got := clangdNormalize(in); got != want {
			t.Errorf("clangdNormalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClangdClassify(t *testing.T) {
	cases := []struct {
		kind   string
		tags   []int
		detail string
		want   string
	}{
		{"function", []int{tagDeclaration}, "int (void)", ""},                                  // header prototype
		{"function", []int{8, tagDeclaration, tagDefinition}, "int (void)", "function"},        // static definition
		{"method", []int{5, tagAbstract, 15, tagDeclaration, 20}, "double () const", "method"}, // pure virtual
		{"constructor", []int{5, tagDeclaration}, "(double)", ""},                              // declared in class
		{"function", nil, "int (void)", "function"},                                            // clangd without tags
		{"struct", []int{tagDeclaration}, "type alias", "type"},                                // typedef
		{"struct", []int{tagDeclaration}, "struct", "struct"},                                  // a C struct has no Definition tag
	}
	for _, c := range cases {
		if got := clangdClassify(&documentSymbol{Tags: c.tags, Detail: c.detail}, c.kind); got != c.want {
			t.Errorf("clangdClassify(%s %v %q) = %q, want %q", c.kind, c.tags, c.detail, got, c.want)
		}
	}
}

func TestClangdFallbackLines(t *testing.T) {
	cases := map[string]string{
		"I[11:26:19.331] Failed to find compilation database for /src/net.c":                          "/src/net.c",
		"I[11:26:19.331] ASTWorker building file /src/a b.c version 1 with command clangd fallback\r": "/src/a b.c",
		"I[11:26:19.331] ASTWorker building file /src/net.c version 1 with command inferred from x.c": "",
		"I[11:26:19.331] ASTWorker building file with command clangd fallback":                        "",
		"I[11:26:31.867] Loaded compilation database from /src/compile_commands.json":                 "",
	}
	for in, want := range cases {
		if got := clangdFallback(in); got != want {
			t.Errorf("clangdFallback(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClangdLanguageIDAndArgs(t *testing.T) {
	for path, want := range map[string]string{"a.c": "c", "a.h": "c", "x/a.CPP": "cpp", "a.hh": "cpp", "a.hxx": "cpp"} {
		if got := clangdLanguageID(path); got != want {
			t.Errorf("clangdLanguageID(%q) = %q", path, got)
		}
	}
	if got := clangdArgs(Options{}); got != nil {
		t.Errorf("args without a database = %q", got)
	}
	spec, _ := Spec("c")
	// A program the operator named that is not clangd gets no clangd flags.
	if got := spec.command(Candidate{Argv: []string{"/opt/ccls"}}, Options{CompileCommandsDir: "/b"}); !reflect.DeepEqual(got, []string{"/opt/ccls"}) {
		t.Errorf("command = %q", got)
	}
	if spec.Group != "clangd" || !spec.CompileCommands || !spec.WaitIndexed {
		t.Errorf("spec = %+v", spec)
	}
}

func TestFindCompileCommands(t *testing.T) {
	root := t.TempDir()
	if _, err := FindCompileCommands(root, ""); !errors.Is(err, ErrNoCompileCommands) || !strings.Contains(err.Error(), "build") {
		t.Errorf("empty tree: %v", err)
	}
	build := filepath.Join(root, "build")
	writeFile(t, filepath.Join(build, compileCommandsFile), "[]")
	if dir, err := FindCompileCommands(root, ""); err != nil || dir != build {
		t.Errorf("build/: %q, %v", dir, err)
	}
	writeFile(t, filepath.Join(root, compileCommandsFile), "[]")
	if dir, err := FindCompileCommands(root, ""); err != nil || dir != root {
		t.Errorf("root wins: %q, %v", dir, err)
	}
	// Named: the directory or the file.
	for _, given := range []string{build, filepath.Join(build, compileCommandsFile)} {
		if dir, err := FindCompileCommands(root, given); err != nil || dir != build {
			t.Errorf("%s: %q, %v", given, dir, err)
		}
	}
	// A directory named compile_commands.json is not the database.
	odd := filepath.Join(root, "odd", compileCommandsFile)
	if err := os.MkdirAll(odd, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := FindCompileCommands(root, filepath.Join(root, "odd")); !errors.Is(err, ErrNoCompileCommands) {
		t.Errorf("a directory for a file: %v", err)
	}
	if _, err := FindCompileCommands(root, filepath.Join(root, "nowhere")); !errors.Is(err, ErrNoCompileCommands) {
		t.Errorf("a missing directory: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(l string) { lines = append(lines, l) }}
	for _, chunk := range []string{"one\ntw", "o\n", "thr", "ee\nrest"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if !reflect.DeepEqual(lines, []string{"one", "two", "three"}) || string(w.buf) != "rest" {
		t.Errorf("lines = %q, rest %q", lines, w.buf)
	}
	// An endless line is dropped, not kept growing.
	_, _ = w.Write([]byte(strings.Repeat("x", maxStderrLine+1)))
	_, _ = w.Write([]byte("\nlast\n"))
	if !reflect.DeepEqual(lines, []string{"one", "two", "three", "", "last"}) {
		t.Errorf("lines = %q", lines)
	}
}

func TestFallbacksKeepFew(t *testing.T) {
	f := &fallbacks{read: clangdFallback}
	for i := range maxFallbacks + 2 {
		f.line("Failed to find compilation database for /src/" + strings.Repeat("a", i+1) + ".c")
		f.line("Failed to find compilation database for /src/a.c")
		f.line("noise")
	}
	if got := f.list(); len(got) != maxFallbacks || got[0] != "/src/a.c" {
		t.Errorf("files = %q", got)
	}
	// A path that cannot be made relative to the root is no indexed file.
	r := &run{root: t.TempDir(), opened: map[string]bool{"a.c": true}, fallbacks: &fallbacks{files: []string{"a.c"}}}
	if err := r.fallbackError(); err != nil {
		t.Errorf("relative path: %v", err)
	}
}
