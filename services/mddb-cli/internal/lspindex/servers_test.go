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

// TestServerArgv pins each language's launch command: the table's programs
// are links to the fake, which records its argv. The commands were checked
// against each server's documentation; a change here should be checked again.
func TestServerArgv(t *testing.T) {
	cases := []struct {
		lang      string
		installed []string
		wantProg  string
		wantArgs  []string
		wantName  string
	}{
		{"go", []string{"gopls"}, "gopls", []string{"serve"}, "gopls/1.0"},
		// basedpyright is preferred: it finds class implementations.
		{"python", []string{"pyrefly", "basedpyright-langserver"}, "basedpyright-langserver", []string{"--stdio"}, "basedpyright/1.0"},
		{"python", []string{"pyrefly"}, "pyrefly", []string{"lsp", "--indexing-mode", "lazy-blocking"}, "pyrefly/1.0"},
		{"php", []string{"phpactor", "phpantom_lsp"}, "phpactor", []string{"language-server"}, "phpactor/1.0"},
		{"php", []string{"phpantom_lsp"}, "phpantom_lsp", []string{}, "phpantom/1.0"},
	}
	for _, c := range cases {
		t.Run(c.lang+"/"+c.wantProg, func(t *testing.T) {
			spec, err := Spec(c.lang)
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			for _, name := range c.installed {
				if err := os.Symlink(fakePath(t), filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			f := newFakeRun(t, map[string]string{"x.src": "x\n"}, fakeScript{
				ServerInfo: map[string]string{"name": "reported-name", "version": "1.0"},
				Symbols:    map[string]any{"x.src": []any{sym("X", 5, 0, 0, 0, 0)}},
			})
			t.Setenv("FAKELSP_SCRIPT", f.script)
			opt := Options{
				Getenv: func(string) string { return "" },
				LookPath: func(name string) (string, error) {
					p := filepath.Join(bin, name)
					if _, err := os.Lstat(p); err != nil {
						return "", err
					}
					return p, nil
				},
			}
			res, err := Index(context.Background(), f.root, []string{"x.src"}, spec, opt)
			if err != nil {
				t.Fatal(err)
			}
			rec := f.record(t)
			if !reflect.DeepEqual(rec.Argv, c.wantArgs) {
				t.Errorf("argv = %q, want %q", rec.Argv, c.wantArgs)
			}
			if res.Server != c.wantName {
				t.Errorf("Server = %q, want %q", res.Server, c.wantName)
			}
			if spec.LanguageID != c.lang || spec.EnvVar != "MDDB_LSP_"+strings.ToUpper(c.lang) {
				t.Errorf("LanguageID %q, EnvVar %q", spec.LanguageID, spec.EnvVar)
			}
		})
	}
}

func TestEnvOverrideSplitsArgv(t *testing.T) {
	spec := goSpec(t)
	var looked string
	cand, err := spec.resolve(
		func(k string) string { return map[string]string{"MDDB_LSP_GO": "  mygopls  -rpc.trace   serve "}[k] },
		func(name string) (string, error) { looked = name; return "/opt/bin/" + name, nil })
	if err != nil {
		t.Fatal(err)
	}
	if looked != "mygopls" || !reflect.DeepEqual(cand.Argv, []string{"/opt/bin/mygopls", "-rpc.trace", "serve"}) || cand.Name != "" {
		t.Errorf("candidate = %+v", cand)
	}
	// The table's program at another path keeps the table's name.
	py, _ := Spec("python")
	cand, err = py.resolve(func(string) string { return "/venv/bin/basedpyright-langserver --stdio" },
		func(name string) (string, error) { return name, nil })
	if err != nil || cand.Name != "basedpyright" {
		t.Errorf("candidate = %+v, %v", cand, err)
	}
}

func TestLanguages(t *testing.T) {
	if got := Languages(); !reflect.DeepEqual(got, []string{"go", "php", "python"}) {
		t.Errorf("Languages = %v", got)
	}
}

func TestServerName(t *testing.T) {
	cases := []struct {
		cand Candidate
		info serverInfo
		want string
	}{
		{Candidate{Name: "gopls", Argv: []string{"/x/gopls"}}, serverInfo{Version: `{"Version":"v0.24.0"}`}, "gopls/0.24.0"},
		{Candidate{Argv: []string{"/x/srv"}}, serverInfo{Name: "my server", Version: "2 beta|1#x"}, "my-server/2-beta-1-x"},
		{Candidate{Argv: []string{"/x/srv"}}, serverInfo{}, "srv/unknown"},
		{Candidate{Argv: []string{"/x/srv"}}, serverInfo{Version: "{broken"}, "srv/unknown"},
	}
	for _, c := range cases {
		if got := serverName(c.cand, c.info); got != c.want {
			t.Errorf("serverName(%+v, %+v) = %q, want %q", c.cand, c.info, got, c.want)
		}
	}
}

func TestGoNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"(*Server).LookupSymbol": "Server.LookupSymbol",
		"(Server).Close":         "Server.Close",
		"(*List[T]).Push":        "List.Push",
		"(*Map[K, V]).Get":       "Map.Get",
		"flush":                  "flush",
		"(broken":                "(broken",
	} {
		if got := goNormalize(in); got != want {
			t.Errorf("goNormalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoSkipRefs(t *testing.T) {
	cases := []struct {
		path, name, kind string
		want             bool
	}{
		{"a_test.go", "TestX", "function", true},
		{"a_test.go", "Test", "function", true},
		{"a_test.go", "Test_x", "function", true},
		{"a_test.go", "BenchmarkX", "function", true},
		{"a_test.go", "FuzzX", "function", true},
		{"a_test.go", "Example", "function", true},
		{"a_test.go", "ExampleIndex_second", "function", true},
		{"a_test.go", "Testify", "function", false},
		{"a_test.go", "helper", "function", false},
		{"a_test.go", "TestX", "method", false},
		{"a.go", "TestX", "function", false},
	}
	for _, c := range cases {
		if got := goSkipRefs(c.path, Def{Name: c.name, Kind: c.kind}); got != c.want {
			t.Errorf("goSkipRefs(%s, %s %s) = %v", c.path, c.kind, c.name, got)
		}
	}
}

func TestGoSettings(t *testing.T) {
	if goSettings(Options{}) != nil {
		t.Error("settings without a memory limit")
	}
	if got := goSettings(Options{MemoryLimitMb: 2}); got["memoryLimit"] != int64(2<<20) {
		t.Errorf("settings = %v", got)
	}
}

func TestPHPNamespace(t *testing.T) {
	src := []byte("<?php\nnamespace App\\Http;\nclass A {}\nnamespace \\Other\\ ;\nclass B {}\n  namespace Third {\n}\nnamespace \\ {\n")
	ns := phpNamespace(src)
	for line, want := range map[int]string{0: "", 1: `App\Http\`, 2: `App\Http\`, 4: `Other\`, 5: `Third\`, 7: "", 9: ""} {
		if got := ns(line); got != want {
			t.Errorf("line %d: %q, want %q", line, got, want)
		}
	}
	if got := phpNamespace([]byte("<?php class A {}"))(3); got != "" {
		t.Errorf("no namespace: %q", got)
	}
}

func TestResolveReportsEveryCandidate(t *testing.T) {
	spec, _ := Spec("python")
	_, err := spec.resolve(func(string) string { return "" }, func(string) (string, error) { return "", errors.New("no") })
	if !errors.Is(err, ErrNoServer) || !strings.Contains(err.Error(), "basedpyright-langserver --stdio; pyrefly lsp --indexing-mode lazy-blocking") {
		t.Errorf("err = %v", err)
	}
}
