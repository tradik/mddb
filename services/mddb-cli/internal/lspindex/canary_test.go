package lspindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// canaryTree is n files with one function each.
func canaryTree(n int) (map[string]string, []string, fakeScript) {
	tree := map[string]string{}
	var files []string
	sc := fakeScript{
		ServerInfo: map[string]string{"name": "gopls", "version": "v0.24.0"},
		Symbols:    map[string]any{}, References: map[string][]loc{},
		Messages: []map[string]any{
			{"type": 1, "message": "go: go.work requires go >= 1.27 (running go 1.26.11)"},
			{"type": 3, "message": "Loading packages..."},
		},
	}
	for i := range n {
		path := fmt.Sprintf("f%02d.go", i)
		tree[path] = fmt.Sprintf("package x\nfunc f%02d() { f%02d() }\n", i, i)
		files = append(files, path)
		sc.Symbols[path] = []any{sym(fmt.Sprintf("f%02d", i), 12, 1, 1, 1, 5)}
		// An unloaded workspace: each file only sees itself.
		sc.References[path+":1"] = []loc{{Path: path, Line: 1, Char: 13}}
	}
	return tree, files, sc
}

func TestIndexCanaryNoCrossFileReference(t *testing.T) {
	spec := goSpec(t)
	tree, files, sc := canaryTree(canaryMinFiles)
	sc.Stderr = "gopls: no views\n"
	f := newFakeRun(t, tree, sc)
	res, err := Index(context.Background(), f.root, files, spec, f.options(spec, Options{}))
	se := wantServerError(t, res, err)
	if !errors.Is(err, ErrWorkspaceNotLoaded) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"no reference crosses a file boundary in 20 files", "GOTOOLCHAIN",
		"go.work requires go >= 1.27", "gopls: no views"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
	if len(se.Messages) != 1 || strings.Contains(msg, "Loading packages") {
		t.Errorf("Messages = %v (only errors belong there)", se.Messages)
	}
	f.record(t)
}

func TestIndexCanaryMostReferencesFail(t *testing.T) {
	spec := goSpec(t)
	tree, files, sc := canaryTree(canaryMinFiles)
	sc.Messages = nil
	for i, path := range files {
		if i < 11 {
			sc.Fail = append(sc.Fail, "references "+path+":1")
		}
		sc.References[path+":1"] = []loc{{Path: files[(i+1)%len(files)], Line: 1}}
	}
	f := newFakeRun(t, tree, sc)
	res, err := Index(context.Background(), f.root, files, spec, f.options(spec, Options{}))
	se := wantServerError(t, res, err)
	if !errors.Is(err, ErrWorkspaceNotLoaded) || !strings.Contains(err.Error(), "11 of 20 references requests failed") {
		t.Fatalf("err = %v", err)
	}
	if len(se.Messages) != 1 || !strings.Contains(se.Messages[0], "scripted failure") {
		t.Errorf("Messages = %v", se.Messages)
	}
}

func TestIndexCanaryPassesLoadedWorkspace(t *testing.T) {
	spec := goSpec(t)
	tree, files, sc := canaryTree(canaryMinFiles)
	// A few failures and one reference crossing files: a loaded workspace.
	sc.Fail = []string{"references f00.go:1", "references f01.go:1"}
	sc.References["f02.go:1"] = append(sc.References["f02.go:1"], loc{Path: "f03.go", Line: 1})
	f := newFakeRun(t, tree, sc)
	res, err := Index(context.Background(), f.root, files, spec, f.options(spec, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.CrossFileRefs != 1 || res.Stats.Skipped != 2 {
		t.Errorf("Stats = %+v", res.Stats)
	}
}

func TestIndexCanaryQuietOnSmallTrees(t *testing.T) {
	spec := goSpec(t)
	tree, files, sc := canaryTree(canaryMinFiles - 1)
	// A file without definitions does not count towards the threshold.
	tree["empty.go"] = "package x\n"
	files = append(files, "empty.go")
	sc.Symbols["empty.go"] = []any{}
	f := newFakeRun(t, tree, sc)
	res, err := Index(context.Background(), f.root, files, spec, f.options(spec, Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.Files != canaryMinFiles || res.Stats.CrossFileRefs != 0 {
		t.Errorf("Stats = %+v", res.Stats)
	}
}
