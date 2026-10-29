package codeintel

import (
	"regexp"
	"strings"
)

// Comment and literal handling shared by the Go, Rust, Java, Python, Ruby and
// PHP scanners.
//
// Each scanner reads a file twice: once with comments removed but string
// literals intact, for the import targets that live inside quotes, and once
// with the literals blanked as well, so a "(" or a keyword inside a string is
// not taken for code.

// syntax is what the scrubber needs to know about a language.
type syntax struct {
	lineComments  []string    // "//", "#"
	blockComments [][2]string // {"/*", "*/"}
	quotes        string      // characters that open a string literal
	tripleQuotes  bool        // Python's """ and '''
	rustChars     bool        // ' opens a char literal only as 'x' or '\n'; otherwise a lifetime
}

var (
	cLike      = syntax{lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, quotes: "\"'`"}
	rustSyntax = syntax{lineComments: []string{"//"}, blockComments: [][2]string{{"/*", "*/"}}, quotes: "\"'", rustChars: true}
	pySyntax   = syntax{lineComments: []string{"#"}, quotes: "\"'", tripleQuotes: true}
	rbSyntax   = syntax{lineComments: []string{"#"}, blockComments: [][2]string{{"\n=begin", "\n=end"}}, quotes: "\"'"}
	phpSyntax  = syntax{lineComments: []string{"//", "#"}, blockComments: [][2]string{{"/*", "*/"}}, quotes: "\"'"}
)

// scrub removes comments, keeping newlines, and with blank set replaces each
// string literal by "0" as well.
func scrub(src string, sx syntax, blank bool) string {
	return scrubMode(src, sx, blank, false)
}

// scrubLines is scrub with literals blanked but their newlines kept, so every
// line of the result is the same line of the source (CODE-008 spans). Plain
// scrub folds a multi-line literal into one "0", which the symbol extractors
// do not mind and a line number does.
func scrubLines(src string, sx syntax) string {
	return scrubMode(src, sx, true, true)
}

func scrubMode(src string, sx syntax, blank, keepLines bool) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		if end, ok := sx.comment(src, i); ok {
			b.WriteString(strings.Repeat("\n", strings.Count(src[i:end], "\n")))
			b.WriteByte(' ')
			i = end
			continue
		}
		if end, ok := sx.literal(src, i); ok {
			if blank {
				b.WriteByte('0')
				if keepLines {
					b.WriteString(strings.Repeat("\n", strings.Count(src[i:end], "\n")))
				}
			} else {
				b.WriteString(src[i:end])
			}
			i = end
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

// comment returns the end of a comment starting at i.
func (sx syntax) comment(src string, i int) (int, bool) {
	for _, lc := range sx.lineComments {
		if strings.HasPrefix(src[i:], lc) {
			if nl := strings.IndexByte(src[i:], '\n'); nl >= 0 {
				return i + nl, true
			}
			return len(src), true
		}
	}
	for _, bc := range sx.blockComments {
		if strings.HasPrefix(src[i:], bc[0]) || (i == 0 && bc[0][0] == '\n' && strings.HasPrefix(src, bc[0][1:])) {
			if end := strings.Index(src[i+1:], bc[1]); end >= 0 {
				return i + 1 + end + len(bc[1]), true
			}
			return len(src), true
		}
	}
	return 0, false
}

// literal returns the end of a string literal starting at i.
func (sx syntax) literal(src string, i int) (int, bool) {
	c := src[i]
	if !strings.ContainsRune(sx.quotes, rune(c)) {
		return 0, false
	}
	if sx.tripleQuotes && strings.HasPrefix(src[i:], strings.Repeat(string(c), 3)) {
		if end := strings.Index(src[i+3:], strings.Repeat(string(c), 3)); end >= 0 {
			return i + 3 + end + 3, true
		}
		return len(src), true
	}
	if c == '\'' && sx.rustChars && !rustCharAt(src, i) {
		return 0, false
	}
	if c == '`' { // Go raw string: no escapes, may span lines
		if end := strings.IndexByte(src[i+1:], '`'); end >= 0 {
			return i + 1 + end + 1, true
		}
		return len(src), true
	}
	return skipLiteral(src, i), true
}

// rustCharAt tells a char literal ('x', '\n', '\u{1F600}') from a lifetime ('a).
func rustCharAt(src string, i int) bool {
	rest := src[i+1:]
	if strings.HasPrefix(rest, `\`) {
		return true
	}
	_, size := firstRune(rest)
	return size > 0 && len(rest) > size && rest[size] == '\''
}

func firstRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

// callRe finds a name followed by "(", with what precedes it.
var callRe = regexp.MustCompile(`([.$>:]?)\b([A-Za-z_]\w*)!?\s*\(`)

// calls returns the names called in code: a name followed by "(", not
// reached through a member or a variable ("x.f(", "$x->f(", "$f(", "T::f("),
// and not a keyword. A Rust macro call, "name!(", counts as name.
func calls(code string, keywords map[string]bool) []string {
	var out []string
	for _, m := range callRe.FindAllStringSubmatch(code, -1) {
		if m[1] != "" || keywords[m[2]] {
			continue
		}
		out = append(out, m[2])
	}
	return out
}

// newRe finds a class instantiated with new.
var newRe = regexp.MustCompile(`\bnew\s+\\?([A-Za-z_][\w\\]*)`)

// usesExcept is names minus the ones the document defines itself.
func usesExcept(names, defines []string) []string {
	own := make(map[string]bool, len(defines)) // not toSet: an Ansible handler name has spaces
	for _, d := range defines {
		own[d] = true
	}
	var out []string
	for _, n := range names {
		if !own[n] {
			out = append(out, n)
		}
	}
	return out
}

// allSubmatches is group n of every match of re.
func allSubmatches(re *regexp.Regexp, s string, n int) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[n])
	}
	return out
}
