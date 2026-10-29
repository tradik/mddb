package main

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

// mcpGetPlanOptions reads explain and latency_target_ms.
func mcpGetPlanOptions(args map[string]interface{}) PlanOptions {
	o := PlanOptions{LatencyTargetMs: mcpGetInt(args, "latency_target_ms")}
	if explain, ok := args["explain"].(bool); ok {
		o.Explain = explain
	}
	return o
}
