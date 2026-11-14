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
	f := flattener{spec: s, path: path, ns: func(int) string { return "" }, normalize: s.Normalize}
	if s.Namespace != nil {
		f.ns = s.Namespace(src)
	}
	if f.normalize == nil {
		f.normalize = func(n string) string { return n }
	}
	f.walk(nodes, "")
	sort.SliceStable(f.syms, func(i, j int) bool { return lessDef(f.syms[i].def, f.syms[j].def) })
	return f.syms, f.unencodable
}

// flattener carries one file's walk through its symbol tree.
type flattener struct {
	spec        ServerSpec
	path        string
	ns          func(line int) string
	normalize   func(string) string
	syms        []symbol
	unencodable int
}

func (f *flattener) walk(nodes []documentSymbol, parent string) {
	for i := range nodes {
		n := &nodes[i]
		span, sel, ok := n.ranges()
		if !ok {
			continue
		}
		kind := f.spec.kindOf(n.Kind)
		if kind == "" {
			// Not a definition, but it may group some: a namespace or module
			// node, or the comment sections pyrefly turns into String
			// symbols ("# -- calls"). Its children are named as if it were
			// not there (a namespace prefix comes from the source).
			f.walk(n.Children, parent)
			continue
		}
		qname, container := f.qualify(n, span, parent)
		start, end := span.lines()
		d := Def{Name: qname, Kind: kind, Start: start, End: end, Container: container, Detail: n.Detail}
		if defEncodable(d) {
			f.syms = append(f.syms, symbol{path: f.path, def: d, pos: sel.Start, span: span, lspKind: n.Kind,
				skipRefs: f.spec.SkipRefs != nil && f.spec.SkipRefs(f.path, d)})
		} else {
			f.unencodable++
		}
		if !callableKinds[kind] {
			f.walk(n.Children, qname)
		}
	}
}

// qualify names a definition from its parent in the tree, or — for a flat
// SymbolInformation answer, where the hierarchy is only a name — from its
// containerName, with the file's namespace in front of a top-level name.
func (f *flattener) qualify(n *documentSymbol, span lspRange, parent string) (qname, container string) {
	name := f.normalize(n.Name)
	owner := parent
	if owner == "" && n.ContainerName != "" {
		owner = f.ns(span.Start.Line) + f.normalize(n.ContainerName)
	}
	if owner != "" {
		return owner + "." + name, owner
	}
	qname = f.ns(span.Start.Line) + name
	if i := strings.LastIndex(qname, "."); i > 0 {
		container = qname[:i]
	}
	return qname, container
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
