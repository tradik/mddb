package main

import (
	"context"
	"errors"

	json "mddb/internal/jsonx"
)

// Reference coding tools (AGT-003).
//
// The workflow they serve: before implementing something, look at how a
// project that already did it did it. Reference collections hold that code —
// indexed by `mddb-cli reference add`, marked code.role "reference" — and
// these tools ask all of them at once. Every one is read-only and answers in
// passages: a path, lines and the definition holding them, plus the
// collection, whose description carries the repository, commit and licence.

// referenceTargets is the collections a reference tool searches: the ones
// named, or every reference collection.
func (s *MCPToolServer) referenceTargets(ctx context.Context, args map[string]interface{}) ([]string, error) {
	targets := []string{rolePrefix + CodeRoleReference}
	if named, ok := args["collections"].([]interface{}); ok && len(named) > 0 {
		targets = targets[:0]
		for _, v := range named {
			if c, ok := v.(string); ok {
				targets = append(targets, c)
			}
		}
	}
	colls := s.client.ExpandTargets(ctx, targets)
	if len(colls) == 0 {
		return nil, errors.New("no reference collections: add one with `mddb-cli reference add <repository>`")
	}
	return colls, nil
}

// toolReferenceSearch searches reference code, hybrid, as passages.
func (s *MCPToolServer) toolReferenceSearch(ctx context.Context, args map[string]interface{}) (string, error) {
	targets, err := s.referenceTargets(ctx, args)
	if err != nil {
		return "", err
	}
	req := &MCPCrossSearchRequest{Query: mcpGetString(args, "query"), TargetCollections: targets,
		TopK: mcpGetInt(args, "top_k"), SearchType: mcpGetString(args, "search_type"),
		PassageOptions: PassageOptions{Format: FormatPassages, PassageLines: mcpGetInt(args, "passage_lines")}}
	if req.SearchType == "" {
		req.SearchType = CrossSearchHybrid
	}
	if req.Filter, err = mcpGetFilter(args); err != nil {
		return "", err
	}
	resp, err := s.client.CrossSearch(ctx, req)
	if err != nil {
		return "", err
	}
	return mcpPassagesJSON(resp.Passages), nil
}

// referenceSymbolResult is one symbol across reference collections.
type referenceSymbolResult struct {
	Symbol      string    `json:"symbol"`
	Definitions []Passage `json:"definitions"`
	References  []Passage `json:"references,omitempty"`
	Callers     []Passage `json:"callers,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"`
}

// symbolAcross runs a symbol lookup in every target and concatenates the
// relations asked for.
func (s *MCPToolServer) symbolAcross(ctx context.Context, args map[string]interface{}, include []string, content bool) (string, error) {
	targets, err := s.referenceTargets(ctx, args)
	if err != nil {
		return "", err
	}
	out := referenceSymbolResult{Symbol: mcpGetString(args, "name"), Definitions: []Passage{}}
	for _, coll := range targets {
		res, err := s.client.CodeSymbol(ctx, &SymbolRequest{Collection: coll, Name: out.Symbol,
			Include: include, Limit: mcpGetInt(args, "limit"), IncludeContent: content})
		if err != nil {
			return "", err
		}
		out.Definitions = append(out.Definitions, res.Definitions...)
		out.References = append(out.References, res.References...)
		out.Callers = append(out.Callers, res.Callers...)
		out.Truncated = out.Truncated || res.Truncated
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return string(data), nil
}

// toolReferenceSymbol: where reference code defines and uses a symbol.
func (s *MCPToolServer) toolReferenceSymbol(ctx context.Context, args map[string]interface{}) (string, error) {
	return s.symbolAcross(ctx, args, []string{RelDefinitions, RelReferences}, mcpGetBool(args, "include_content"))
}

// toolReferenceExamples: how reference code calls a symbol — the callers,
// with their code.
func (s *MCPToolServer) toolReferenceExamples(ctx context.Context, args map[string]interface{}) (string, error) {
	return s.symbolAcross(ctx, args, []string{RelDefinitions, RelCallers}, true)
}

// toolReferenceDependencies: what a file in reference code imports, and
// what imports it.
func (s *MCPToolServer) toolReferenceDependencies(ctx context.Context, args map[string]interface{}) (string, error) {
	direction := mcpGetString(args, "direction")
	if direction == "" {
		direction = string(GraphOut)
	}
	res, err := s.client.CodeGraph(ctx, &MCPCodeGraphRequest{Collection: mcpGetString(args, "collection"),
		Key: mcpGetString(args, "key"), Direction: direction, Depth: mcpGetInt(args, "depth"), Lines: true})
	if err != nil {
		return "", err
	}
	edges := res.Edges[:0]
	for _, e := range res.Edges {
		if e.Kind == EdgeImports {
			edges = append(edges, e)
		}
	}
	res.Edges = edges
	data, _ := json.MarshalIndent(res, "", "  ")
	return string(data), nil
}
