package main

// Output schemas of the MCP tools (#461).
//
// A tool listed in mcpToolOutputSchemas answers every successful call with
// structuredContent (mcpToolResult), and a client may refuse an answer that
// does not validate. So each schema describes the answer as it is on the wire,
// in every output form the tool has (full, fields projection, passages,
// group_by, a capped limit): a property has the type it has whenever it is
// present, nothing is required, and no property is forbidden, because the
// forms differ in which fields they carry. TestMCPOutputSchemas_StructuredContent
// checks each schema against real answers, and that each property it names
// turns up in at least one of them.

// outType is a schema of one JSON type, or of any of several.
func outType(types ...string) map[string]interface{} {
	if len(types) == 1 {
		return map[string]interface{}{"type": types[0]}
	}
	return map[string]interface{}{"type": types}
}

// outObject is an object schema with the given properties.
func outObject(props map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": props}
}

// outArray is an array schema whose items follow items.
func outArray(items map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": items}
}

// outMap is an object whose every value follows values (a map keyed by name).
func outMap(values map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "object", "additionalProperties": values}
}

// outWith is props plus extra; extra wins on a shared name.
func outWith(props, extra map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(props)+len(extra))
	for k, v := range props {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// outDocumentProps is a document in a search answer. Timestamps are RFC 3339
// strings; a fields projection keeps only id, key and meta.
func outDocumentProps() map[string]interface{} {
	return map[string]interface{}{
		"id":         outType("string"),
		"key":        outType("string"),
		"lang":       outType("string"),
		"meta":       outType("object"),
		"content_md": outType("string"),
		"added_at":   outType("string"),
		"updated_at": outType("string"),
	}
}

// outLimitProps are set when the server's cap lowered the limit (#368).
func outLimitProps() map[string]interface{} {
	return map[string]interface{}{
		"limit":       outType("integer"),
		"limitCapped": outType("boolean"),
	}
}

// outGroupProps are set on a result when the request had group_by (SRCH-016).
func outGroupProps() map[string]interface{} {
	return map[string]interface{}{
		"group":     outType("string"),
		"groupRank": outType("integer"),
	}
}

// outPassages is the answer's list in format "passages" (AGT-002). Scored
// tools carry each passage's score; search_documents does not rank.
func outPassages(scored bool) map[string]interface{} {
	props := map[string]interface{}{
		"collection": outType("string"),
		"key":        outType("string"),
		"lang":       outType("string"),
		"path":       outType("string"),
		"lines":      outObject(map[string]interface{}{"start": outType("integer"), "end": outType("integer")}),
		"content":    outType("string"),
		"source":     outType("string"),
		"tokens":     outType("integer"),
	}
	if scored {
		props["score"] = outType("number")
	}
	return outArray(outObject(props))
}

// outSearchPlan is how a vector search ran, with explain (SRCH-013).
func outSearchPlan() map[string]interface{} {
	return outObject(map[string]interface{}{
		"algorithm":  outType("string"),
		"candidates": outType("integer"),
		"prefilter":  outType("boolean"),
		"reasons":    outArray(outType("string")),
	})
}

// outFacets is value counts by metadata key.
func outFacets() map[string]interface{} {
	return outMap(outArray(outObject(map[string]interface{}{
		"value": outType("string"),
		"count": outType("integer"),
	})))
}

// mcpToolOutputSchemas maps tool name → JSON Schema for structured output.
// Only tools with well-defined output shapes are included.
var mcpToolOutputSchemas = map[string]map[string]interface{}{
	"get_stats": outObject(map[string]interface{}{
		"database_path":      outType("string"),
		"database_size":      outType("integer"),
		"mode":               outType("string"),
		"total_documents":    outType("integer"),
		"total_revisions":    outType("integer"),
		"total_meta_indices": outType("integer"),
		// null on a server without any collection.
		"collections": outWith(outType("array", "null"), map[string]interface{}{
			"items": outObject(map[string]interface{}{
				"name":             outType("string"),
				"document_count":   outType("integer"),
				"revision_count":   outType("integer"),
				"meta_index_count": outType("integer"),
			}),
		}),
	}),
	"search_documents": outObject(outWith(outLimitProps(), map[string]interface{}{
		"total":     outType("integer"),
		"documents": outArray(outObject(outWith(outDocumentProps(), map[string]interface{}{"revision": outType("string")}))),
		"passages":  outPassages(false),
	})),
	"semantic_search": outObject(outWith(outLimitProps(), map[string]interface{}{
		"total":          outType("integer"),
		"model":          outType("string"),
		"dimensions":     outType("integer"),
		"algorithm":      outType("string"),
		"distanceMetric": outType("string"),
		"plan":           outSearchPlan(),
		"passages":       outPassages(true),
		"results": outArray(outObject(outWith(outGroupProps(), map[string]interface{}{
			"score":      outType("number"),
			"rank":       outType("integer"),
			"document":   outObject(outDocumentProps()),
			"chunkIndex": outType("integer"),
			"chunkText":  outType("string"),
			"startLine":  outType("integer"),
			"endLine":    outType("integer"),
		}))),
	})),
	"full_text_search": outObject(outWith(outLimitProps(), map[string]interface{}{
		"total":     outType("integer"),
		"algorithm": outType("string"),
		"fuzzy":     outType("integer"),
		"lang":      outType("string"),
		"facets":    outFacets(),
		"passages":  outPassages(true),
		"results": outArray(outObject(outWith(outGroupProps(), map[string]interface{}{
			"score":        outType("number"),
			"matchedTerms": outArray(outType("string")),
			"document":     outObject(outDocumentProps()),
			"highlights": outArray(outObject(map[string]interface{}{
				"fragment":     outType("string"),
				"matchedTerms": outArray(outType("string")),
				"startOffset":  outType("integer"),
				"endOffset":    outType("integer"),
				"startLine":    outType("integer"),
				"endLine":      outType("integer"),
			})),
		}))),
	})),
	"hybrid_search": outObject(outWith(outLimitProps(), map[string]interface{}{
		"total":           outType("integer"),
		"strategy":        outType("string"),
		"ftsAlgorithm":    outType("string"),
		"vectorAlgorithm": outType("string"),
		"distanceMetric":  outType("string"),
		"plan":            outSearchPlan(),
		"passages":        outPassages(true),
		"results": outArray(outObject(outWith(outGroupProps(), map[string]interface{}{
			"combinedScore": outType("number"),
			"ftsScore":      outType("number"),
			"vectorScore":   outType("number"),
			"rank":          outType("integer"),
			"matchedTerms":  outArray(outType("string")),
			"document":      outObject(outDocumentProps()),
		}))),
	})),
	"vector_stats": outObject(map[string]interface{}{
		"provider":   outType("string"),
		"model":      outType("string"),
		"dimensions": outType("integer"),
		"enabled":    outType("boolean"),
		"collections": outMap(outObject(map[string]interface{}{
			"total_documents":    outType("integer"),
			"embedded_documents": outType("integer"),
		})),
	}),
	"get_checksum": outObject(map[string]interface{}{
		"collection":    outType("string"),
		"checksum":      outType("string"),
		"documentCount": outType("integer"),
	}),
	"classify_document": outObject(map[string]interface{}{
		"model":      outType("string"),
		"dimensions": outType("integer"),
		"results": outArray(outObject(map[string]interface{}{
			"label": outType("string"),
			"score": outType("number"),
		})),
	}),
	"aggregate": outObject(map[string]interface{}{
		"collection": outType("string"),
		"totalDocs":  outType("integer"),
		"durationMs": outType("integer"),
		"facets":     outFacets(),
		"histograms": outMap(outArray(outObject(map[string]interface{}{
			"key":   outType("string"),
			"from":  outType("integer"),
			"to":    outType("integer"),
			"count": outType("integer"),
		}))),
	}),
}
