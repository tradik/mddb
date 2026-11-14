package lspindex

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// cTree is three files with a static init each and a header with a
// prototype, a struct and an enum.
var cTree = map[string]string{"net.c": "\n\n\n\n\n\n\n\n\n\n\n\n\n\n", "disk.c": "\n\n\n\n\n\n\n\n\n\n\n\n\n\n", "common.h": "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n"}

func cScript() fakeScript {
	static := func(name string) map[string]any {
		return tagged(sym(name, 12, 2, 5, 2, 11), "int (void)", 8, tagDeclaration, tagDefinition)
	}
	return fakeScript{
		Symbols: map[string]any{
			"net.c":  []any{static("init")},
			"disk.c": []any{static("init")},
			"common.h": []any{
				tagged(sym("net_start", 12, 4, 4, 4, 4), "int (void)", tagDeclaration),
				tagged(sym("point", 23, 8, 11, 8, 7, sym("x", 8, 9, 9, 9, 5)), "struct", tagDeclaration),
				tagged(sym("color", 10, 13, 13, 13, 5, sym("red", 22, 13, 13, 13, 13)), "enum", tagDeclaration),
			},
		},
		References: map[string][]loc{
			"net.c:2":  {{Path: "net.c", Line: 2, Char: 11}, {Path: "net.c", Line: 9, Char: 10}},
			"disk.c:2": {{Path: "disk.c", Line: 2, Char: 11}, {Path: "disk.c", Line: 11, Char: 10}},
		},
	}
}

func TestIndexClangdC(t *testing.T) {
	res, f, err := clangdRun(t, "c", cTree, []string{"net.c", "disk.c", "common.h"}, cScript())
	if err != nil {
		t.Fatal(err)
	}
	if got := defNames(res.Files["common.h"]); !reflect.DeepEqual(got, []string{"point struct ", "color enum "}) {
		t.Errorf("common.h defs = %q", got)
	}
	// Each init keeps its own file's references.
	for path, line := range map[string]int{"net.c": 10, "disk.c": 12} {
		want := []Ref{{Target: path + "#init", Lines: []int{3, line}}}
		if got := res.Files[path].Refs; !reflect.DeepEqual(got, want) {
			t.Errorf("%s refs = %+v, want %+v", path, got, want)
		}
	}
	rec := f.record(t)
	for _, k := range rec.Asked {
		if strings.HasPrefix(k, "implementation ") {
			t.Errorf("C asked %s", k)
		}
	}
	if !reflect.DeepEqual(rec.LanguageIDs, []string{"c", "c", "c"}) {
		t.Errorf("languageIds = %q", rec.LanguageIDs)
	}
}

func TestIndexClangdRefusesFallbackFlags(t *testing.T) {
	sc := cScript()
	sc.StderrOnOpen = "I[11:26:19.331] Failed to find compilation database for {file}"
	res, f, err := clangdRun(t, "c", cTree, []string{"net.c", "disk.c"}, sc)
	se := wantServerError(t, res, err)
	if !errors.Is(err, ErrFallbackFlags) || !strings.Contains(se.Error(), "disk.c, net.c") || !strings.Contains(se.Error(), "--compile-commands") {
		t.Errorf("err = %v", err)
	}
	_ = f.record(t)
}

func TestIndexClangdRefusesBeforeReferences(t *testing.T) {
	sc := cScript()
	// Logged at start-up, so read long before the definitions are in.
	sc.Stderr = "I[11:26:19.331] ASTWorker building file {root}/net.c version 1 with command clangd fallback\n"
	res, f, err := clangdRun(t, "c", cTree, []string{"net.c", "disk.c"}, sc)
	if _ = wantServerError(t, res, err); !errors.Is(err, ErrFallbackFlags) {
		t.Errorf("err = %v", err)
	}
	for _, k := range f.record(t).Asked {
		if strings.HasPrefix(k, "references ") {
			t.Errorf("asked %s after a fallback compile", k)
		}
	}
}

func TestIndexClangdIgnoresFallbackOutsideTheRun(t *testing.T) {
	sc := cScript()
	sc.Stderr = "I[11:26:19.331] ASTWorker building file /elsewhere/x.c version 1 with command clangd fallback\n"
	if _, _, err := clangdRun(t, "c", cTree, []string{"net.c"}, sc); err != nil {
		t.Errorf("err = %v", err)
	}
}
