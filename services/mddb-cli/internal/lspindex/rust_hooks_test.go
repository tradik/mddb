package lspindex

import "testing"

// The rust-analyzer hooks on their own (servers_rust.go).

func TestRustImplType(t *testing.T) {
	cases := map[string]string{
		"impl Square":                       "Square",
		"impl Shape for Square":             "Square",
		"impl fmt::Display for Square":      "Square",
		"impl Wrapper<T>":                   "Wrapper",
		"impl<T: Clone> Wrapper<T>":         "Wrapper",
		"impl From<Vec<u8>> for Bytes":      "Bytes",
		"impl crate::Square":                "Square",
		"impl Shape for &'static str":       "str",
		"impl<'a> Tr for &'a mut dyn Shape": "Shape",
		"impl Tr for *const T":              "T",
		"impl Shape for [T]":                "[T]",
		"impl Tr for (A, B)":                "(A,B)",
		"impl !Send for Raw":                "Raw",
		"Square":                            "",
		"impl 'a":                           "'a",
	}
	for in, want := range cases {
		if got := rustImplType(in); got != want {
			t.Errorf("rustImplType(%q) = %q, want %q", in, got, want)
		}
	}
	if got := rustOwner(&documentSymbol{Name: "impl Square", Kind: symbolStruct}); got != "" {
		t.Errorf("a struct owns: %q", got)
	}
}

func TestRustSkipRefs(t *testing.T) {
	cases := map[Def]bool{
		{Name: "tests.it_works", Kind: "function"}:   true,
		{Name: "a.test.helper", Kind: "function"}:    true,
		{Name: "tests", Kind: "function"}:            false,
		{Name: "tests.Fixture.new", Kind: "method"}:  false,
		{Name: "latest.run", Kind: "function"}:       false,
		{Name: "inner.Hidden.new", Kind: "function"}: false,
	}
	for d, want := range cases {
		if got := rustSkipRefs("src/lib.rs", d); got != want {
			t.Errorf("rustSkipRefs(%+v) = %v", d, got)
		}
	}
}
