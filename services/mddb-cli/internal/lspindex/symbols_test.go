package lspindex

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// decodeSymbols turns builder maps into what documentSymbol decodes to.
func decodeSymbols(t *testing.T, nodes ...map[string]any) []documentSymbol {
	t.Helper()
	var out []documentSymbol
	if err := json.Unmarshal([]byte(mustJSON(t, nodes)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func defsOf(syms []symbol) []Def {
	var out []Def
	for _, s := range syms {
		out = append(out, s.def)
	}
	return out
}

func TestFlattenPython(t *testing.T) {
	spec, _ := Spec("python")
	// Outer.Inner.method, a module-level function, a function inside a
	// method (local) and a class attribute (not a definition).
	nodes := decodeSymbols(t,
		sym("Outer", 5, 0, 3, 0, 6,
			sym("Inner", 5, 1, 3, 1, 10,
				sym("method", 6, 2, 3, 2, 12, sym("local", 12, 3, 3, 3, 12))),
			sym("attr", 13, 1, 1, 1, 4)),
		sym("helper", 12, 5, 6, 5, 4),
		sym("__init__", 9, 7, 7, 7, 4),
	)
	syms, bad := spec.flatten("pkg/outer.py", nil, nodes)
	want := []Def{
		{Name: "Outer", Kind: "class", Start: 1, End: 4},
		{Name: "Outer.Inner", Kind: "class", Start: 2, End: 4, Container: "Outer"},
		{Name: "Outer.Inner.method", Kind: "method", Start: 3, End: 4, Container: "Outer.Inner"},
		{Name: "helper", Kind: "function", Start: 6, End: 7},
		{Name: "__init__", Kind: "constructor", Start: 8, End: 8},
	}
	if got := defsOf(syms); !reflect.DeepEqual(got, want) || bad != 0 {
		t.Errorf("defs = %+v (bad %d)", got, bad)
	}
}

func TestFlattenPHPNamespace(t *testing.T) {
	spec, _ := Spec("php")
	src := []byte("<?php\nnamespace App\\Http;\n\ninterface Handler { public function handle(): void; }\n")
	nodes := decodeSymbols(t,
		// A namespace node, as some servers send, only groups.
		sym(`App\Http`, 3, 1, 8, 1, 10,
			sym("Handler", 11, 3, 3, 3, 10),
			sym("Kernel", 5, 5, 8, 5, 6, sym("handle", 6, 6, 6, 6, 20), sym("$x", 7, 7, 7, 7, 1))),
		// A section marker grouping definitions, as pyrefly sends for
		// "# -- calls" comments.
		sym("-- section", 15, 9, 9, 9, 0, sym("Grouped", 5, 9, 9, 9, 0)),
	)
	syms, _ := spec.flatten("src/Http/Kernel.php", src, nodes)
	want := []Def{
		{Name: `App\Http\Handler`, Kind: "interface", Start: 4, End: 4},
		{Name: `App\Http\Kernel`, Kind: "class", Start: 6, End: 9},
		{Name: `App\Http\Kernel.handle`, Kind: "method", Start: 7, End: 7, Container: `App\Http\Kernel`},
		{Name: `App\Http\Grouped`, Kind: "class", Start: 10, End: 10},
	}
	if got := defsOf(syms); !reflect.DeepEqual(got, want) {
		t.Errorf("defs = %+v", got)
	}
}

func TestFlattenFlatSymbolInformation(t *testing.T) {
	spec, _ := Spec("python")
	rng := map[string]any{"start": pos(2, 4), "end": pos(3, 0)}
	nodes := decodeSymbols(t,
		map[string]any{"name": "Outer", "kind": 5, "location": map[string]any{"uri": "file:///x.py", "range": map[string]any{"start": pos(0, 0), "end": pos(3, 0)}}},
		map[string]any{"name": "run", "kind": 6, "containerName": "Outer", "location": map[string]any{"uri": "file:///x.py", "range": rng}},
		map[string]any{"name": "broken", "kind": 12},
	)
	syms, _ := spec.flatten("x.py", nil, nodes)
	want := []Def{
		{Name: "Outer", Kind: "class", Start: 1, End: 3},
		{Name: "Outer.run", Kind: "method", Start: 3, End: 3, Container: "Outer"},
	}
	if got := defsOf(syms); !reflect.DeepEqual(got, want) {
		t.Errorf("defs = %+v", got)
	}
	if syms[1].pos != (position{Line: 2, Character: 4}) {
		t.Errorf("flat symbols are asked at the start of their range: %+v", syms[1].pos)
	}
}

func TestFlattenCountsUnencodable(t *testing.T) {
	spec, _ := Spec("go")
	nodes := decodeSymbols(t, sym("bad name", 12, 0, 0, 0, 0), sym("good", 12, 1, 1, 1, 0))
	syms, bad := spec.flatten("a.go", nil, nodes)
	if bad != 1 || len(syms) != 1 || syms[0].def.Name != "good" {
		t.Errorf("syms %+v, bad %d", defsOf(syms), bad)
	}
}

func TestLessDefTies(t *testing.T) {
	a := Def{Name: "a", Kind: "function", Start: 1, End: 2}
	cases := []struct {
		x, y Def
		want bool
	}{
		{a, Def{Name: "a", Kind: "function", Start: 2, End: 2}, true},
		{a, Def{Name: "b", Kind: "function", Start: 1, End: 2}, true},
		{a, Def{Name: "a", Kind: "method", Start: 1, End: 2}, true},
		{a, Def{Name: "a", Kind: "function", Start: 1, End: 3}, true},
		{a, a, false},
	}
	for _, c := range cases {
		if got := lessDef(c.x, c.y); got != c.want {
			t.Errorf("lessDef(%+v, %+v) = %v", c.x, c.y, got)
		}
	}
}

func TestInnermost(t *testing.T) {
	r := func(l1, c1, l2, c2 int) lspRange { return lspRange{Start: position{l1, c1}, End: position{l2, c2}} }
	syms := []symbol{
		{def: Def{Name: "Outer"}, span: r(0, 0, 10, 0)},
		{def: Def{Name: "Inner"}, span: r(2, 0, 5, 0)},
		{def: Def{Name: "Twin"}, span: r(2, 0, 3, 0)},
	}
	for p, want := range map[position]string{{1, 0}: "Outer", {2, 3}: "Twin", {4, 0}: "Inner", {11, 0}: ""} {
		got := innermost(syms, p)
		if (got == nil && want != "") || (got != nil && got.def.Name != want) {
			t.Errorf("innermost(%v) = %v, want %q", p, got, want)
		}
	}
}

func TestRangeLines(t *testing.T) {
	r := lspRange{Start: position{Line: 2}, End: position{Line: 5}}
	if s, e := r.lines(); s != 3 || e != 5 {
		t.Errorf("end at column 0 of a later line: %d-%d", s, e)
	}
	r.End.Character = 1
	if s, e := r.lines(); s != 3 || e != 6 {
		t.Errorf("%d-%d", s, e)
	}
	r = lspRange{Start: position{Line: 2}, End: position{Line: 2}}
	if s, e := r.lines(); s != 3 || e != 3 {
		t.Errorf("empty range: %d-%d", s, e)
	}
}

func TestURIRoundTrip(t *testing.T) {
	uri := fileURI("/tmp/a b/c#d.go")
	if uri != "file:///tmp/a%20b/c%23d.go" {
		t.Errorf("fileURI = %q", uri)
	}
	if p, ok := uriPath(uri); !ok || p != "/tmp/a b/c#d.go" {
		t.Errorf("uriPath = %q %v", p, ok)
	}
	for _, bad := range []string{"untitled:1", "%zz", "file:"} {
		if _, ok := uriPath(bad); ok {
			t.Errorf("uriPath(%q) accepted", bad)
		}
	}
}

// Windows paths: the drive letter follows a slash in the URI and loses it on
// the way back, whether the server writes the colon or %3A.
func TestURIRoundTripWithADriveLetter(t *testing.T) {
	old := windowsPaths
	windowsPaths = true
	t.Cleanup(func() { windowsPaths = old })
	if uri := fileURI(`C:\src\a b.go`); uri != "file:///C:/src/a%20b.go" {
		t.Errorf("fileURI = %q", uri)
	}
	for _, uri := range []string{"file:///C:/src/a%20b.go", "file:///c%3A/src/a%20b.go"} {
		p, ok := uriPath(uri)
		if !ok || !strings.EqualFold(filepath.ToSlash(p), "C:/src/a b.go") {
			t.Errorf("uriPath(%q) = %q %v", uri, p, ok)
		}
	}
	if p, _ := uriPath("file:///9:/x"); filepath.ToSlash(p) != "/9:/x" {
		t.Errorf("not a drive letter: %q", p)
	}
}
