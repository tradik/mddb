package lspindex

import (
	"slices"
	"strings"
)

// rust-analyzer, the Rust server (CODE-018). Checked against rust-analyzer
// 1.98.0 (the rustup component) on 2026-11-14, over a probe crate and
// services/mddb-chat:
//
//   - documentSymbol puts methods under impl blocks: kind Object (19), named
//     "impl Square", "impl Shape for Square", "impl fmt::Display for Square",
//     "impl Wrapper<T>" (the impl's own <T: Clone> already gone), selection
//     on the self type. An impl block is no definition; it names its
//     methods' container (Square.area) and is what implementation answers
//     for a trait point at (rustOwner);
//   - inline modules (`mod inner { ... }`) are Module (2) nodes with their
//     items as children, so they prefix them (inner.Hidden.new); a module
//     in its own file has no node, and its items carry no prefix — the
//     file's path says where they are;
//   - a type alias is TypeParameter (26); a trait Interface (11), its
//     methods Method (6); an associated function without self (new) is a
//     Function (12) under its impl, kept as rust-analyzer says;
//   - implementation answers for a trait (its impl blocks) and for a trait
//     method (each impl's method). Asked for an impl's method it lists the
//     method's siblings in the other impls, so only a trait's own methods
//     are asked (ImplParents);
//   - it loads the workspace with `cargo metadata`, builds build scripts and
//     proc-macros, then primes its caches, and says when it is done with
//     experimental/serverStatus (quiescent). A workspace it cannot load is
//     health "error" ("Failed to load workspaces.") while every request
//     still answers, from single files.
//
// checkOnSave is off: the run saves nothing, and the cargo check it starts
// once the workspace has loaded would compile the crate for no fact.

// rustSpec is the table entry for Rust.
func rustSpec() ServerSpec {
	return ServerSpec{
		Language: "rust", LanguageID: "rust", EnvVar: "MDDB_LSP_RUST",
		Candidates:     []Candidate{{Name: "rust-analyzer", Argv: []string{"rust-analyzer"}}},
		Kinds:          map[int]string{symbolTypeParameter: "type"},
		ImplKinds:      []int{symbolInterface, symbolMethod},
		ImplParents:    []int{symbolInterface},
		Owner:          rustOwner,
		NamespaceNodes: true,
		SkipRefs:       rustSkipRefs,
		Settings:       rustSettings,
		ServerStatus:   true,
		StatusHint: "run autoindex on the directory of the workspace's Cargo.toml (rust-analyzer looks for it " +
			"there and one directory below) and check that `cargo metadata` succeeds there, then run again",
		WaitIndexed: true,
	}
}

// rustSettings turns off the cargo check rust-analyzer starts once loaded.
func rustSettings(Options) map[string]any {
	return map[string]any{"checkOnSave": false}
}

// rustOwner is the type an impl block's items belong to, "" for any other
// node.
func rustOwner(n *documentSymbol) string {
	if n.Kind != symbolObject {
		return ""
	}
	return rustImplType(n.Name)
}

// rustImplType reduces an impl block's name to its self type's name:
// "impl Shape for Wrapper<T>" is Wrapper, "impl crate::Square" Square,
// "impl Shape for &'static str" str. A type without a name of its own
// keeps its text without spaces ([T], (A,B)).
func rustImplType(name string) string {
	rest, ok := strings.CutPrefix(name, "impl")
	if !ok {
		return ""
	}
	rest = stripTemplateArgs(rest)
	if _, self, ok := strings.Cut(rest, " for "); ok {
		rest = self
	}
	rest = trimRustPointers(strings.TrimSpace(rest))
	if i := strings.LastIndex(rest, "::"); i >= 0 {
		rest = rest[i+2:]
	}
	return strings.Join(strings.Fields(rest), "")
}

// rustPointerPrefixes are what may stand before a self type's name.
var rustPointerPrefixes = []string{"&", "mut ", "dyn ", "*const ", "*mut "}

// trimRustPointers drops references, pointers, dyn and lifetimes in front
// of a type: &'a mut dyn Shape is Shape.
func trimRustPointers(s string) string {
	for {
		before := s
		for _, p := range rustPointerPrefixes {
			s = strings.TrimPrefix(s, p)
		}
		if strings.HasPrefix(s, "'") {
			if _, after, ok := strings.Cut(s, " "); ok {
				s = after
			}
		}
		s = strings.TrimSpace(s)
		if s == before {
			return s
		}
	}
}

// rustTestModules are the conventional names of a #[cfg(test)] module.
var rustTestModules = []string{"tests", "test"}

// rustSkipRefs skips the functions of a test module: the #[test] functions
// the test harness calls by attribute, nothing in the code, and their
// helpers, referenced only from the module itself. documentSymbol carries
// no attributes, so the module's conventional name is the cheap sign; a
// #[test] function elsewhere is asked like any other.
func rustSkipRefs(_ string, d Def) bool {
	if d.Kind != "function" {
		return false
	}
	segments := strings.Split(d.Name, ".")
	for _, s := range segments[:len(segments)-1] {
		if slices.Contains(rustTestModules, s) {
			return true
		}
	}
	return false
}
