package codeintel

import (
	"regexp"
	"strings"
)

// Python, Ruby and PHP.
//
//   - defines: functions, methods and classes (Ruby modules, PHP interfaces,
//     traits and enums).
//   - uses: names called that the file does not define, not through a value
//     (obj.f(), $obj->f()); classes instantiated with new, and Ruby's
//     Name.new.
//   - imports: a Python module as a path (a.b → a/b, the file a/b.py in a
//     tree stored from its root), relative imports from the file (from .x →
//     ./x); Ruby require as written and require_relative from the file; PHP
//     include and require from the file, and namespaces from use as written.

var (
	pyDefRe        = regexp.MustCompile(`(?m)^\s*(?:async\s+)?(?:def|class)\s+([A-Za-z_]\w*)`)
	pyImportRe     = regexp.MustCompile(`(?m)^\s*import\s+([^\n]+)`)
	pyFromImportRe = regexp.MustCompile(`(?m)^\s*from\s+(\.*)([\w.]*)\s+import\s+\(?([^\n)]+)`)

	rbDefRe      = regexp.MustCompile(`(?m)^\s*def\s+(?:self\.)?([A-Za-z_]\w*[?!=]?)`)
	rbClassRe    = regexp.MustCompile(`(?m)^\s*(?:class|module)\s+(?:[A-Z]\w*::)*([A-Z]\w*)`)
	rbRequireRe  = regexp.MustCompile(`(?m)^\s*(require|require_relative)\s*\(?\s*["']([^"']+)["']`)
	rbNewRe      = regexp.MustCompile(`\b([A-Z]\w*)\.new\b`)
	phpDefRe     = regexp.MustCompile(`(?i)\bfunction\s+&?\s*([A-Za-z_]\w*)\s*\(`)
	phpClassRe   = regexp.MustCompile(`(?i)\b(?:class|interface|trait|enum)\s+([A-Za-z_]\w*)`)
	phpIncludeRe = regexp.MustCompile(`(?i)\b(?:require|include)(?:_once)?\s*\(?\s*(__DIR__\s*\.\s*)?["']([^"']+)["']`)
	phpUseRe     = regexp.MustCompile(`(?im)^\s*use\s+(?:function\s+|const\s+)?\\?([A-Za-z_][\w\\]*)`)
	phpStaticUse = regexp.MustCompile(`\b([A-Z]\w*)::[A-Za-z_]\w*\s*\(`)
	pyKeywords   = toSet(`if elif while for with return yield assert print del not and or in is lambda await except raise`)
	rubyKeywords = toSet(`if unless while until case when return yield puts print p raise require require_relative defined super lambda proc`)
	phpKeywords  = toSet(`if elseif while for foreach switch match return array list isset empty unset echo print exit die include include_once require require_once function fn catch declare use new clone`)
)

func extractPython(content string) Symbols {
	var s Symbols
	code := scrub(content, pySyntax, true)
	for _, list := range allSubmatches(pyImportRe, code, 1) {
		for _, part := range strings.Split(list, ",") {
			if f := strings.Fields(part); len(f) > 0 { // "a.b as c" → a.b
				s.Imports = append(s.Imports, strings.ReplaceAll(f[0], ".", "/"))
			}
		}
	}
	for _, m := range pyFromImportRe.FindAllStringSubmatch(code, -1) {
		if m[2] != "" {
			s.Imports = append(s.Imports, pyRelative(m[1], m[2]))
			continue
		}
		for _, name := range strings.Split(m[3], ",") { // from . import a, b: modules a and b
			if f := strings.Fields(name); len(f) > 0 {
				s.Imports = append(s.Imports, pyRelative(m[1], f[0]))
			}
		}
	}
	s.Defines = allSubmatches(pyDefRe, code, 1)
	s.Uses = usesExcept(calls(code, pyKeywords), s.Defines)
	return s
}

// pyRelative turns "from ..a.b import" into ../a/b: one dot is the package
// the file is in (./), each further dot one level up.
func pyRelative(dots, module string) string {
	mod := strings.ReplaceAll(module, ".", "/")
	if dots == "" {
		return mod
	}
	if len(dots) == 1 {
		return "./" + mod
	}
	return strings.Repeat("../", len(dots)-1) + mod
}

func extractRuby(content string) Symbols {
	var s Symbols
	for _, m := range rbRequireRe.FindAllStringSubmatch(scrub(content, rbSyntax, false), -1) {
		target := m[2]
		if m[1] == "require_relative" && !strings.HasPrefix(target, ".") {
			target = "./" + target
		}
		s.Imports = append(s.Imports, target)
	}
	code := scrub(content, rbSyntax, true)
	s.Defines = append(allSubmatches(rbDefRe, code, 1), allSubmatches(rbClassRe, code, 1)...)
	names := append(calls(code, rubyKeywords), allSubmatches(rbNewRe, code, 1)...)
	s.Uses = usesExcept(names, s.Defines)
	return s
}

func extractPHP(content string) Symbols {
	var s Symbols
	for _, m := range phpIncludeRe.FindAllStringSubmatch(scrub(content, phpSyntax, false), -1) {
		target := strings.TrimPrefix(m[2], "/")
		if m[1] == "" && strings.HasPrefix(m[2], "/") {
			target = m[2] // an absolute path, not one built from __DIR__
		} else if !strings.HasPrefix(target, ".") {
			target = "./" + target
		}
		s.Imports = append(s.Imports, target)
	}
	code := scrub(content, phpSyntax, true)
	s.Imports = append(s.Imports, allSubmatches(phpUseRe, code, 1)...)
	s.Defines = append(allSubmatches(phpDefRe, code, 1), allSubmatches(phpClassRe, code, 1)...)
	names := calls(code, phpKeywords)
	names = append(names, allSubmatches(phpStaticUse, code, 1)...)
	for _, n := range allSubmatches(newRe, code, 1) {
		if i := strings.LastIndexByte(n, '\\'); i >= 0 {
			n = n[i+1:]
		}
		names = append(names, n)
	}
	s.Uses = usesExcept(names, s.Defines)
	return s
}
