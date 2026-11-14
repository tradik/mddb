package lspindex

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Per-language hooks used by the server table.

// goNormalize turns gopls method names into Type.Method: gopls names a method
// after its receiver, `(*Server).LookupSymbol`, `(List[T]).Push`.
func goNormalize(name string) string {
	if !strings.HasPrefix(name, "(") {
		return name
	}
	recv, method, ok := strings.Cut(name[1:], ").")
	if !ok {
		return name
	}
	recv = strings.TrimPrefix(recv, "*")
	if i := strings.IndexByte(recv, '['); i >= 0 {
		recv = recv[:i]
	}
	return recv + "." + method
}

// goTestPrefixes are the functions `go test` calls by name. Nothing in the
// code calls them, so asking for their references is a request per test
// function for an empty answer — half the files of a well-tested Go repo.
var goTestPrefixes = []string{"Test", "Benchmark", "Fuzz", "Example"}

// goSkipRefs skips test entry points in _test.go files.
func goSkipRefs(path string, d Def) bool {
	if !strings.HasSuffix(path, "_test.go") || d.Kind != "function" {
		return false
	}
	for _, p := range goTestPrefixes {
		if isTestName(d.Name, p) {
			return true
		}
	}
	return false
}

// isTestName applies the go test rule: the prefix, then nothing or a rune
// that is not a lower-case letter (TestX and Test_x run, Testify does not).
func isTestName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return len(name) == len(prefix) || !unicode.IsLower(r)
}

// goSettings passes the memory limit as gopls' memoryLimit setting (bytes,
// experimental in v0.24.0, applied with debug.SetMemoryLimit).
func goSettings(opt Options) map[string]any {
	if opt.MemoryLimitMb <= 0 {
		return nil
	}
	return map[string]any{"memoryLimit": int64(opt.MemoryLimitMb) << 20}
}

// phpNamespaceRe finds `namespace A\B;` and `namespace A\B {`.
var phpNamespaceRe = regexp.MustCompile(`(?m)^[ \t]*namespace[ \t]+([A-Za-z_\\\x80-\xff][A-Za-z0-9_\\\x80-\xff]*)[ \t]*[;{]`)

// phpNamespace qualifies PHP names with their namespace. Neither phpactor nor
// PHPantom puts the namespace in documentSymbol, and `Kernel` alone would
// merge every class of that name across namespaces. A name takes the last
// namespace declared at or above its line.
func phpNamespace(src []byte) func(line int) string {
	type decl struct {
		line   int
		prefix string
	}
	var decls []decl
	for _, m := range phpNamespaceRe.FindAllSubmatchIndex(src, -1) {
		prefix := ""
		if name := strings.Trim(string(src[m[2]:m[3]]), `\`); name != "" {
			prefix = name + `\`
		}
		decls = append(decls, decl{line: bytes.Count(src[:m[0]], []byte("\n")), prefix: prefix})
	}
	return func(line int) string {
		prefix := ""
		for _, d := range decls {
			if d.line <= line {
				prefix = d.prefix
			}
		}
		return prefix
	}
}
