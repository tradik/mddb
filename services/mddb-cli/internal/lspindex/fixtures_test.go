package lspindex

import "testing"

// goTree is a small Go package: an interface, a struct implementing it, a
// caller in another file and a test file. Line numbers in the script are
// 0-based, as LSP counts them.
var goTree = map[string]string{
	"a.go": "package store\n\ntype Closer interface {\n\tClose() error\n}\n\ntype File struct{ name string }\n\n" +
		"func (f *File) Close() error {\n\treturn flush(f.name)\n}\n\nfunc flush(name string) error { return nil }\n",
	"b.go":      "package store\n\nfunc use() {\n\t_ = flush(\"x\")\n\tvar f File\n\t_ = f.Close()\n}\n",
	"a_test.go": "package store\n\nimport \"testing\"\nfunc TestFlush(t *testing.T) { _ = flush(\"\") }\nfunc Testify() {}\n",
	"c.go":      "package store\n\nvar _ = flush\n", // in the tree, not indexed
}

var goFiles = []string{"a.go", "b.go", "a_test.go"}

// goScript answers like gopls does for goTree.
func goScript() fakeScript {
	flush := sym("flush", 12, 12, 12, 12, 5)
	flush["detail"] = "func(name string) error"
	return fakeScript{
		ServerInfo: map[string]string{"name": "gopls", "version": `{"GoVersion":"go1.27.3","Version":"v0.24.0"}`},
		Symbols: map[string]any{
			"a.go": []any{
				sym("Closer", 11, 2, 4, 2, 5, sym("Close", 6, 3, 3, 3, 1)),
				sym("File", 23, 6, 6, 6, 5, sym("name", 8, 6, 6, 6, 18)),
				sym("(*File).Close", 6, 8, 10, 8, 15),
				flush,
				sym("K", 14, 1, 1, 1, 6),
			},
			"b.go": []any{sym("use", 12, 2, 6, 2, 5, sym("f", 13, 4, 4, 4, 5), sym("inner", 12, 5, 5, 5, 1))},
			"a_test.go": []any{
				sym("TestFlush", 12, 3, 3, 3, 5),
				sym("Testify", 12, 4, 4, 4, 5),
			},
		},
		References: map[string][]loc{
			"a.go:12": {
				{Path: "a.go", Line: 9, Char: 8}, {Path: "b.go", Line: 3, Char: 5}, {Path: "a_test.go", Line: 3, Char: 35},
				{Path: "c.go", Line: 2, Char: 8}, {Path: "/usr/lib/go/src/x.go", Line: 4},
			},
			"a.go:6": {{Path: "a.go", Line: 8, Char: 9}, {Path: "b.go", Line: 4, Char: 7}},
			"a.go:8": {{Path: "b.go", Line: 5, Char: 7}},
			"a.go:3": {{Path: "b.go", Line: 5, Char: 7}},
		},
		Implementation: map[string][]loc{
			"a.go:2": {{Path: "a.go", Line: 6, Char: 5}, {Path: "a.go", Line: 2, Char: 5}, {URI: "untitled:Untitled-1"}, {Path: "b.go", Line: 0}},
		},
	}
}

var goWant = map[string]*FileFacts{
	"a.go": {
		Defs: []Def{
			{Name: "Closer", Kind: "interface", Start: 3, End: 5},
			{Name: "Closer.Close", Kind: "method", Start: 4, End: 4, Container: "Closer"},
			{Name: "File", Kind: "struct", Start: 7, End: 7},
			{Name: "File.Close", Kind: "method", Start: 9, End: 11, Container: "File"},
			{Name: "flush", Kind: "function", Start: 13, End: 13, Detail: "func(name string) error"},
		},
		Refs:  []Ref{{Target: "a.go#File", Lines: []int{9}}, {Target: "a.go#flush", Lines: []int{10}}},
		Impls: []Impl{{Target: "a.go#Closer", Local: "File"}},
	},
	"b.go": {
		Defs: []Def{{Name: "use", Kind: "function", Start: 3, End: 7}},
		Refs: []Ref{
			{Target: "a.go#Closer.Close", Lines: []int{6}}, {Target: "a.go#File", Lines: []int{5}},
			{Target: "a.go#File.Close", Lines: []int{6}}, {Target: "a.go#flush", Lines: []int{4}},
		},
	},
	"a_test.go": {
		Defs: []Def{{Name: "TestFlush", Kind: "function", Start: 4, End: 4}, {Name: "Testify", Kind: "function", Start: 5, End: 5}},
		Refs: []Ref{{Target: "a.go#flush", Lines: []int{4}}},
	},
}

func goSpec(t *testing.T) ServerSpec {
	t.Helper()
	spec, err := Spec("go")
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
