package lspindex

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// clangd's answers over a small C++ tree, as clangd 23.1.0 gave them
// (i418 probe, 2026-11-14): namespace geo, an abstract Shape, Square with
// its members declared in the class and defined in square.cpp, Circle
// defined inline. Lines are 0-based.
var cppTree = map[string]string{
	"shape.hpp":  strings.Repeat("\n", 29),
	"square.cpp": strings.Repeat("\n", 20),
}

// tagged adds clangd's detail and symbol tags to a fake symbol.
func tagged(s map[string]any, detail string, tags ...int) map[string]any {
	s["detail"], s["tags"] = detail, tags
	return s
}

func cppScript() fakeScript {
	def := []int{tagDeclaration, tagDefinition}
	return fakeScript{
		ServerInfo:    map[string]string{"name": "clangd", "version": "clangd version 23.1.0 (https://github.com/llvm/llvm-project ea7d852a) linux+grpc x86_64-unknown-linux-gnu"},
		ProgressToken: "backgroundIndexProgress",
		Symbols: map[string]any{
			"shape.hpp": []any{sym("geo", 3, 2, 28, 2, 10,
				tagged(sym("Shape", 5, 4, 8, 4, 6,
					tagged(sym("~Shape", 9, 6, 6, 6, 9), "", 5, 15, tagDeclaration, tagDefinition),
					tagged(sym("area", 6, 7, 7, 7, 16), "double () const", 5, tagAbstract, 15, tagDeclaration, 20),
				), "class", tagAbstract, tagDeclaration, tagDefinition),
				tagged(sym("Square", 5, 10, 16, 10, 6,
					tagged(sym("Square", 9, 12, 12, 12, 10), "(double)", 5, tagDeclaration),
					tagged(sym("area", 6, 13, 13, 13, 8), "double () const", 5, 15, tagDeclaration, 20),
					tagged(sym("side", 8, 15, 15, 15, 8), "double", 2, tagDeclaration),
				), "class", def...),
				tagged(sym("Circle", 5, 18, 24, 18, 6,
					tagged(sym("Circle", 9, 20, 20, 20, 10), "(double)", 5, tagDeclaration, tagDefinition),
					tagged(sym("area", 6, 21, 21, 21, 8), "double () const", 5, 15, tagDeclaration, tagDefinition, 20),
				), "class", def...),
				tagged(sym("total", 12, 26, 26, 26, 7), "double (const Shape &, const Shape &)", tagDeclaration),
				tagged(sym("point_t", 23, 27, 27, 27, 14), "type alias", tagDeclaration),
			)},
			"square.cpp": []any{
				sym("geo", 3, 2, 11, 2, 10,
					tagged(sym("Square::Square", 9, 4, 4, 4, 8), "(double)", 5, tagDeclaration, tagDefinition),
					tagged(sym("Square::area", 6, 6, 9, 6, 15), "double () const", 5, 15, tagDeclaration, tagDefinition, 20)),
				sym("(anonymous namespace)", 3, 13, 15, 13, 0, tagged(sym("helper", 12, 14, 14, 14, 4), "int ()", def...)),
				tagged(sym("Box<T>::get", 6, 17, 19, 17, 10), "T ()", def...),
			},
		},
		Implementation: map[string][]loc{
			"shape.hpp:7": {{Path: "shape.hpp", Line: 21, Char: 8}, {Path: "square.cpp", Line: 6, Char: 15}},
			"shape.hpp:4": {{Path: "shape.hpp", Line: 10, Char: 6}, {Path: "shape.hpp", Line: 18, Char: 6}},
		},
	}
}

func clangdRun(t *testing.T, lang string, tree map[string]string, files []string, sc fakeScript) (*Result, *fakeRun, error) {
	t.Helper()
	spec, err := Spec(lang)
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeRun(t, tree, sc)
	res, err := Index(context.Background(), f.root, files, spec, f.options(spec, Options{}))
	return res, f, err
}

func defNames(ff *FileFacts) []string {
	var out []string
	for _, d := range ff.Defs {
		out = append(out, d.Name+" "+d.Kind+" "+d.Container)
	}
	return out
}

func TestIndexClangdCpp(t *testing.T) {
	old := maxLoadWait
	maxLoadWait = 50 * time.Millisecond // clangd waits for its index regardless
	defer func() { maxLoadWait = old }()
	sc := cppScript()
	sc.ProgressEndMs = 400
	res, f, err := clangdRun(t, "cpp", cppTree, []string{"shape.hpp", "square.cpp"}, sc)
	if err != nil {
		t.Fatal(err)
	}
	wantDefs := map[string][]string{
		// Declarations without a body are gone, but for the pure virtual
		// area; namespaces prefix, and are no container.
		"shape.hpp": {"geo.Shape class ", "geo.Shape.~Shape constructor geo.Shape", "geo.Shape.area method geo.Shape",
			"geo.Square class ", "geo.Circle class ", "geo.Circle.Circle constructor geo.Circle",
			"geo.Circle.area method geo.Circle", "geo.point_t type "},
		"square.cpp": {"geo.Square.Square constructor geo.Square", "geo.Square.area method geo.Square",
			"helper function ", "Box.get method Box"},
	}
	for path, want := range wantDefs {
		if got := defNames(res.Files[path]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s defs:\n got %q\nwant %q", path, got, want)
		}
	}
	impls := map[string][]Impl{
		"shape.hpp":  {{Target: "shape.hpp#geo.Shape", Local: "geo.Circle"}, {Target: "shape.hpp#geo.Shape", Local: "geo.Square"}, {Target: "shape.hpp#geo.Shape.area", Local: "geo.Circle.area"}},
		"square.cpp": {{Target: "shape.hpp#geo.Shape.area", Local: "geo.Square.area"}},
	}
	for path, want := range impls {
		if got := res.Files[path].Impls; !reflect.DeepEqual(got, want) {
			t.Errorf("%s impls = %+v, want %+v", path, got, want)
		}
	}
	rec := f.record(t)
	var implAsked []string
	for _, k := range rec.Asked {
		if rest, ok := strings.CutPrefix(k, "implementation "); ok {
			implAsked = append(implAsked, rest)
		}
	}
	slices.Sort(implAsked)
	// Classes and methods; not constructors, functions or aliases.
	want := []string{"shape.hpp:10", "shape.hpp:18", "shape.hpp:21", "shape.hpp:4", "shape.hpp:7", "square.cpp:17", "square.cpp:6"}
	if !reflect.DeepEqual(implAsked, want) {
		t.Errorf("implementation asked for %q, want %q", implAsked, want)
	}
	// backgroundIndexProgress is waited for past maxLoadWait.
	end := slices.Index(rec.Asked, "progress end")
	first := slices.IndexFunc(rec.Asked, func(k string) bool { return strings.HasPrefix(k, "references ") })
	if end < 0 || first < end {
		t.Errorf("references asked before the background index ended: %v", rec.Asked)
	}
	if !reflect.DeepEqual(rec.LanguageIDs, []string{"cpp", "cpp"}) {
		t.Errorf("languageIds = %q", rec.LanguageIDs)
	}
	if res.Server != "clangd/23.1.0" {
		t.Errorf("Server = %q", res.Server)
	}
}
