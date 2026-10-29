package codeintel

import (
	"reflect"
	"strings"
	"testing"
)

func TestCovHFinishSpansOrdersOuterFirstAndDropsDuplicates(t *testing.T) {
	got := finishSpans([]Span{
		{Symbol: "inner", Kind: "function", StartLine: 1, EndLine: 3},
		{Symbol: "outer", Kind: "class", StartLine: 1, EndLine: 9},
		{Symbol: "inner", Kind: "function", StartLine: 1, EndLine: 3},
	})
	want := []Span{
		{Symbol: "outer", Kind: "class", StartLine: 1, EndLine: 9},
		{Symbol: "inner", Kind: "function", StartLine: 1, EndLine: 3, Enclosing: "outer"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("finishSpans:\n got %+v\nwant %+v", got, want)
	}
}

func TestCovHBodyEndContinuationsAndUnclosed(t *testing.T) {
	// A trailing operator carries the declaration onto the next line, where
	// its body opens.
	code := "x = a +\n  b {\n}\n"
	if end := bodyEnd(code, 0); end != strings.LastIndex(code, "}") {
		t.Fatalf("continued declaration: %d", end)
	}
	// A declaration that runs off the end of the file has no body.
	if end := bodyEnd("func f(a int", 0); end != -1 {
		t.Fatalf("unclosed declaration: %d", end)
	}
	if !continues("f(a,\nb)", 0, 4) {
		t.Fatal("a trailing comma continues the declaration")
	}
}
