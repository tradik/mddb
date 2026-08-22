package main

import (
	"context"

	gql "mddb/graphql"
)

// GraphQL surface for the code connection graph (CODE-005).
//
// The third transport for one traversal — REST, MCP and GraphQL all call
// Server.CodeGraph, so the three cannot disagree about what the graph contains.

// CodeGraph resolves the connection graph around one code document.
func (a *GraphQLAdapter) CodeGraph(ctx context.Context, input gql.CodeGraphInput) (*gql.CodeGraph, error) {
	if _, err := a.requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if err := a.CheckPermission(ctx, input.Collection, int(PermRead)); err != nil {
		return nil, err
	}

	direction, err := ParseGraphDirection(derefString(input.Direction))
	if err != nil {
		return nil, err
	}

	res, err := a.server.CodeGraph(GraphRequest{
		Collection:   input.Collection,
		Key:          input.Key,
		Direction:    direction,
		Depth:        derefInt(input.Depth, 0),
		MaxDegree:    derefInt(input.MaxDegree, 0),
		IncludeLines: input.Lines != nil && *input.Lines,
	})
	if err != nil {
		return nil, err
	}

	return codeGraphToGQL(res), nil
}

func codeGraphToGQL(res *GraphResult) *gql.CodeGraph {
	nodes := make([]*gql.CodeGraphNode, 0, len(res.Nodes))
	for i := range res.Nodes {
		n := res.Nodes[i]
		nodes = append(nodes, &gql.CodeGraphNode{
			Key: n.Key, DocID: n.DocID,
			Lang: optString(n.Lang), Language: optString(n.Language),
			Depth: n.Depth,
		})
	}
	edges := make([]*gql.CodeGraphEdge, 0, len(res.Edges))
	for i := range res.Edges {
		e := res.Edges[i]
		edge := &gql.CodeGraphEdge{
			From: e.From, To: e.To, Kind: string(e.Kind),
			Symbol: e.Symbol, Direction: e.Direction,
		}
		if e.Lines != nil {
			from, to := e.Lines.FromLine, e.Lines.ToLine
			edge.FromLine, edge.ToLine = &from, &to
		}
		edges = append(edges, edge)
	}
	return &gql.CodeGraph{
		Collection: res.Collection,
		Root:       res.Root,
		Direction:  res.Direction,
		Depth:      res.Depth,
		Nodes:      nodes,
		Edges:      edges,
		Truncated:  res.Truncated,
	}
}

// optString maps an empty value to a null field rather than an empty string —
// "this document has no language" and "its language is the empty string" are
// different answers.
func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
