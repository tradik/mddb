package codeintel

import (
	"regexp"
	"strings"
)

// Per-language definition finders for Spans (CODE-008). Each one locates
// declarations in a scrubbed copy of the source whose lines match the
// original's, so a match's offset gives the definition's line.

// spanDef is one declaration pattern: group 1 is the name.
type spanDef struct {
	re   *regexp.Regexp
	kind string
}

var (
	goMethodRe = regexp.MustCompile(`(?m)^func\s+\(`)
	rustItemRe = regexp.MustCompile(`\b(fn|struct|enum|trait|type|union)\s+([A-Za-z_]\w*)`)
	rustImplRe = regexp.MustCompile(`(?m)^\s*impl\b(?:<[^>{]*>)?\s+(?:[\w:<>, ]+\s+for\s+)?([A-Za-z_]\w*)`)

	javaDefs = []spanDef{{javaTypeRe, "class"}, {javaMethodRe, "method"}}
	phpDefs  = []spanDef{{phpClassRe, "class"}, {phpDefRe, "function"}}

	jsMethodRe = regexp.MustCompile(`(?m)^[ \t]+(?:static\s+|async\s+|get\s+|set\s+|\*\s*)*([A-Za-z_$][\w$]*)\s*\([^()]*\)\s*\{`)
	jsDefs     = []spanDef{{jsClassRe, "class"}, {jsFuncRe, "function"}, {jsConstFn, "function"}, {jsMethodRe, "method"}}
	jsNotNames = toSet(`if for while switch catch function return with`)

	pyClassRe = regexp.MustCompile(`(?m)^[ \t]*class\s+([A-Za-z_]\w*)`)
	pyFuncRe  = regexp.MustCompile(`(?m)^[ \t]*(?:async\s+)?def\s+([A-Za-z_]\w*)`)
	pyDefs    = []spanDef{{pyClassRe, "class"}, {pyFuncRe, "function"}}
	rbDefs    = []spanDef{{rbClassRe, "class"}, {rbDefRe, "method"}}

	cTypeDefRe = `(?:struct|union|enum|class)\s+%s\s*(?::[^{;]*)?\{`
	asmLabelRe = regexp.MustCompile(`(?m)^([A-Za-z_.$][\w.$]*):`)
)

// braceSpans finds every match of defs and closes it at its body's brace.
func braceSpans(code string, defs []spanDef) []Span {
	ls := lineStarts(code)
	var out []Span
	for _, d := range defs {
		for _, m := range d.re.FindAllStringSubmatchIndex(code, -1) {
			out = append(out, spanFrom(code, ls, code[m[2]:m[3]], d.kind, m[2], m[3]))
		}
	}
	return out
}

func goSpans(content string) []Span {
	code := scrubLines(content, cLike)
	ls := lineStarts(code)
	var out []Span
	for _, m := range goFuncRe.FindAllStringSubmatchIndex(code, -1) {
		kind := "function"
		if goMethodRe.MatchString(code[m[0]:m[3]]) {
			kind = "method"
		}
		out = append(out, spanFrom(code, ls, code[m[2]:m[3]], kind, m[2], m[3]))
	}
	for _, m := range goTypeRe.FindAllStringSubmatchIndex(code, -1) {
		out = append(out, spanFrom(code, ls, code[m[2]:m[3]], "type", m[2], m[3]))
	}
	return out
}

func rustSpans(content string) []Span {
	code := scrubLines(content, rustSyntax)
	ls := lineStarts(code)
	var out []Span
	for _, m := range rustItemRe.FindAllStringSubmatchIndex(code, -1) {
		kind := "type"
		if code[m[2]:m[3]] == "fn" {
			kind = "function"
		}
		out = append(out, spanFrom(code, ls, code[m[4]:m[5]], kind, m[4], m[5]))
	}
	// impl blocks are not definitions of their own, but they are what a
	// method sits inside: "Index::search" reads better than a bare "search".
	for _, m := range rustImplRe.FindAllStringSubmatchIndex(code, -1) {
		out = append(out, spanFrom(code, ls, code[m[2]:m[3]], "impl", m[2], m[3]))
	}
	return out
}

func jsSpans(content string) []Span {
	var out []Span
	for _, sp := range braceSpans(scrubLines(content, cLike), jsDefs) {
		if !jsNotNames[sp.Symbol] {
			out = append(out, sp)
		}
	}
	return out
}

// cSpans places each name the C scanner defines: a function at its body, a
// struct/union/enum/class at its braces, anything else (a prototype-only
// name, a #define) on the line it first appears.
func cSpans(content string) []Span {
	code := scrubLines(content, cLike)
	ls := lineStarts(code)
	var out []Span
	for _, name := range ExtractWithLimit("c", content, DefaultMaxSymbols).Defines {
		q := regexp.QuoteMeta(name)
		if loc := regexp.MustCompile(strings.Replace(cTypeDefRe, "%s", q, 1)).FindStringIndex(code); loc != nil {
			out = append(out, spanFrom(code, ls, name, "type", loc[0], loc[0]))
			continue
		}
		placed := false
		for _, m := range regexp.MustCompile(`\b`+q+`\s*\(`).FindAllStringIndex(code, -1) {
			if end := bodyEnd(code, m[0]+len(name)); end >= 0 {
				out = append(out, Span{Symbol: name, Kind: "function", StartLine: ls.at(m[0]), EndLine: ls.at(end)})
				placed = true
				break
			}
		}
		if !placed {
			if loc := regexp.MustCompile(`\b` + q + `\b`).FindStringIndex(code); loc != nil {
				line := ls.at(loc[0])
				out = append(out, Span{Symbol: name, Kind: "macro", StartLine: line, EndLine: line})
			}
		}
	}
	return out
}

// indentSpans ends a definition where the indentation returns to its own
// level (Python); with endKeyword, on that line when it is Ruby's `end`.
func indentSpans(code string, defs []spanDef, endKeyword bool) []Span {
	ls := lineStarts(code)
	src := strings.Split(code, "\n")
	var out []Span
	for _, d := range defs {
		for _, m := range d.re.FindAllStringSubmatchIndex(code, -1) {
			start := ls.at(m[2])
			indent := indentOf(src[start-1])
			end := start
			for i := start; i < len(src); i++ {
				line := src[i]
				if strings.TrimSpace(line) == "" {
					continue
				}
				if indentOf(line) <= indent {
					if endKeyword && strings.HasPrefix(strings.TrimSpace(line), "end") {
						end = i + 1
					}
					break
				}
				end = i + 1
			}
			out = append(out, Span{Symbol: code[m[2]:m[3]], Kind: d.kind, StartLine: start, EndLine: end})
		}
	}
	return out
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 8
		default:
			return n
		}
	}
	return n
}

// cssSpans gives every selector of a rule the rule's span.
func cssSpans(content string) []Span {
	code := scrubLines(content, cLike)
	ls := lineStarts(code)
	var out []Span
	start := 0
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '}', ';':
			start = i + 1
		case '{':
			sel := strings.TrimSpace(code[start:i])
			if sel != "" && !strings.HasPrefix(sel, "@") {
				first := start + strings.Index(code[start:i], sel)
				end := matchBrace(code, i)
				for _, one := range strings.Split(sel, ",") {
					if one = strings.TrimSpace(one); one != "" {
						out = append(out, Span{Symbol: one, Kind: "rule", StartLine: ls.at(first), EndLine: ls.at(end)})
					}
				}
			}
			start = i + 1
		}
	}
	return out
}

func terraformSpans(content string) []Span {
	// Block labels are quoted, so literals stay: a scrub without blanking
	// still keeps every line where it was.
	code := scrub(content, tfSyntax, false)
	ls := lineStarts(code)
	var out []Span
	for _, m := range tfTwoLabelRe.FindAllStringSubmatchIndex(code, -1) {
		name := code[m[4]:m[5]] + "." + code[m[6]:m[7]]
		if code[m[2]:m[3]] == "data" {
			name = "data." + name
		}
		out = append(out, spanFrom(code, ls, name, "block", m[2], m[1]))
	}
	for _, m := range tfOneLabelRe.FindAllStringSubmatchIndex(code, -1) {
		out = append(out, spanFrom(code, ls, tfPrefix[code[m[2]:m[3]]]+code[m[4]:m[5]], "block", m[2], m[1]))
	}
	return out
}

// asmSpans runs each label to the line before the next one.
func asmSpans(content string) []Span {
	code := scrubLines(content, cLike)
	ls := lineStarts(code)
	matches := asmLabelRe.FindAllStringSubmatchIndex(code, -1)
	total := ls.at(max(len(strings.TrimRight(code, "\n"))-1, 0))
	var out []Span
	for i, m := range matches {
		end := total
		if i+1 < len(matches) {
			end = ls.at(matches[i+1][0]) - 1
		}
		out = append(out, Span{Symbol: code[m[2]:m[3]], Kind: "label", StartLine: ls.at(m[0]), EndLine: end})
	}
	return out
}
