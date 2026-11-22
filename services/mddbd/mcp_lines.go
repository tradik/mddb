package main

import (
	"fmt"
	"strings"
)

// Line answers for agents.
//
// An agent that searches code pays for every byte of an answer. JSON spends
// most of them on structure: an envelope, field names on every hit, the path
// twice, white space. grep's answer has none of that — a path, a line number
// and the text — and an agent reads it as well. The renderers here write that
// shape from the results the JSON answers are built from: a path once for
// consecutive hits in one file, one hit per line, and a count that says
// whether the list is complete, so nobody searches again to be sure.

// FormatLines selects the line answer on code_symbol.
const FormatLines = "lines"

// lineTextMax bounds the source text shown for one line.
const lineTextMax = 160

// lineText is one source line as shown in a line answer: without its
// indentation, cut to lineTextMax bytes around the first of terms it holds
// (see cutAround), or from its start without any.
func lineText(s string, terms ...string) string {
	return cutAround(strings.TrimSpace(s), terms, lineTextMax)
}

// lineWriter writes hits, naming a path once for consecutive hits in it.
type lineWriter struct {
	b        strings.Builder
	lastPath string
}

// hit writes "path:loc rest", or "  :loc rest" under the path just written.
func (w *lineWriter) hit(path, loc, rest string) {
	if path == w.lastPath {
		w.b.WriteString("  ")
	} else {
		w.b.WriteString(path)
		w.lastPath = path
	}
	w.b.WriteString(":" + loc + rest + "\n")
}

// line writes a line that is not a hit; the next hit names its path again.
func (w *lineWriter) line(format string, args ...any) {
	fmt.Fprintf(&w.b, format+"\n", args...)
	w.lastPath = ""
}

func (w *lineWriter) String() string { return strings.TrimRight(w.b.String(), "\n") }

// countLine says how much of a list is shown: "4 of 4", or "20 of 32, more
// with limit" when the limit cut it.
func countLine(shown, total int) string {
	if total > shown {
		return fmt.Sprintf("%d of %d, more with limit", shown, total)
	}
	return fmt.Sprintf("%d of %d", shown, shown)
}

// symbolLines renders a symbol lookup as lines:
//
//	path:start-end  kind  Qualified.Name      definitions, callers, callees, implementations, siblings
//	path:line  in Enclosing.Func: text        references, tests
//
// Each relation asked for has a count line, also when it is empty, and a
// passage names its target definition when the lookup found several.
func symbolLines(req *SymbolRequest, res *SymbolResult) string {
	w := &lineWriter{}
	head := res.Symbol
	if res.Precision != "" {
		head += ": " + res.Precision
	}
	if res.SemanticSource != "" {
		head += " (" + res.SemanticSource + ")"
	}
	w.line("%s", head)
	if res.Ambiguous {
		w.line("ambiguous, %d candidates; pass path to choose one", len(res.Candidates))
		for _, c := range res.Candidates {
			w.line("  %s  %s  %s", c.Path, c.Kind, c.Name)
		}
	}
	several := res.Ambiguous || len(res.Definitions) > 1
	for _, rel := range res.relations() {
		if !req.wants(rel.name) {
			continue
		}
		list := *rel.list
		total := len(list)
		if n, cut := res.Totals[rel.name]; cut {
			total = n
		}
		w.line("%s: %s", rel.name, countLine(len(list), total))
		for i := range list {
			writeSymbolPassage(w, &list[i], several && rel.name != RelDefinitions)
		}
	}
	if res.Truncated && len(res.Totals) == 0 {
		w.line("scan limit reached: lists may be incomplete; narrow with path")
	}
	return w.String()
}

// writeSymbolPassage writes one passage of a symbol lookup: a single line for
// a use, a span for a definition, and the text under it when it was asked for.
func writeSymbolPassage(w *lineWriter, p *Passage, withTarget bool) {
	target := ""
	if withTarget && p.Target != "" {
		target = "  -> " + p.Target
	}
	if p.Source == RelReferences || p.Source == RelTests {
		rest := ": " + lineText(p.Content)
		if p.Symbol != "" {
			rest = "  in " + p.Symbol + rest
		}
		w.hit(p.Path, fmt.Sprint(p.Lines.Start), rest+target)
		return
	}
	rest := "  " + p.Symbol
	if p.Kind != "" {
		rest = "  " + p.Kind + rest
	}
	w.hit(p.Path, fmt.Sprintf("%d-%d", p.Lines.Start, p.Lines.End), rest+target)
	if p.Content != "" {
		for _, l := range strings.Split(p.Content, "\n") {
			w.b.WriteString("    " + l + "\n")
		}
		w.lastPath = ""
	}
}

// passageLines renders ranked passages as lines: a header "path:start-end
// Symbol" per hit and its non-empty lines numbered under it. more says the
// search held further hits past the ones shown; a line too long to show whole
// is cut around the first of terms it holds.
func passageLines(passages []Passage, more bool, terms []string) string {
	w := &lineWriter{}
	for i := range passages {
		p := &passages[i]
		head := fmt.Sprintf("%s:%d-%d", p.Path, p.Lines.Start, p.Lines.End)
		if p.Symbol != "" {
			head += "  " + p.Symbol
		}
		w.line("%s", head)
		for n, l := range strings.Split(p.Content, "\n") {
			if strings.TrimSpace(l) != "" {
				w.line("  %d: %s", p.Lines.Start+n, lineText(l, terms...))
			}
		}
	}
	if more {
		w.line("%d hits, more with limit", len(passages))
	} else {
		w.line("%d of %d hits", len(passages), len(passages))
	}
	return w.String()
}
