package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndToEnd(t *testing.T) {
	base := newFake(t, &fakeMDDB{export: fixtureExport, searchDiffers: true, hybridFails: true})
	out := filepath.Join(t.TempDir(), "rep.json")
	var stdout, stderr bytes.Buffer
	code := cli([]string{"-url", base, "-heuristic", "heur", "-semantic", "sem", "-truth", writeFile(t, "t.json", fixtureTruth),
		"-n", "5", "-repeat", "2", "-queries", evalFiles(t), "-out", out, "-baseline", base}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	b, err := os.ReadFile(out) // #nosec G304 -- the test's own file
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		report
		Symbols []map[string]any `json:"symbols"` // shadows report.Symbols: *symbol does not decode
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	all := rep.Groups[len(rep.Groups)-1]
	if len(rep.Symbols) != 5 || rep.Symbols[0]["stratum"] == nil {
		t.Fatalf("%+v", rep.Symbols)
	}
	if rep.Sampled != 5 || all.Name != "all" || len(rep.Groups) != 4 {
		t.Fatalf("%+v", rep.Groups)
	}
	h, s := all.Heuristic, all.Semantic
	if h.Returned != 7 || h.Correct != 3 || h.Truth != 5 || s.Returned != 5 || s.Correct != 5 || s.Truth != 5 {
		t.Fatalf("heuristic %+v\nsemantic %+v", h, s)
	}
	if s.Precision != 1 || s.Recall != 1 || s.Exact != 5 || h.Exact != 1 || s.Truncated != 1 || s.ImplsExact != 5 || h.ImplsExact != 4 {
		t.Fatalf("heuristic %+v\nsemantic %+v", h, s)
	}
	if rep.Meta == nil || rep.Meta.Documents != 2 || rep.Meta.Truncated != 1 {
		t.Fatalf("%+v", rep.Meta)
	}
	if len(rep.Search) != 4 || rep.Search[2].Identical != 4 || rep.Search[0].Identical != 2 || rep.Search[0].SameResults != 3 || len(rep.Search[0].Differ) != 1 {
		t.Fatalf("%+v", rep.Search)
	}
	for _, want := range []string{"| all | 5 | semantic | 100.0% (5/5) | 100.0% (5/5) |", "facts: 2 documents",
		"search /v1/fts, heur@" + base + " vs sem@" + base + ": 2/4 queries byte-identical, 3/4 with the same results", `differs: "differs"`, "error: POST"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("table lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestCLIErrors(t *testing.T) {
	truth := writeFile(t, "t.json", fixtureTruth)
	fine := newFake(t, &fakeMDDB{export: fixtureExport})
	cases := map[string]struct {
		args []string
		code int
	}{
		"bad flag":         {[]string{"-nope"}, 2},
		"missing":          {[]string{"-heuristic", "h"}, 1},
		"no truth file":    {[]string{"-heuristic", "h", "-semantic", "s", "-truth", filepath.Join(t.TempDir(), "none")}, 1},
		"bad truth":        {[]string{"-heuristic", "h", "-semantic", "s", "-truth", writeFile(t, "b.json", "{")}, 1},
		"empty truth":      {[]string{"-heuristic", "h", "-semantic", "s", "-truth", writeFile(t, "e.json", `{"files":{}}`)}, 1},
		"server down":      {[]string{"-url", newFake(t, &fakeMDDB{broken: true}), "-heuristic", "h", "-semantic", "s", "-truth", truth}, 1},
		"symbol rejected":  {[]string{"-url", newFake(t, &fakeMDDB{symbolStatus: 400}), "-heuristic", "h", "-semantic", "s", "-truth", truth}, 1},
		"export fails":     {[]string{"-url", newFake(t, &fakeMDDB{exportStatus: 500}), "-heuristic", "h", "-semantic", "s", "-truth", truth}, 1},
		"bad export":       {[]string{"-url", newFake(t, &fakeMDDB{export: "{\n"}), "-heuristic", "h", "-semantic", "s", "-truth", truth}, 1},
		"no queries file":  {[]string{"-url", fine, "-heuristic", "h", "-semantic", "s", "-truth", truth, "-queries", "/nonexistent.json"}, 1},
		"bad queries":      {[]string{"-url", fine, "-heuristic", "h", "-semantic", "s", "-truth", truth, "-queries", writeFile(t, "q.json", "[")}, 1},
		"empty queries":    {[]string{"-url", fine, "-heuristic", "h", "-semantic", "s", "-truth", truth, "-queries", writeFile(t, "q.json", "{}")}, 1},
		"unwritable out":   {[]string{"-url", fine, "-heuristic", "h", "-semantic", "s", "-truth", truth, "-out", filepath.Join(t.TempDir(), "no", "x.json")}, 1},
		"no meta, queries": {[]string{"-url", fine, "-heuristic", "h", "-semantic", "s", "-truth", truth, "-meta=false", "-repeat", "0"}, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := cli(c.args, &stdout, &stderr); code != c.code {
				t.Fatalf("exit %d, want %d: %s", code, c.code, stderr.String())
			}
		})
	}
}

func TestLookupFailures(t *testing.T) {
	s := &symbol{Path: "a.go", Name: "Run"}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	if _, _, _, err := lookup(http.DefaultClient, dead.URL, "c", s, 1); err == nil {
		t.Fatal("unreachable server")
	}
	for name, h := range map[string]http.HandlerFunc{
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"cut short": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("{"))
		},
	} {
		srv := httptest.NewServer(h)
		if _, _, _, err := lookup(http.DefaultClient, srv.URL, "c", s, 1); err == nil {
			t.Errorf("%s: no error", name)
		}
		srv.Close()
	}
}

func TestStrataWithoutInterfaces(t *testing.T) {
	cls := func(name string, impls int) *symbol {
		return &symbol{Path: "m.py", Name: name, Kind: "class", impls: impls, refs: map[string]bool{}}
	}
	all := []*symbol{cls("Base", 2), cls("Leaf", 0), {Path: "tests/test_m.py", Name: "f", Kind: "function"},
		{Path: `src/K.php`, Name: `App\Http\Kernel`, Kind: "class"}}
	st := stratify(all)
	if len(st[stratumInterface]) != 1 || st[stratumInterface][0].Name != "Base" || len(st[stratumFunction]) != 0 {
		t.Fatalf("%+v", st)
	}
	if got := all[3].bare(); got != "Kernel" {
		t.Fatal(got)
	}
	for p, want := range map[string]bool{"x_test.go": true, "test_x.py": true, "x_test.py": true, "conftest.py": true,
		"XTest.php": true, "x.go": false, "tests/helpers.py": false} {
		if isTestFile(p) != want {
			t.Errorf("%s", p)
		}
	}
}

func TestEmptyMeta(t *testing.T) {
	st, err := factBytesOf(`{"meta":{"path":["a"]}}` + "\n")
	if err != nil || st.Documents != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	big := `{"meta":{"symIndex":["v1"],"symRefs":["` + strings.Repeat("x", 70<<10) + `"]}}`
	if st, _ := factBytesOf(big); st.Over64KiB != 1 || st.P99Bytes != st.MaxBytes {
		t.Fatalf("%+v", st)
	}
	if ratio(0, 0) != 1 {
		t.Fatal("0/0")
	}
}

func TestMainExitCode(t *testing.T) {
	args, saved := os.Args, exit
	defer func() { os.Args, exit = args, saved }()
	got := -1
	os.Args, exit = []string{"codesymbol", "-nope"}, func(c int) { got = c }
	main()
	if got != 2 {
		t.Fatal(got)
	}
}

// A tree of methods with names of their own (PHP) fills the sample with them.
func TestSampleFillsWithMethods(t *testing.T) {
	var all []*symbol
	for _, n := range []string{"A.run", "B.stop", "C.go", "D.go"} {
		all = append(all, &symbol{Path: "x.php", Name: n, Kind: "method", refs: map[string]bool{}})
	}
	got := sample(all, 3, 1)
	strata := map[string]int{}
	for _, s := range got {
		strata[s.Stratum]++
	}
	if len(got) != 3 || strata[stratumAmbiguous] != 2 || strata[stratumMethod] != 1 {
		t.Fatalf("%v", strata)
	}
}
