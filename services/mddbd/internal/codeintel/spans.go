package codeintel

import (
	"sort"
	"strings"
)

// Definition spans (CODE-008).
//
// Symbols says what a file defines; a span says where: the line a definition
// starts on, the line it ends on, and the definition it sits inside. That is
// what turns "this file uses SearchHybrid" into "lines 118-164 of query.go,
// inside the method Index.Search, call SearchHybrid" — a caller found without
// a call parser, as the definition whose span holds the line of the use.
//
// Same rules as the extractors: scanners, not parsers. A definition starts at
// the line its declaration matches on. It ends at the brace that closes its
// body (Go, Rust, Java, C, C++, JavaScript, TypeScript, PHP, CSS,
// Terraform), where the indentation drops back (Python), at the matching
// `end` (Ruby), or before the next label (assembly). A declaration without a
// body — `type ID int`, a prototype, a #define — spans its own line.
// Languages with no definitions to speak of (HTML, YAML) have no spans.

// Span is one definition's place in its source.
type Span struct {
	Symbol    string `json:"symbol"`
	Kind      string `json:"kind"` // function, method, type, class, rule, block, macro, label
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	// Enclosing is the innermost other definition whose span holds this one
	// (a method's class, a nested function's parent); empty at top level.
	Enclosing string `json:"enclosing,omitempty"`
}

// Contains reports whether line lies within the span.
func (s Span) Contains(line int) bool { return line >= s.StartLine && line <= s.EndLine }

// Spans returns the definitions of a source file, ordered by start line.
// Running it twice on the same content gives the same result.
func Spans(language, content string) []Span {
	var spans []Span
	switch strings.ToLower(language) {
	case "go":
		spans = goSpans(content)
	case "rust":
		spans = rustSpans(content)
	case "java":
		spans = braceSpans(scrubLines(content, cLike), javaDefs)
	case "javascript", "typescript":
		spans = jsSpans(content)
	case "php":
		spans = braceSpans(scrubLines(content, phpSyntax), phpDefs)
	case "c", "cpp":
		spans = cSpans(content)
	case "python":
		spans = indentSpans(scrubLines(content, pySyntax), pyDefs, false)
	case "ruby":
		spans = indentSpans(scrubLines(content, rbSyntax), rbDefs, true)
	case "css", "scss", "sass", "less":
		spans = cssSpans(content)
	case "terraform":
		spans = terraformSpans(content)
	case "asm":
		spans = asmSpans(content)
	default:
		return nil
	}
	return finishSpans(spans)
}

// SymbolAt returns the innermost span holding line, or nil.
func SymbolAt(spans []Span, line int) *Span {
	var best *Span
	for i := range spans {
		s := &spans[i]
		if s.Contains(line) && (best == nil || s.EndLine-s.StartLine < best.EndLine-best.StartLine) {
			best = s
		}
	}
	return best
}

// finishSpans sorts, deduplicates and fills Enclosing.
func finishSpans(spans []Span) []Span {
	sort.Slice(spans, func(i, j int) bool {
		a, b := spans[i], spans[j]
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		if a.EndLine != b.EndLine {
			return a.EndLine > b.EndLine // outer first
		}
		return a.Symbol < b.Symbol
	})
	out := spans[:0]
	for i, s := range spans {
		if i > 0 && s == spans[i-1] {
			continue
		}
		out = append(out, s)
	}
	for i := range out {
		var parent *Span
		for j := range out {
			p := &out[j]
			if i == j || p.Symbol == out[i].Symbol && p.StartLine == out[i].StartLine {
				continue
			}
			if p.StartLine <= out[i].StartLine && p.EndLine >= out[i].EndLine &&
				(p.StartLine < out[i].StartLine || p.EndLine > out[i].EndLine) &&
				(parent == nil || p.EndLine-p.StartLine < parent.EndLine-parent.StartLine) {
				parent = p
			}
		}
		if parent != nil {
			out[i].Enclosing = parent.Symbol
		}
	}
	return out
}

// lines maps byte offsets of one text to 1-based line numbers.
type lines []int

func lineStarts(s string) lines {
	starts := lines{0}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func (l lines) at(offset int) int {
	return sort.SearchInts(l, offset+1)
}

// bodyEnd finds the brace that closes the body of a declaration starting at
// from, and returns its offset, or -1 when the declaration has no body. The
// body's "{" must come before the declaration ends: a ";" or a line break
// that is not a continuation (an open paren, a trailing comma or operator, a
// following "{", where or throws) ends it.
func bodyEnd(code string, from int) int {
	depth := 0
	for i := from; i < len(code); i++ {
		switch c := code[i]; c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ';':
			if depth <= 0 {
				return -1
			}
		case '{':
			if depth <= 0 {
				return matchBrace(code, i)
			}
		case '\n':
			if depth <= 0 && !continues(code, from, i) {
				return -1
			}
		}
	}
	return -1
}

// continues reports whether the line break at nl continues the declaration.
func continues(code string, from, nl int) bool {
	prev := strings.TrimRight(code[from:nl], " \t\r")
	if prev != "" && strings.ContainsRune(",(+-*/=&|<>:", rune(prev[len(prev)-1])) {
		return true
	}
	next := strings.TrimLeft(code[nl+1:], " \t\r\n")
	return strings.HasPrefix(next, "{") || strings.HasPrefix(next, "where") ||
		strings.HasPrefix(next, "throws") || strings.HasPrefix(next, "extends") ||
		strings.HasPrefix(next, "implements") || strings.HasPrefix(next, "->")
}

// matchBrace returns the offset of the "}" closing the "{" at open, or the
// last offset when the file ends first.
func matchBrace(code string, open int) int {
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(code) - 1
}

// spanFrom builds a span starting at offset start whose body search begins at
// from, closing at the matching brace or on its own line.
func spanFrom(code string, ls lines, symbol, kind string, start, from int) Span {
	sp := Span{Symbol: symbol, Kind: kind, StartLine: ls.at(start)}
	sp.EndLine = sp.StartLine
	if end := bodyEnd(code, from); end >= 0 {
		sp.EndLine = ls.at(end)
	}
	return sp
}

// cssSyntax has only block comments: "//" in a stylesheet is usually a
// protocol-relative URL, not a comment.
var cssSyntax = syntax{blockComments: [][2]string{{"/*", "*/"}}, quotes: "\"'"}

var commentSyntax = map[string]syntax{
	"go": cLike, "java": cLike, "javascript": cLike, "typescript": cLike, "c": cLike, "cpp": cLike, "asm": cLike,
	"rust": rustSyntax, "php": phpSyntax, "python": pySyntax, "ruby": rbSyntax, "terraform": tfSyntax,
	"css": cssSyntax, "scss": cssSyntax, "sass": cssSyntax, "less": cssSyntax,
}

// WithoutComments returns content with its comments blanked and every line
// where it was, so a line number found in the result is the source's. String
// literals stay: a name in a string is often a real reference (a route, a
// registration, an HTML class). Languages without comment syntax here come
// back unchanged.
func WithoutComments(language, content string) string {
	if sx, ok := commentSyntax[strings.ToLower(language)]; ok {
		return scrub(content, sx, false)
	}
	return content
}
