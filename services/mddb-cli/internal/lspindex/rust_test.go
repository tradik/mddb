package lspindex

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// rust-analyzer's answers over a small crate, shaped as rust-analyzer 1.98.0
// gave them (c018 probe, 2026-11-14): impl blocks are Object (19) nodes
// named "impl Square", "impl Shape for Square", selected on the self type;
// an inline module is a Module (2) node; a type alias a TypeParameter (26).
// Lines are 0-based.
// The crate's two files.
const (
	libRs    = "src/lib.rs"
	shapesRs = "src/shapes.rs"
)

var rustTree = map[string]string{
	libRs:    strings.Repeat("\n", 60),
	shapesRs: strings.Repeat("\n", 20),
}

func rustScript() fakeScript {
	return fakeScript{
		ServerInfo: map[string]string{"name": "rust-analyzer", "version": "1.98.0 (88d9e12 2026-08-18)"},
		Symbols: map[string]any{
			libRs: []any{
				sym("Shape", 11, 4, 9, 4, 10, sym("area", 6, 5, 5, 5, 7), sym("name", 6, 6, 8, 6, 7)),
				sym("Square", 23, 11, 13, 11, 11, sym("side", 8, 12, 12, 12, 4)),
				sym("impl Square", 19, 15, 19, 15, 5, sym("new", 12, 16, 18, 16, 11)),
				sym("impl Shape for Square", 19, 21, 25, 21, 15, sym("area", 6, 22, 24, 22, 7)),
				sym("impl Shape for Wrapper<T>", 19, 27, 31, 27, 18, sym("area", 6, 28, 30, 28, 7)),
				sym("Wrapper", 23, 33, 35, 33, 11),
				sym("Alias", 26, 37, 37, 37, 9),
				sym("MAX", 14, 38, 38, 38, 10),
				sym("inner", 2, 39, 47, 39, 4,
					sym("Hidden", 23, 40, 40, 40, 15),
					sym("impl Hidden", 19, 41, 45, 41, 9, sym("new", 12, 42, 44, 42, 15))),
				sym("tests", 2, 49, 55, 49, 4, sym("it_works", 12, 51, 54, 52, 7)),
				// A self type the format cannot carry: its method is
				// unencodable, implementations pointing at it are dropped.
				sym("impl Tr for A#B", 19, 57, 59, 57, 12, sym("f", 6, 58, 58, 58, 7)),
			},
			shapesRs: []any{
				sym("Circle", 23, 2, 4, 2, 11),
				sym("impl Shape for Circle", 19, 6, 10, 6, 15, sym("area", 6, 7, 9, 7, 7)),
				sym("impl crate::Square", 19, 12, 16, 12, 5, sym("double", 6, 13, 15, 13, 11)),
			},
		},
		References: map[string][]loc{
			libRs + ":11": {{Path: shapesRs, Line: 12, Char: 12}},
		},
		Implementation: map[string][]loc{
			libRs + ":4": {{Path: libRs, Line: 21, Char: 15}, {Path: libRs, Line: 27, Char: 18},
				{Path: shapesRs, Line: 6, Char: 15}, {Path: libRs, Line: 57, Char: 12}},
			libRs + ":5": {{Path: libRs, Line: 22, Char: 7}, {Path: libRs, Line: 28, Char: 7},
				{Path: shapesRs, Line: 7, Char: 7}, {Path: libRs, Line: 5, Char: 7}},
		},
	}
}

var rustFiles = []string{libRs, shapesRs}

// rustStatus is an experimental/serverStatus notification's params.
func rustStatus(health string, quiescent bool, message string) map[string]any {
	return map[string]any{"health": health, "quiescent": quiescent, "message": message}
}

func rustRun(t *testing.T, sc fakeScript, opt Options) (*Result, *fakeRun, error) {
	t.Helper()
	spec, err := Spec("rust")
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeRun(t, rustTree, sc)
	res, err := Index(context.Background(), f.root, rustFiles, spec, f.options(spec, opt))
	return res, f, err
}

// asked returns the requests of one method the fake saw, sorted.
func asked(rec record, method string) []string {
	var out []string
	for _, k := range rec.Asked {
		if rest, ok := strings.CutPrefix(k, method+" "); ok {
			out = append(out, rest)
		}
	}
	slices.Sort(out)
	return out
}

func TestIndexRust(t *testing.T) {
	sc := rustScript()
	// Not quiescent at first, then quiescent with a warning (failed build
	// scripts, say), which still loads the workspace. A progress that never
	// ends (cargo check) does not hold the run once the status says done.
	sc.Statuses = []map[string]any{rustStatus("ok", false, "")}
	sc.StatusLater = rustStatus("warning", true, "build scripts failed")
	sc.StatusLaterMs = 300
	sc.ProgressOpen = true
	res, f, err := rustRun(t, sc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	wantDefs := map[string][]string{
		libRs: {"Shape interface ", "Shape.area method Shape", "Shape.name method Shape", "Square struct ",
			"Square.new function Square", "Square.area method Square", "Wrapper.area method Wrapper", "Wrapper struct ",
			"Alias type ", "inner.Hidden struct ", "inner.Hidden.new function inner.Hidden", "tests.it_works function "},
		shapesRs: {"Circle struct ", "Circle.area method Circle", "Square.double method Square"},
	}
	for path, want := range wantDefs {
		if got := defNames(res.Files[path]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s defs:\n got %q\nwant %q", path, got, want)
		}
	}
	impls := map[string][]Impl{
		libRs: {{Target: libRs + "#Shape", Local: "Square"}, {Target: libRs + "#Shape", Local: "Wrapper"},
			{Target: libRs + "#Shape.area", Local: "Square.area"}, {Target: libRs + "#Shape.area", Local: "Wrapper.area"}},
		shapesRs: {{Target: libRs + "#Shape", Local: "Circle"}, {Target: libRs + "#Shape.area", Local: "Circle.area"}},
	}
	for path, want := range impls {
		if got := res.Files[path].Impls; !reflect.DeepEqual(got, want) {
			t.Errorf("%s impls = %+v, want %+v", path, got, want)
		}
	}
	if st := res.Stats; st.Unencodable != 1 || st.Dropped != 1 || st.CrossFileRefs != 1 {
		t.Errorf("stats = %+v", st)
	}
	if res.Server != "rust-analyzer/1.98.0" {
		t.Errorf("Server = %q", res.Server)
	}
	checkRustRequests(t, f.record(t))
}

// checkRustRequests checks what TestIndexRust asked of the fake, and when.
func checkRustRequests(t *testing.T, rec record) {
	t.Helper()
	// The trait and its methods; not the methods of impl blocks, whose
	// implementations are their siblings.
	if got := asked(rec, "implementation"); !reflect.DeepEqual(got, []string{libRs + ":4", libRs + ":5", libRs + ":6"}) {
		t.Errorf("implementation asked for %q", got)
	}
	refs := asked(rec, "references")
	for _, never := range []string{libRs + ":52", libRs + ":15", libRs + ":21"} { // a test, two impl blocks
		if slices.Contains(refs, never) {
			t.Errorf("references asked for %s: %q", never, refs)
		}
	}
	later := slices.Index(rec.Asked, "status later")
	first := slices.IndexFunc(rec.Asked, func(k string) bool { return strings.HasPrefix(k, "references ") })
	if later < 0 || first < later {
		t.Errorf("references asked before the server was quiescent: %v", rec.Asked)
	}
	caps := mustJSON(t, rec.Initialize["capabilities"])
	if !strings.Contains(caps, `"experimental":{"serverStatusNotification":true}`) {
		t.Errorf("capabilities = %s", caps)
	}
	if got := mustJSON(t, rec.Initialize["initializationOptions"]); got != `{"checkOnSave":false}` {
		t.Errorf("initializationOptions = %s", got)
	}
}

func TestIndexRustNeverQuiescent(t *testing.T) {
	sc := rustScript()
	sc.Statuses = []map[string]any{rustStatus("ok", false, "")}
	res, _, err := rustRun(t, sc, Options{Timeout: time.Second})
	_ = wantServerError(t, res, err)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "indexing rust stopped") {
		t.Fatalf("err = %v", err)
	}
}

func TestIndexRustWorkspaceFailed(t *testing.T) {
	sc := rustScript()
	sc.Statuses = []map[string]any{rustStatus("ok", false, ""),
		rustStatus("error", true, "Failed to load workspaces.")}
	res, f, err := rustRun(t, sc, Options{})
	_ = wantServerError(t, res, err)
	if !errors.Is(err, ErrWorkspaceNotLoaded) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{`"Failed to load workspaces."`, "Cargo.toml", "cargo metadata"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if refs := asked(f.record(t), "references"); len(refs) > 0 {
		t.Errorf("references asked of a server that loaded nothing: %q", refs)
	}
}

func TestIndexRustStatusUnreadable(t *testing.T) {
	sc := rustScript()
	// A status that does not decode is ignored: the progress wait decides.
	sc.StatusLater = map[string]any{"quiescent": "yes"}
	sc.StatusLaterMs = 1
	if _, _, err := rustRun(t, sc, Options{}); err != nil {
		t.Fatal(err)
	}
}
