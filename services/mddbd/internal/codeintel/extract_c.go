package codeintel

import (
	"regexp"
	"strings"
)

// C, C++ and assembly (#319).
//
// The questions a kernel audit starts with — "which file defines do_execve",
// "what calls vm_page_alloc", "who includes sys/mbuf.h" — need defines, uses
// and imports for C. Like the other extractors this is a scanner, not a
// parser: C's surface is regular enough at file scope that brace depth, paren
// depth and the token before a '(' find the declarations people search for,
// and a scanner never fails on code a compiler would reject or on macros it
// cannot expand.
//
// What counts:
//   - defines: function definitions; static prototypes (the definition is in
//     this file); struct/union/enum tags; typedef names; #define macros;
//     file-scope variables. An extern or non-static prototype is a
//     declaration of something defined elsewhere, so it is not a define —
//     otherwise every header would claim every function it declares.
//   - uses: identifiers called inside function bodies — name( — that this
//     file does not define, excluding language keywords and calls through a
//     struct member (a->fn(), s.fn()), which are not global symbols.
//   - imports: #include targets. "<x>" is kept in angle brackets so the
//     caller can resolve it against the collection's include paths; "x" is
//     relative to the including file.

var (
	cIncludeRe = regexp.MustCompile(`^\s*#\s*include\s*([<"])([^>"]+)[>"]`)
	cDefineRe  = regexp.MustCompile(`^\s*#\s*define\s+([A-Za-z_]\w*)`)

	asmEntryRe = regexp.MustCompile(`\b(?:ENTRY|ALTENTRY|NENTRY|ASENTRY|ENTRY_NP|GLOBAL)\(\s*([A-Za-z_]\w*)\s*\)`)
	asmGloblRe = regexp.MustCompile(`(?m)^\s*\.glob(?:a)?l\s+([A-Za-z_]\w*)`)
	asmCallRe  = regexp.MustCompile(`(?m)^\s*(?:call|callq|calll|bl|blx|jal|bsr)\s+([A-Za-z_]\w*)`)
)

// cKeywords are words followed by '(' that are syntax, not calls, and words
// that can never name a symbol.
var cKeywords = toSet(`if else while for do switch case default return goto break continue
	sizeof typeof __typeof __typeof__ _Alignof alignof __alignof__ _Generic _Static_assert
	static_assert defined __builtin_offsetof offsetof __has_include
	int char short long unsigned signed float double void bool _Bool _Complex
	struct union enum typedef const volatile register static extern inline __inline __inline__
	restrict __restrict __restrict__ auto asm __asm __asm__ __volatile__ __volatile
	new delete throw catch try operator template class namespace using public private
	protected virtual explicit noexcept decltype constexpr nullptr this`)

// cAttributes take an argument list but are not function names:
// `__printflike(1, 2)` after a prototype, `__aligned(8)` after a global.
var cAttributes = toSet(`__attribute__ __attribute __declspec __aligned __section __printflike
	__scanflike __format_arg __nonnull __alloc_size __alloc_align __deprecated __used
	__requires_exclusive __requires_shared __locks_exclusive __locks_shared __unlocks
	__result_use_check __warn_unused_result alignas _Alignas`)

func toSet(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// extractC handles C and C++.
func extractC(content string) Symbols {
	var s Symbols
	code, includes, macros := cPreprocess(stripCComments(content))
	s.Imports = includes
	s.Defines = append(s.Defines, macros...)

	toks := cTokens(blankLiterals(code))
	defines, calls := cScan(toks)
	s.Defines = append(s.Defines, defines...)

	own := toSet(strings.Join(s.Defines, " "))
	for _, c := range calls {
		if !own[c] {
			s.Uses = append(s.Uses, c)
		}
	}
	return s
}

// extractAsm handles assembly sources, which in a kernel tree are run through
// the C preprocessor: #include and comments follow C rules.
func extractAsm(content string) Symbols {
	var s Symbols
	code, includes, _ := cPreprocess(stripCComments(content))
	s.Imports = includes
	for _, re := range []*regexp.Regexp{asmEntryRe, asmGloblRe} {
		for _, m := range re.FindAllStringSubmatch(code, -1) {
			s.Defines = append(s.Defines, m[1])
		}
	}
	own := toSet(strings.Join(s.Defines, " "))
	for _, m := range asmCallRe.FindAllStringSubmatch(code, -1) {
		if !own[m[1]] {
			s.Uses = append(s.Uses, m[1])
		}
	}
	return s
}

// stripCComments blanks /* */ and // comments, leaving string and character
// literals intact (an include path or a "//" inside a string is not a
// comment). Newlines are kept so directives stay on their own lines.
func stripCComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			for _, r := range src[i : i+2+end+2] {
				if r == '\n' {
					b.WriteByte('\n')
				}
			}
			b.WriteByte(' ')
			i += 2 + end + 1
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				b.WriteByte('\n')
			}
		case c == '"' || c == '\'':
			j := skipLiteral(src, i)
			b.WriteString(src[i:j])
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// skipLiteral returns the index just past the literal opening at i, or the
// end of its line when it is never closed.
func skipLiteral(src string, i int) int {
	quote := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case quote:
			return j + 1
		case '\n':
			return j
		}
	}
	return len(src)
}

// cPreprocess removes directive lines (with their backslash continuations)
// from the code, returning the include targets and macro names they held.
func cPreprocess(src string) (code string, includes, macros []string) {
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := cIncludeRe.FindStringSubmatch(line); m != nil {
			target := strings.TrimSpace(m[2])
			if m[1] == "<" {
				target = "<" + target + ">"
			}
			includes = append(includes, target)
		} else if m := cDefineRe.FindStringSubmatch(line); m != nil {
			macros = append(macros, m[1])
		}
		for {
			continued := strings.HasSuffix(strings.TrimRight(lines[i], " \t\r"), `\`)
			lines[i] = ""
			if !continued || i+1 >= len(lines) {
				break
			}
			i++
		}
	}
	return strings.Join(lines, "\n"), includes, macros
}

// blankLiterals replaces string and character literals with a placeholder, so
// the braces and parentheses inside them do not count.
func blankLiterals(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); i++ {
		if c := src[i]; c == '"' || c == '\'' {
			i = skipLiteral(src, i) - 1
			b.WriteByte('0')
			continue
		}
		b.WriteByte(src[i])
	}
	return b.String()
}

// cTokens splits code into identifiers and single punctuation characters,
// with "->" and "::" kept whole. Numbers and whitespace are dropped.
func cTokens(src string) []string {
	var toks []string
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, src[i:j])
			i = j
		case c >= '0' && c <= '9':
			for i < len(src) && (isIdentPart(src[i]) || src[i] == '.') {
				i++
			}
		case c == '-' && i+1 < len(src) && src[i+1] == '>', c == ':' && i+1 < len(src) && src[i+1] == ':':
			toks = append(toks, src[i:i+2])
			i += 2
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			toks = append(toks, src[i:i+1])
			i++
		}
	}
	return toks
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

func isIdent(t string) bool { return t != "" && isIdentStart(t[0]) }
