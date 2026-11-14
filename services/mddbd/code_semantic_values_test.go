package main

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"mddb/internal/storage"
)

func TestDecodeSemanticFacts(t *testing.T) {
	lines := make([]string, 0, 70)
	for n := 1; n <= 70; n++ {
		lines = append(lines, strconv.Itoa(n))
	}
	meta := map[string][]string{
		MetaKeySymIndex: {"v1 7b39baa38a2e 000000000000 gopls/0.24.0 truncated"},
		MetaKeySymDefs: {
			"File.Close method 9-11 File",
			"flush function 13-13",
			"Huge function 1-99999999999999999999", // valid text, past int
			"broken",
		},
		MetaKeySymRefs: {
			// Continuations sort as text: lines 65-70 come before 1-64.
			"util.go#log " + strings.Join(lines[64:], ","),
			"util.go#log " + strings.Join(lines[:64], ","),
			"c#/y.go#Run 1",
			"big.go#X 99999999999999999999",
			"nohash 1",
		},
		MetaKeySymImpls: {"store/file.go#Closer File", "bad"},
	}
	got, ok := decodeSemanticFacts(meta)
	if !ok {
		t.Fatal("decode refused a valid symIndex")
	}
	want := SemanticFacts{
		Source: "gopls/0.24.0", Truncated: true,
		Defs: []SymbolDefFact{
			{Name: "File.Close", Kind: "method", Start: 9, End: 11, Container: "File"},
			{Name: "flush", Kind: "function", Start: 13, End: 13},
		},
		Refs: []SymbolRefFact{
			{Path: "util.go", Name: "log", Lines: seq(1, 70)},
			{Path: "c#/y.go", Name: "Run", Lines: []int{1}},
		},
		Impls: []SymbolImplFact{{Path: "store/file.go", Name: "Closer", Local: "File"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded\n%#v\nwant\n%#v", got, want)
	}

	if _, ok := decodeSemanticFacts(map[string][]string{MetaKeySymDefs: {"A function 1-1"}}); ok {
		t.Error("facts without symIndex decoded")
	}
	if _, ok := decodeSemanticFacts(nil); ok {
		t.Error("nil meta decoded")
	}
}

func TestValidFactValue(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		want       bool
	}{
		{MetaKeySymDefs, "Good function 1-1", true},
		{MetaKeySymDefs, `App\Http\Kernel.handle method 7-7 App\Http\Kernel`, true},
		{MetaKeySymDefs, "Good function 2-10", true}, // compared as numbers, not text
		{MetaKeySymDefs, "Good function 10-2", false},
		{MetaKeySymDefs, "Good function 1", false},
		{MetaKeySymDefs, "Good function 1-x", false},
		{MetaKeySymDefs, "Good function 1-2-3", false},
		{MetaKeySymDefs, "Go#od function 1-1", false},
		{MetaKeySymDefs, "Good function 1-1 Con#tainer", false},
		{MetaKeySymDefs, "Good\u00a0x function 1-1", false}, // no-break space
		{MetaKeySymDefs, "Good\x1cx function 1-1", false},   // separator
		{MetaKeySymDefs, "Good  function 1-1", false},
		{MetaKeySymRefs, "b.go#Run 1,2,10", true},
		{MetaKeySymRefs, "b.go#Run 10,9", false},
		{MetaKeySymRefs, "b.go#Run ", false},
		{MetaKeySymRefs, "b.go#Run 01", false},
		{MetaKeySymRefs, "b go#Run 1", false},
		{MetaKeySymRefs, "#Run 1", false},
		{MetaKeySymRefs, "b.go# 1", false},
		{MetaKeySymRefs, "b.go#Run 1 2", false},
		{MetaKeySymImpls, "i.go#Iface Local", true},
		{MetaKeySymImpls, "i.go#Iface Lo#cal", false},
		{MetaKeySymImpls, "i.go Local", false},
		{MetaKeySymImpls, "i.go#Iface", false},
		{MetaKeySymDefs, strings.Repeat("a", 1020) + " type 1-1", false}, // over 1024 bytes
	} {
		if got := validFactValue(tc.key, tc.value); got != tc.want {
			t.Errorf("validFactValue(%s, %q) = %v, want %v", tc.key, tc.value, got, tc.want)
		}
	}
}

// FuzzDecodeSemanticFacts feeds arbitrary values to the decoder and the
// validator: neither may panic, and whatever decodes must be well-formed.
func FuzzDecodeSemanticFacts(f *testing.F) {
	f.Add("v1 7b39baa38a2e d gopls/1", "A function 1-2 B", "a.go#A 1,2", "i.go#I A")
	f.Add("v1 x y z truncated", "A function 9-1", "a.go#A 2,1", "i.go#I")
	f.Add("", "", "", "")
	f.Add("v1   ", "- - -", "# ,", "# #")
	f.Fuzz(func(t *testing.T, index, def, ref, impl string) {
		meta := map[string][]string{
			MetaKeySymIndex: {index},
			MetaKeySymDefs:  strings.Split(def, "\n"),
			MetaKeySymRefs:  strings.Split(ref, "\n"),
			MetaKeySymImpls: strings.Split(impl, "\n"),
		}
		facts, ok := decodeSemanticFacts(meta)
		if !ok {
			return
		}
		for _, d := range facts.Defs {
			if d.Start < 1 || d.End < d.Start || !semanticFactKinds[d.Kind] {
				t.Fatalf("decoded an invalid definition %+v", d)
			}
		}
		for _, r := range facts.Refs {
			for i := 1; i < len(r.Lines); i++ {
				if r.Lines[i] <= r.Lines[i-1] {
					t.Fatalf("lines not ascending: %v", r.Lines)
				}
			}
		}
		keepSemanticFacts(&storage.Doc{Key: "a.go", Meta: meta}, 3)
	})
}

func seq(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for n := from; n <= to; n++ {
		out = append(out, n)
	}
	return out
}
