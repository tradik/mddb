package main

import (
	"fmt"

	json "mddb/internal/jsonx"
)

// Arguments the vector-backed search tools (semantic_search, hybrid_search,
// cross_search) share, described and read once.

// mcpVectorAlgorithmDesc describes the vector algorithm argument (SRCH-013).
const mcpVectorAlgorithmDesc = "Vector index: auto (the planner chooses per query from collection size, " +
	"the indexes already built and how much the filter admits — use it rather than guessing), flat (exact, default), " +
	"hnsw, ivf, pq, opq, sq, sq4, bq"

// Planner arguments (SRCH-013).
var (
	mcpExplainSchema = map[string]interface{}{
		"type":        "boolean",
		"description": "Add plan to the response: the vector index that answered, whether the filter was applied first, and why",
	}
	mcpLatencyTargetSchema = map[string]interface{}{
		"type": "integer", "minimum": 0, "maximum": maxLatencyTargetMs,
		"description": "With algorithm auto: a latency goal in ms; 10 or less favours a quantized index on a large collection",
	}
)

// Image arguments (RAG-010).
var (
	mcpIncludeImagesSchema = map[string]interface{}{
		"type": "boolean",
		"description": "Attach each hit's images (alt text, caption, page, url) as images; with a provider that embeds images, " +
			"also return the images whose picture matches the query as imageHits",
	}
	mcpQueryImageSchema = map[string]interface{}{
		"type": "string",
		"description": "Search by an image instead of text: base64, a data URI or an http(s) URL. Needs a provider that embeds " +
			"images (voyage, cohere v3+); the results are image documents, each with meta.parent. query may then be omitted",
	}
	mcpExtractImagesSchema = map[string]interface{}{
		"type": "boolean",
		"description": "Store the images of Markdown documents and PDFs as image documents (kind image, meta.parent, altText, caption, page); " +
			"default on in a collection of type images",
	}
	mcpFetchRemoteImagesSchema = map[string]interface{}{
		"type": "boolean", "description": "With extract_images: fetch Markdown image links to http(s) URLs (SSRF-guarded); off by default",
	}
	mcpImageFilesSchema = map[string]interface{}{
		"type":        "array",
		"description": "The files the Markdown's image links name by relative path, each {path, content_base64}",
		"items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"path":           map[string]interface{}{"type": "string"},
			"content_base64": map[string]interface{}{"type": "string"},
		}},
	}
)

// mcpGetImageOptions reads include_images.
func mcpGetImageOptions(args map[string]interface{}) ImageOptions {
	return ImageOptions{IncludeImages: mcpGetBool(args, "include_images")}
}

// Relevance feedback arguments (SRCH-015).
var (
	mcpFeedbackExampleSchema = map[string]interface{}{
		"type": "array",
		"items": map[string]interface{}{"oneOf": []interface{}{
			map[string]interface{}{"type": "string", "description": "a document key or id from an earlier result"},
			map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}, "description": "a vector"},
		}},
	}
	mcpPositiveSchema = withDescription(mcpFeedbackExampleSchema,
		"Results that were right, by key (or vectors): the search moves towards them. After a first search, send the keys of the hits that answered")
	mcpNegativeSchema = withDescription(mcpFeedbackExampleSchema,
		"Results that were wrong, by key (or vectors): the search moves away from them")
	mcpFeedbackModeSchema = map[string]interface{}{
		"type": "string", "enum": []string{FeedbackModeQuery, FeedbackModeRescore},
		"description": "query (default): move the query vector, free with any index. rescore: re-rank the candidates by similarity to the examples, finer but costlier",
	}
	mcpFeedbackWeightSchema = map[string]interface{}{
		"type": "number", "minimum": 0, "maximum": maxFeedbackWeight,
		"description": "rescore mode: how much the examples move a score (default 0.5)",
	}
	mcpRocchioSchema = map[string]interface{}{
		"type":        "object",
		"description": "query mode weights of q' = alpha·q + beta·mean(positive) − gamma·mean(negative); default 1, 0.75, 0.25",
		"properties": map[string]interface{}{
			"alpha": map[string]interface{}{"type": "number"}, "beta": map[string]interface{}{"type": "number"},
			"gamma": map[string]interface{}{"type": "number"},
		},
	}
)

// withDescription is schema with a description of its own.
func withDescription(schema map[string]interface{}, description string) map[string]interface{} {
	out := make(map[string]interface{}, len(schema)+1)
	for k, v := range schema {
		out[k] = v
	}
	out["description"] = description
	return out
}

// mcpFeedbackProperties adds the feedback arguments to a tool's properties.
func mcpFeedbackProperties(props map[string]interface{}) map[string]interface{} {
	props["positive"] = mcpPositiveSchema
	props["negative"] = mcpNegativeSchema
	props["feedback_mode"] = mcpFeedbackModeSchema
	props["feedback_weight"] = mcpFeedbackWeightSchema
	props["rocchio"] = mcpRocchioSchema
	return props
}

// mcpGetFeedback reads positive, negative, feedback_mode, feedback_weight
// and rocchio, through the same JSON decoding the REST surface uses.
func mcpGetFeedback(args map[string]interface{}) (FeedbackOptions, error) {
	// The arguments came in as JSON, so they marshal back.
	raw, _ := json.Marshal(map[string]interface{}{
		"positive": args["positive"], "negative": args["negative"], "feedbackMode": args["feedback_mode"],
		"feedbackWeight": args["feedback_weight"], "rocchio": args["rocchio"],
	})
	var f FeedbackOptions
	if err := json.Unmarshal(raw, &f); err != nil {
		return FeedbackOptions{}, fmt.Errorf("%w: %w", errFeedbackExample, err)
	}
	return f, nil
}

// mcpGetPlanOptions reads explain and latency_target_ms.
func mcpGetPlanOptions(args map[string]interface{}) PlanOptions {
	o := PlanOptions{LatencyTargetMs: mcpGetInt(args, "latency_target_ms")}
	if explain, ok := args["explain"].(bool); ok {
		o.Explain = explain
	}
	return o
}
