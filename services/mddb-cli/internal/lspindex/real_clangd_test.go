//go:build lspreal

package lspindex

// clangd against small C and C++ trees (#418): local only, like the other
// real-server tests. clangd must be on PATH or in MDDB_LSP_C/MDDB_LSP_CPP.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// realCTree writes three files with a static init each, their callers in
// main.c and a header, plus compile_commands.json when withDB.
func realCTree(t *testing.T, withDB bool) (string, []string) {
	t.Helper()
	root := t.TempDir()
	files := []string{"common.h", "main.c"}
	writeFile(t, filepath.Join(root, "common.h"), "#ifndef COMMON_H\n#define COMMON_H\nint log_line(const char *msg);\n"+
		"int net_start(void);\nint disk_start(void);\nint ui_start(void);\nstruct point { int x; int y; };\n#endif\n")
	for _, m := range []string{"net", "disk", "ui"} {
		writeFile(t, filepath.Join(root, m+".c"), "#include \"common.h\"\n\nstatic int init(void)\n{\n\treturn log_line(\""+m+"\");\n}\n\n"+
			"int "+m+"_start(void)\n{\n\tint rc = init();\n\tif (rc)\n\t\treturn init();\n\treturn rc;\n}\n")
		files = append(files, m+".c")
	}
	writeFile(t, filepath.Join(root, "main.c"), "#include <stdio.h>\n#include \"common.h\"\n\n"+
		"int log_line(const char *msg)\n{\n\treturn printf(\"%s\\n\", msg) < 0;\n}\n\n"+
		"int main(void)\n{\n\treturn net_start() + disk_start() + ui_start();\n}\n")
	if withDB {
		writeCompileCommands(t, root, "cc", files[1:])
	}
	return root, files
}

func writeCompileCommands(t *testing.T, root, compiler string, files []string) {
	t.Helper()
	var db []map[string]any
	for _, f := range files {
		db = append(db, map[string]any{"directory": root, "file": filepath.Join(root, f), "arguments": []string{compiler, "-c", f}})
	}
	b, err := json.Marshal(db)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, compileCommandsFile), string(b))
}

func realClangd(t *testing.T, lang, root string, files []string) (*Result, error) {
	t.Helper()
	spec, err := Spec(lang)
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{Timeout: 5 * time.Minute}
	if dir, err := FindCompileCommands(root, ""); err == nil {
		opt.CompileCommandsDir = dir
	}
	res, err := Index(context.Background(), root, files, spec, opt)
	if res != nil {
		st := res.Stats
		t.Logf("%s: %s; %d files, %d defs, %d ref lines (%d cross-file), %d impls; %d requests, %d skipped, %d unencodable, %d dropped; %s (load %s)",
			lang, res.Server, st.Files, st.Defs, st.Refs, st.CrossFileRefs, st.Impls, st.Requests, st.Skipped, st.Unencodable, st.Dropped,
			st.Elapsed.Round(time.Millisecond), st.LoadWait.Round(time.Millisecond))
		for _, p := range sortedKeys(res.Files) {
			t.Logf("  %s: %+v", p, *res.Files[p])
		}
	}
	return res, err
}

func TestRealClangdC(t *testing.T) {
	root, files := realCTree(t, true)
	res, err := realClangd(t, "c", root, files)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"net.c", "disk.c", "ui.c"} {
		want := []Ref{{Target: m + "#init", Lines: []int{10, 12}}} // the calls; the definition is no reference
		var got []Ref
		for _, r := range res.Files[m].Refs {
			if strings.HasSuffix(r.Target, "#init") {
				got = append(got, r)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: init refs %+v, want %+v", m, got, want)
		}
	}
	for _, d := range res.Files["common.h"].Defs {
		if d.Kind == "function" {
			t.Errorf("a prototype became a definition: %+v", d)
		}
	}
}

func TestRealClangdCRefusedWithoutDatabase(t *testing.T) {
	root, files := realCTree(t, false)
	_, err := realClangd(t, "c", root, files)
	if !errors.Is(err, ErrFallbackFlags) {
		t.Fatalf("err = %v", err)
	}
	t.Logf("refused: %.300s", err)
}

func TestRealClangdCpp(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "shape.hpp"), "#pragma once\nnamespace geo {\nclass Shape {\npublic:\n\tvirtual ~Shape() = default;\n"+
		"\tvirtual double area() const = 0;\n};\nclass Square : public Shape {\npublic:\n\texplicit Square(double s);\n"+
		"\tdouble area() const override;\nprivate:\n\tdouble side;\n};\nclass Circle : public Shape {\npublic:\n"+
		"\texplicit Circle(double r) : radius(r) {}\n\tdouble area() const override { return 3.14 * radius * radius; }\n"+
		"private:\n\tdouble radius;\n};\n}  // namespace geo\n")
	writeFile(t, filepath.Join(root, "square.cpp"), "#include \"shape.hpp\"\nnamespace geo {\nSquare::Square(double s) : side(s) {}\n"+
		"double Square::area() const\n{\n\treturn side * side;\n}\n}  // namespace geo\n")
	writeFile(t, filepath.Join(root, "main.cpp"), "#include \"shape.hpp\"\nint main()\n{\n\tgeo::Square s(2);\n\tgeo::Circle c(1);\n"+
		"\tconst geo::Shape &a = s, &b = c;\n\treturn static_cast<int>(a.area() + b.area());\n}\n")
	files := []string{"shape.hpp", "square.cpp", "main.cpp"}
	writeCompileCommands(t, root, "c++", files[1:])
	res, err := realClangd(t, "cpp", root, files)
	if err != nil {
		t.Fatal(err)
	}
	got := implsOf(res, "shape.hpp#geo.Shape.area")
	t.Logf("shape.hpp#geo.Shape.area: %v", got)
	if len(got) != 2 {
		t.Errorf("geo.Shape.area: %d implementations, want 2", len(got))
	}
}
