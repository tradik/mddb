package lspindex

import (
	"sort"
	"strings"
)

// symbol is a definition with what the driver needs to ask about it.
type symbol struct {
	path     string   // file, slash-separated, relative to the root
	def      Def      // what is reported
	pos      position // the name, where references and implementation are asked
	span     lspRange // the whole definition, to find what encloses a location
	lspKind  int
	skipRefs bool
}

// target is how references and implementations name this symbol.
func (s *symbol) target() string {
	return s.path + "#" + s.def.Name
}

// callableKinds own their children: what a function declares is local.
var callableKinds = map[string]bool{"function": true, "method": true, "constructor": true}

// flatten turns one file's documentSymbol answer into definitions with
// qualified names. unencodable counts definitions whose name the format
// cannot carry.
func (s ServerSpec) flatten(path string, src []byte, nodes []documentSymbol) (syms []symbol, unencodable int) {
	ns := func(int) string { return "" }
	if s.Namespace != nil {
		ns = s.Namespace(src)
	}
	normalize := s.Normalize
	if normalize == nil {
		normalize = func(n string) string { return n }
	}
	var walk func(nodes []documentSymbol, parent string)
	walk = func(nodes []documentSymbol, parent string) {
		for _, n := range nodes {
			span, sel, ok := n.ranges()
			if !ok {
				continue
			}
			kind := s.kindOf(n.Kind)
			if kind == "" {
				// Not a definition, but it may group some: a namespace or
				// module node, or the comment sections pyrefly turns into
				// String symbols ("# -- calls"). Its children are named as
				// if it were not there (a namespace prefix comes from the
				// source).
				walk(n.Children, parent)
				continue
			}
			name := normalize(n.Name)
			owner := parent
			if owner == "" && n.ContainerName != "" {
				// Flat SymbolInformation: the hierarchy is only a name.
				owner = ns(span.Start.Line) + normalize(n.ContainerName)
			}
			var qname, container string
			if owner != "" {
				qname, container = owner+"."+name, owner
			} else {
				qname = ns(span.Start.Line) + name
				if i := strings.LastIndex(qname, "."); i > 0 {
					container = qname[:i]
				}
			}
			start, end := span.lines()
			d := Def{Name: qname, Kind: kind, Start: start, End: end, Container: container, Detail: n.Detail}
			if defEncodable(d) {
				syms = append(syms, symbol{path: path, def: d, pos: sel.Start, span: span, lspKind: n.Kind,
					skipRefs: s.SkipRefs != nil && s.SkipRefs(path, d)})
			} else {
				unencodable++
			}
			if !callableKinds[kind] {
				walk(n.Children, qname)
			}
		}
	}
	walk(nodes, "")
	sort.SliceStable(syms, func(i, j int) bool { return lessDef(syms[i].def, syms[j].def) })
	return syms, unencodable
}

// ranges returns the definition's span and the range of its name.
func (n *documentSymbol) ranges() (span, sel lspRange, ok bool) {
	switch {
	case n.Range != nil:
		span = *n.Range
		sel = span
		if n.SelectionRange != nil {
			sel = *n.SelectionRange
		}
		return span, sel, true
	case n.Location != nil:
		return n.Location.Range, n.Location.Range, true
	}
	return span, sel, false
}

// lessDef orders definitions by line, then name, kind and end.
func lessDef(a, b Def) bool {
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.End < b.End
}

// innermost returns the innermost definition in syms whose span contains p,
// or nil. Spans nest, so among those containing p the innermost starts last,
// or ends first when two start together.
func innermost(syms []symbol, p position) *symbol {
	var best *symbol
	for i := range syms {
		s := &syms[i]
		if !s.span.contains(p) {
			continue
		}
		if best == nil || best.span.Start.before(s.span.Start) ||
			(best.span.Start == s.span.Start && s.span.End.before(best.span.End)) {
			best = s
		}
	}
	return best
}
