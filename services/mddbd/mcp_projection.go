package main

// Response projection for the MCP read path.
//
// Narrow, high-frequency lookups (e.g. a "versions" service that only needs
// name + currentVersion) pay for the full document on every hit: all meta keys
// plus the entire contentMd body. These helpers let a tool drop the body
// (includeContent=false) and/or restrict the returned meta keys (fields=[...]),
// cutting client token usage by 5-30x. Both controls are strictly opt-in — with
// includeContent defaulting to true and empty fields returning everything, the
// default output is byte-for-byte unchanged.
//
// Projection happens here, on the marshaled response, rather than in the storage
// or search core, so it is client-agnostic (works for the direct and remote
// clients alike) and additive.

// mcpProjectionActive reports whether either projection control changes the
// output. When false, callers should marshal the typed response untouched so
// existing behavior is preserved exactly.
func mcpProjectionActive(fields []string, includeContent bool) bool {
	return !includeContent || len(fields) > 0
}

// projectMCPDocument reduces a single document to the requested projection.
//
//   - fields set        -> keep only id, key and the requested meta.<field> keys
//     (the aggressive shape used by narrow lookups).
//   - fields empty      -> keep the full document shape (id, key, lang, meta,
//     timestamps) minus the body.
//   - includeContent    -> content_md is included only when true.
//
// The result is a plain map so absent keys are genuinely omitted from the JSON,
// not emitted as empty values.
func projectMCPDocument(doc MCPDocument, fields []string, includeContent bool) map[string]interface{} {
	m := map[string]interface{}{
		"id":  doc.ID,
		"key": doc.Key,
	}
	if len(fields) > 0 {
		m["meta"] = projectMeta(doc.Meta, fields)
	} else {
		m["lang"] = doc.Lang
		m["meta"] = doc.Meta
		m["added_at"] = doc.AddedAt
		m["updated_at"] = doc.UpdatedAt
	}
	if includeContent {
		m["content_md"] = doc.ContentMD
	}
	return m
}

// projectMeta returns a copy of meta containing only the requested keys.
// Requested keys that are absent from the document are simply skipped, so the
// result never carries empty placeholder entries.
func projectMeta(meta map[string][]string, fields []string) map[string][]string {
	out := make(map[string][]string, len(fields))
	for _, f := range fields {
		if v, ok := meta[f]; ok {
			out[f] = v
		}
	}
	return out
}

// projectSearchResult builds the projected view of a search_documents response.
func projectSearchResult(resp *MCPSearchResponse, fields []string, includeContent bool) map[string]interface{} {
	docs := make([]map[string]interface{}, len(resp.Documents))
	for i := range resp.Documents {
		docs[i] = projectMCPDocument(resp.Documents[i], fields, includeContent)
	}
	return map[string]interface{}{
		"documents": docs,
		"total":     resp.Total,
	}
}

// projectFTSResult builds the projected view of a full_text_search response,
// preserving the per-hit score and matched terms.
func projectFTSResult(resp *MCPFTSSearchResponse, fields []string, includeContent bool) map[string]interface{} {
	results := make([]map[string]interface{}, len(resp.Results))
	for i := range resp.Results {
		results[i] = map[string]interface{}{
			"document":     projectMCPDocument(resp.Results[i].Document, fields, includeContent),
			"score":        resp.Results[i].Score,
			"matchedTerms": resp.Results[i].MatchedTerms,
		}
		// Highlights survive projection deliberately (CODE-002). Projection
		// and highlighting are the two halves of the same request — "do not
		// send me the body, tell me where to look" — so dropping the fragments
		// here would leave the caller with neither the text nor its location,
		// which is the failure issue #192 describes.
		if len(resp.Results[i].Highlights) > 0 {
			results[i]["highlights"] = resp.Results[i].Highlights
		}
	}
	out := map[string]interface{}{
		"results":   results,
		"total":     resp.Total,
		"algorithm": resp.Algorithm,
		"fuzzy":     resp.Fuzzy,
	}
	if resp.Lang != "" {
		out["lang"] = resp.Lang
	}
	return out
}

// projectVectorResult builds the projected view of a semantic_search response,
// preserving the per-hit score and rank plus the embedding metadata.
func projectVectorResult(resp *MCPVectorSearchResponse, fields []string, includeContent bool) map[string]interface{} {
	results := make([]map[string]interface{}, len(resp.Results))
	for i := range resp.Results {
		results[i] = map[string]interface{}{
			"document": projectMCPDocument(resp.Results[i].Document, fields, includeContent),
			"score":    resp.Results[i].Score,
			"rank":     resp.Results[i].Rank,
		}
	}
	return map[string]interface{}{
		"results":        results,
		"total":          resp.Total,
		"model":          resp.Model,
		"dimensions":     resp.Dimensions,
		"algorithm":      resp.Algorithm,
		"distanceMetric": resp.DistanceMetric,
	}
}
