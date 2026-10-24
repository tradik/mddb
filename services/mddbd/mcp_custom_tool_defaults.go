package main

import (
	"fmt"
	"sort"
	"strings"
)

// What a custom tool's `defaults:` may set, per action (#328, #321).
//
// Defaults were merged by one hand-written function per action, and each
// covered a different subset: full_text_search took `limit` and nothing else,
// so a tool configured with `algorithm: bm25`, `highlight: true` and
// `filterMeta` ran with tf-idf, no highlights, stemming on — and searched the
// whole collection, outside the scope the operator had pinned. Nothing said
// so. One table now says which keys each action accepts and what argument
// each becomes; a key set for an action that does not take it fails
// validation at startup, as an unknown action does.

// defaultArg maps one `defaults:` key to the built-in tool argument it sets.
type defaultArg struct {
	key string // the YAML key under defaults
	arg string // the built-in tool's argument
	get func(d MCPCustomToolDefs) (any, bool)
}

func intArg(v int) (any, bool)       { return float64(v), v != 0 }
func floatArg(v float64) (any, bool) { return v, v != 0 }
func strArg(v string) (any, bool)    { return v, v != "" }
func boolArg(v *bool) (any, bool)    { return v != nil && *v, v != nil }

// listArg converts to []interface{}, the shape a tool sees when the same list
// arrives from a client as JSON.
func listArg(v []string) (any, bool) {
	out := make([]interface{}, len(v))
	for i, x := range v {
		out[i] = x
	}
	return out, len(v) > 0
}
func metaArg(v map[string][]string) (any, bool) {
	return mcpMetaToInterface(v), v != nil
}

var customToolDefaultArgs = []defaultArg{
	{"topK", "top_k", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.TopK) }},
	{"threshold", "threshold", func(d MCPCustomToolDefs) (any, bool) { return floatArg(d.Threshold) }},
	{"filterMeta", "filter_meta", func(d MCPCustomToolDefs) (any, bool) { return metaArg(d.FilterMeta) }},
	{"sort", "sort", func(d MCPCustomToolDefs) (any, bool) { return strArg(d.Sort) }},
	{"asc", "asc", func(d MCPCustomToolDefs) (any, bool) { return boolArg(d.Asc) }},
	{"limit", "limit", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.Limit) }},
	{"offset", "offset", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.Offset) }},
	{"algorithm", "algorithm", func(d MCPCustomToolDefs) (any, bool) { return strArg(d.Algorithm) }},
	{"fuzzy", "fuzzy", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.Fuzzy) }},
	{"highlight", "highlight", func(d MCPCustomToolDefs) (any, bool) { return boolArg(d.Highlight) }},
	{"highlightTag", "highlight_tag", func(d MCPCustomToolDefs) (any, bool) { return strArg(d.HighlightTag) }},
	{"maxHighlights", "max_highlights", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.MaxHighlights) }},
	{"fragmentSize", "fragment_size", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.FragmentSize) }},
	{"disableStem", "disable_stem", func(d MCPCustomToolDefs) (any, bool) { return boolArg(d.DisableStem) }},
	{"disableSynonyms", "disable_synonyms", func(d MCPCustomToolDefs) (any, bool) { return boolArg(d.DisableSynonyms) }},
	{"mode", "mode", func(d MCPCustomToolDefs) (any, bool) { return strArg(d.Mode) }},
	{"strategy", "strategy", func(d MCPCustomToolDefs) (any, bool) { return strArg(d.Strategy) }},
	{"alpha", "alpha", func(d MCPCustomToolDefs) (any, bool) { return floatArg(d.Alpha) }},
	{"oversample", "oversample", func(d MCPCustomToolDefs) (any, bool) { return floatArg(d.Oversample) }},
	{"vectorAlgorithm", "vector_algorithm", func(d MCPCustomToolDefs) (any, bool) { return strArg(d.VectorAlgorithm) }},
	{"direction", "direction", func(d MCPCustomToolDefs) (any, bool) { return strArg(d.Direction) }},
	{"depth", "depth", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.Depth) }},
	{"maxDegree", "max_degree", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.MaxDegree) }},
	{"lines", "lines", func(d MCPCustomToolDefs) (any, bool) { return boolArg(d.Lines) }},
	{"groupBy", "facets", func(d MCPCustomToolDefs) (any, bool) { return listArg(d.GroupBy) }},
	{"maxFacetSize", "max_facet_size", func(d MCPCustomToolDefs) (any, bool) { return intArg(d.MaxFacetSize) }},
}

// customToolActions lists, for each action a custom tool may wrap, the
// defaults keys it accepts beyond collection, query, includeContent and
// fields. Every action here is read-only: a custom tool exists to give a
// locked-down surface, and none of these writes.
var customToolActions = map[string][]string{
	"semantic_search":  {"topK", "threshold", "filterMeta"},
	"search_documents": {"sort", "asc", "limit", "offset", "filterMeta"},
	"full_text_search": {"limit", "filterMeta", "algorithm", "fuzzy", "highlight", "highlightTag",
		"maxHighlights", "fragmentSize", "disableStem", "disableSynonyms", "mode"},
	"hybrid_search": {"topK", "threshold", "filterMeta", "algorithm", "fuzzy", "strategy", "alpha",
		"oversample", "vectorAlgorithm"},
	"code_graph":    {"direction", "depth", "maxDegree", "lines"},
	"aggregate":     {"filterMeta", "groupBy", "maxFacetSize"},
	"fts_languages": {},
}

// customToolActionNames returns the supported actions, sorted, for messages.
func customToolActionNames() string {
	names := make([]string, 0, len(customToolActions))
	for a := range customToolActions {
		names = append(names, a)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// mcpMergeActionDefaults fills the defaults an action accepts into merged.
func mcpMergeActionDefaults(action string, d MCPCustomToolDefs, merged map[string]interface{}) error {
	allowed, ok := customToolActions[action]
	if !ok {
		return fmt.Errorf("unknown custom tool action: %s", action)
	}
	accept := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		accept[k] = true
	}
	for _, da := range customToolDefaultArgs {
		if !accept[da.key] {
			continue
		}
		if v, set := da.get(d); set {
			merged[da.arg] = v
		}
	}
	return nil
}

// validateCustomToolDefaults rejects a default the action would ignore.
func validateCustomToolDefaults(action string, d MCPCustomToolDefs) error {
	accept := map[string]bool{}
	for _, k := range customToolActions[action] {
		accept[k] = true
	}
	var stray []string
	for _, da := range customToolDefaultArgs {
		if _, set := da.get(d); set && !accept[da.key] {
			stray = append(stray, da.key)
		}
	}
	if len(stray) > 0 {
		return fmt.Errorf("defaults %s do not apply to action %s (it accepts: %s)",
			strings.Join(stray, ", "), action, strings.Join(customToolActions[action], ", "))
	}
	return nil
}
