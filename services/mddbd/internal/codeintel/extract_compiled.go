package codeintel

import (
	"regexp"
	"strings"
)

// Go, Rust and Java.
//
//   - defines: functions and methods, and the types a file declares.
//   - uses: names called that the file does not define. A call through a
//     value or a package (x.f(), pkg.F()) is left out, as in C: from the
//     text alone a method and a package function look the same.
//   - imports: Go import paths and Rust use paths as written; a Java import
//     as a path (java.util.List → java/util/List), which is the file's key
//     in a tree stored from its source root; a Rust `mod x;` as ./x, the
//     file beside it.

var (
	goFuncRe   = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`)
	goTypeRe   = regexp.MustCompile(`(?m)^\s*type\s+([A-Za-z_]\w*)`)
	goImportRe = regexp.MustCompile(`(?m)^\s*(?:import\s+)?(?:[A-Za-z_.]\w*\s+)?"([^"]+)"\s*$`)
	goImportIn = regexp.MustCompile(`(?s)\bimport\s*(\([^)]*\)|[^\n]*)`)

	rustDefRe   = regexp.MustCompile(`\b(?:fn|struct|enum|trait|type|union)\s+([A-Za-z_]\w*)`)
	rustMacroRe = regexp.MustCompile(`\bmacro_rules!\s*([A-Za-z_]\w*)`)
	rustUseRe   = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?use\s+([^;]+);`)
	rustModRe   = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+([A-Za-z_]\w*)\s*;`)
	rustCrateRe = regexp.MustCompile(`(?m)^\s*extern\s+crate\s+([A-Za-z_]\w*)`)

	javaTypeRe   = regexp.MustCompile(`\b(?:class|interface|enum|record)\s+([A-Za-z_]\w*)`)
	javaMethodRe = regexp.MustCompile(`(?m)^\s*(?:@\w+\s+)*(?:(?:public|protected|private|static|final|abstract|synchronized|native|default|strictfp)\s+)*(?:<[^>]*>\s*)?[\w.<>\[\],?\s]+?\s+([A-Za-z_]\w*)\s*\([^;{)]*\)\s*(?:throws\s+[\w.,\s]+)?\{`)
	javaImportRe = regexp.MustCompile(`(?m)^\s*import\s+(static\s+)?([\w.]+?)(\.\*)?\s*;`)
)

var goKeywords = toSet(`import if for switch select func return go defer range case chan map struct
	interface make len cap append new copy delete panic recover print println close min max clear
	complex real imag int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 uintptr
	float32 float64 complex64 complex128 string byte rune bool error any`)

var rustKeywords = toSet(`pub if while for loop match return fn Some None Ok Err Box Vec String
	println print eprintln eprint format write writeln vec assert assert_eq assert_ne debug_assert
	debug_assert_eq debug_assert_ne panic todo unimplemented unreachable matches dbg include_str
	include_bytes concat stringify env cfg line file column module_path`)

var javaKeywords = toSet(`if while for switch catch return synchronized new super this throw
	assert try do else case`)

func extractGo(content string) Symbols {
	var s Symbols
	for _, block := range allSubmatches(goImportIn, scrub(content, cLike, false), 1) {
		s.Imports = append(s.Imports, allSubmatches(goImportRe, strings.Trim(block, "()"), 1)...)
	}
	code := scrub(content, cLike, true)
	s.Defines = append(allSubmatches(goFuncRe, code, 1), allSubmatches(goTypeRe, code, 1)...)
	s.Uses = usesExcept(calls(code, goKeywords), s.Defines)
	return s
}

func extractRust(content string) Symbols {
	var s Symbols
	code := scrub(content, rustSyntax, true)
	for _, u := range allSubmatches(rustUseRe, code, 1) {
		s.Imports = append(s.Imports, strings.Join(strings.Fields(u), ""))
	}
	for _, m := range allSubmatches(rustModRe, code, 1) {
		s.Imports = append(s.Imports, "./"+m)
	}
	s.Imports = append(s.Imports, allSubmatches(rustCrateRe, code, 1)...)
	s.Defines = append(allSubmatches(rustDefRe, code, 1), allSubmatches(rustMacroRe, code, 1)...)
	s.Uses = usesExcept(calls(code, rustKeywords), s.Defines)
	return s
}

func extractJava(content string) Symbols {
	var s Symbols
	code := scrub(content, cLike, true)
	for _, m := range javaImportRe.FindAllStringSubmatch(code, -1) {
		name := m[2]
		if m[1] != "" && m[3] == "" { // import static a.b.C.member: the class is a.b.C
			if i := strings.LastIndexByte(name, '.'); i > 0 {
				name = name[:i]
			}
		}
		s.Imports = append(s.Imports, strings.ReplaceAll(name, ".", "/"))
	}
	s.Defines = append(allSubmatches(javaTypeRe, code, 1), allSubmatches(javaMethodRe, code, 1)...)
	names := append(calls(code, javaKeywords), allSubmatches(newRe, code, 1)...)
	s.Uses = usesExcept(names, s.Defines)
	return s
}
