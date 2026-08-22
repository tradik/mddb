package main

import (
	"errors"
	"sort"
)

// Code connection graph (CODE-005).
//
// Answers the relational questions full-text search cannot: what breaks if I
// change `.hero-banner`, which pages load `checkout.js`, what does nothing
// reference any more.
//
// No edges are stored. They are derived at query time from the symbol meta of
// CODE-004 through the metadata index that already exists — `defines` on one
// side, `uses` or `imports` on the other. That is deliberate: an edge is a
// statement about two documents, and storing it means one copy per side, which
// drift apart the moment someone edits only one of them. Deriving from the
// index makes a reindex reproduce the graph exactly.
//
// Resolution stays inside a single collection: a theme is a collection (#192),
// and a selector shared between two unrelated collections is a coincidence, not
// a dependency.

// EdgeKind names why two documents are connected.
type EdgeKind string

const (
	// EdgeUsesSelector connects a document applying a class or id to the
	// stylesheet declaring it.
	EdgeUsesSelector EdgeKind = "uses-selector"
	// EdgeImports connects a document to one it pulls in by path — a
	// `<script src>`, a `<link href>`, an `@import`, an ES import.
	EdgeImports EdgeKind = "imports"
)

// Traversal limits. A popular selector such as `.title` is referenced by nearly
// every template, so an unbounded walk of a real theme returns the whole theme.
// These match the shape of the FTS limits: bounded by default, explicit when
// raised.
const (
	GraphMaxDepth      = 3
	GraphDefaultDepth  = 1
	GraphMaxDegree     = 100
	GraphDefaultDegree = 100
)

// GraphDirection selects which way edges are followed.
type GraphDirection string

const (
	// GraphOut follows what this document depends on.
	GraphOut GraphDirection = "out"
	// GraphIn follows what depends on this document — "what breaks if I
	// change this".
	GraphIn GraphDirection = "in"
	// GraphBoth follows either.
	GraphBoth GraphDirection = "both"
)

// ParseGraphDirection maps a request parameter onto a direction, defaulting to
// "both" for an empty value.
func ParseGraphDirection(s string) (GraphDirection, error) {
	switch GraphDirection(s) {
	case "":
		return GraphBoth, nil
	case GraphIn, GraphOut, GraphBoth:
		return GraphDirection(s), nil
	default:
		return "", errors.New("direction must be one of: in, out, both")
	}
}

// GraphEdge is one connection, carrying the symbol that justifies it. Without
// the symbol the answer is "these two files are related", which is not
// actionable; with it, the caller knows which line to look at.
type GraphEdge struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Kind      EdgeKind `json:"kind"`
	Symbol    string   `json:"symbol"`
	Direction string   `json:"direction"`
	// Lines is filled only when the caller asks for it — see
	// code_graph_lines.go for why it is not the default.
	Lines *EdgeLines `json:"lines,omitempty"`
}

// GraphNode is a document reached by the traversal.
type GraphNode struct {
	Key      string `json:"key"`
	DocID    string `json:"docId"`
	Lang     string `json:"lang,omitempty"`
	Language string `json:"language,omitempty"`
	Depth    int    `json:"depth"`
}

// GraphResult is the answer to one traversal.
type GraphResult struct {
	Collection string      `json:"collection"`
	Root       string      `json:"root"`
	Direction  string      `json:"direction"`
	Depth      int         `json:"depth"`
	Nodes      []GraphNode `json:"nodes"`
	Edges      []GraphEdge `json:"edges"`
	// Truncated reports that a degree or depth limit cut the walk short, so
	// a caller reading "nothing else depends on this" knows whether to
	// believe it.
	Truncated bool `json:"truncated"`
}

// GraphRequest is one traversal, already validated.
type GraphRequest struct {
	Collection string
	Key        string
	Direction  GraphDirection
	Depth      int
	MaxDegree  int
	// IncludeLines asks for the first-occurrence line of each edge's symbol
	// on both sides. It is the only part of a traversal that reads document
	// content.
	IncludeLines bool
}

// Normalise clamps the traversal limits and fills the defaults.
func (r *GraphRequest) Normalise() error {
	if r.Collection == "" {
		return errors.New("collection is required")
	}
	if r.Key == "" {
		return errors.New("key is required")
	}
	if r.Direction == "" {
		r.Direction = GraphBoth
	}
	if r.Depth <= 0 {
		r.Depth = GraphDefaultDepth
	}
	if r.Depth > GraphMaxDepth {
		r.Depth = GraphMaxDepth
	}
	if r.MaxDegree <= 0 {
		r.MaxDegree = GraphDefaultDegree
	}
	if r.MaxDegree > GraphMaxDegree {
		r.MaxDegree = GraphMaxDegree
	}
	return nil
}

func (r GraphRequest) wantsOut() bool {
	return r.Direction == GraphOut || r.Direction == GraphBoth
}

func (r GraphRequest) wantsIn() bool {
	return r.Direction == GraphIn || r.Direction == GraphBoth
}

// CodeGraph walks the connection graph outward from one document.
//
// Breadth-first, so a node is recorded at the shortest distance from the root
// rather than whichever path happened to reach it first — "two hops away" has
// to mean the same thing on every run.
func (s *Server) CodeGraph(req GraphRequest) (*GraphResult, error) {
	if err := req.Normalise(); err != nil {
		return nil, err
	}

	root, err := s.loadCodeDocByKey(req.Collection, req.Key)
	if err != nil {
		return nil, err
	}

	res := &GraphResult{
		Collection: req.Collection,
		Root:       root.Key,
		Direction:  string(req.Direction),
		Depth:      req.Depth,
		Nodes:      []GraphNode{nodeFor(root, 0)},
		Edges:      []GraphEdge{},
	}

	seen := map[string]bool{root.Key: true}
	frontier := []graphVisit{{doc: root, depth: 0}}

	for len(frontier) > 0 {
		var next []graphVisit
		for _, cur := range frontier {
			if cur.depth >= req.Depth {
				continue
			}
			edges, truncated := s.neighbours(req, cur.doc)
			res.Truncated = res.Truncated || truncated

			for _, e := range edges {
				res.Edges = append(res.Edges, e.edge)
				if seen[e.doc.Key] {
					continue
				}
				seen[e.doc.Key] = true
				res.Nodes = append(res.Nodes, nodeFor(e.doc, cur.depth+1))
				next = append(next, graphVisit{doc: e.doc, depth: cur.depth + 1})
			}
		}
		frontier = next
	}

	sortGraph(res)
	if req.IncludeLines {
		s.annotateLines(req.Collection, res)
	}
	return res, nil
}

type graphVisit struct {
	doc   *codeDocRef
	depth int
}

func nodeFor(d *codeDocRef, depth int) GraphNode {
	return GraphNode{Key: d.Key, DocID: d.DocID, Lang: d.Lang, Language: d.Language, Depth: depth}
}

// sortGraph makes the answer byte-identical across runs. Neighbours come from
// map iteration over meta values, and a caller diffing two graph responses to
// see what a change did should see only what changed.
func sortGraph(res *GraphResult) {
	sort.SliceStable(res.Nodes, func(i, j int) bool {
		if res.Nodes[i].Depth != res.Nodes[j].Depth {
			return res.Nodes[i].Depth < res.Nodes[j].Depth
		}
		return res.Nodes[i].Key < res.Nodes[j].Key
	})
	sort.SliceStable(res.Edges, func(i, j int) bool {
		a, b := res.Edges[i], res.Edges[j]
		switch {
		case a.From != b.From:
			return a.From < b.From
		case a.To != b.To:
			return a.To < b.To
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		default:
			return a.Symbol < b.Symbol
		}
	})
}
