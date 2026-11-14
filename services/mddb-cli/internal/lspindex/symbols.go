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
	f.walk(nodes, scope{})
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

// scope is where a walk is: the qualified name of the enclosing definition,
// and the namespace prefix a top-level name takes (geo. for C++ namespace geo).
type scope struct{ parent, prefix string }

func (f *flattener) walk(nodes []documentSymbol, sc scope) {
	for i := range nodes {
		n := &nodes[i]
		span, sel, ok := n.ranges()
		if !ok {
			continue
		}
		kind, name := f.kindOf(n), f.normalize(n.Name)
		if kind == "" || name == "" {
			// Not a definition, but it may group some: a namespace or module
			// node, the comment sections pyrefly turns into String symbols
			// ("# -- calls"), an anonymous struct. Its children are named as
			// if it were not there, but for a namespace's prefix.
			f.walk(n.Children, f.inside(n, name, sc))
			continue
		}
		qname, container := f.qualify(n, name, span, sc)
		f.add(n, kind, Def{Name: qname, Container: container}, span, sel)
		if !callableKinds[kind] {
			f.walk(n.Children, scope{parent: qname})
		}
	}
}

// kindOf is the fact kind of a node, "" for no definition.
func (f *flattener) kindOf(n *documentSymbol) string {
	kind := f.spec.kindOf(n.Kind)
	if kind != "" && f.spec.Classify != nil {
		kind = f.spec.Classify(n, kind)
	}
	return kind
}

// inside is the scope of the children of a node that is no definition: a
// namespace node adds its name to the prefix where the spec asks for that
// (a PHP namespace prefix comes from the source instead).
func (f *flattener) inside(n *documentSymbol, name string, sc scope) scope {
	if f.spec.NamespaceNodes && n.Kind == symbolNamespace && name != "" {
		sc.prefix += name + "."
	}
	return sc
}

// add records a definition named by d, or counts it when the format cannot
// carry it.
func (f *flattener) add(n *documentSymbol, kind string, d Def, span, sel lspRange) {
	d.Kind, d.Detail = kind, n.Detail
	d.Start, d.End = span.lines()
	if !defEncodable(d) {
		f.unencodable++
		return
	}
	f.syms = append(f.syms, symbol{path: f.path, def: d, pos: sel.Start, span: span, lspKind: n.Kind,
		skipRefs: f.spec.SkipRefs != nil && f.spec.SkipRefs(f.path, d)})
}

// qualify names a definition from its parent in the tree, or — for a flat
// SymbolInformation answer, where the hierarchy is only a name — from its
// containerName, with the file's namespace in front of a top-level name.
// The container is the enclosing definition, or the qualifier a normalized
// name carries (Square.area defined out of class is in Square), never a
// namespace.
func (f *flattener) qualify(n *documentSymbol, name string, span lspRange, sc scope) (qname, container string) {
	top := f.ns(span.Start.Line) + sc.prefix
	owner := sc.parent
	if owner == "" && n.ContainerName != "" {
		owner = top + f.normalize(n.ContainerName)
	}
	if owner != "" {
		qname, container = owner+"."+name, owner
	} else {
		qname = top + name
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		container = qname[:len(qname)-len(name)+i]
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
