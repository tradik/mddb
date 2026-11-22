package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	json "mddb/internal/jsonx"
)

// Parsing of POST /qdrant/collections/{name}/points/query (#447): the body
// qdrant-client's query_points sends over REST. It serialises its
// QueryRequest model with exclude_none, so a field the caller left unset is
// absent; another client may send it as null, which counts as absent too.

// qdrantQueryDefaultLimit is Qdrant's limit when the request has none.
const qdrantQueryDefaultLimit = 10

// qdrantQuery is a parsed point query.
type qdrantQuery struct {
	Vector      []float32
	Limit       int
	Offset      int
	Threshold   float64
	WithPayload bool
}

// qdrantKnownKeys are the fields the subset reads; params (hnsw_ef, exact,
// quantization) tunes Qdrant's own index, and MDDB's planner picks the
// algorithm; with_vector false is the default.
var qdrantKnownKeys = map[string]bool{
	"query": true, "using": true, "limit": true, "offset": true, "score_threshold": true,
	"with_payload": true, "with_vector": true, "params": true,
}

// parseQdrantQuery reads a query body. Each refusal names the field, so a
// caller sees what to change rather than an empty answer.
func parseQdrantQuery(body []byte) (*qdrantQuery, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return nil, errors.New("body: must be a JSON object")
	}
	if err := qdrantOnlyKnownKeys(obj); err != nil {
		return nil, err
	}
	q := &qdrantQuery{Limit: qdrantQueryDefaultLimit, WithPayload: true}
	var err error
	if q.Vector, err = qdrantVector(obj["query"]); err != nil {
		return nil, err
	}
	if using := qdrantString(obj["using"]); using != "" {
		return nil, fmt.Errorf("using: an MDDB collection has one unnamed vector; leave the vector name empty (got %q)", using)
	}
	if err := qdrantSet(obj["limit"], &q.Limit, "limit", 1); err != nil {
		return nil, err
	}
	if err := qdrantSet(obj["offset"], &q.Offset, "offset", 0); err != nil {
		return nil, err
	}
	if err := qdrantSet(obj["score_threshold"], &q.Threshold, "score_threshold", math.Inf(-1)); err != nil {
		return nil, err
	}
	if err := qdrantSet(obj["with_payload"], &q.WithPayload, "with_payload (true or false; payload selectors are not supported)", false); err != nil {
		return nil, err
	}
	withVector := false
	if err := qdrantSet(obj["with_vector"], &withVector, "with_vector", false); err != nil || withVector {
		return nil, errors.New("with_vector: only false is supported; MDDB does not return stored vectors")
	}
	return q, nil
}

// qdrantOnlyKnownKeys refuses a field the subset does not translate —
// filter, prefetch, lookup_from, shard_key — rather than ignore it and
// return results the caller did not ask for.
func qdrantOnlyKnownKeys(obj map[string]json.RawMessage) error {
	var unknown []string
	for k, v := range obj {
		if !qdrantKnownKeys[k] && !qdrantNull(v) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s: not supported by the MDDB Qdrant-compatible subset", strings.Join(unknown, ", "))
}

// qdrantVector reads query: a dense vector, or {"nearest": vector}, the
// two forms a nearest-neighbour query takes. Every other query kind
// (recommend, discover, fusion, order_by, a point id, a sparse vector) is
// refused.
func qdrantVector(raw json.RawMessage) ([]float32, error) {
	const msg = "query: must be a dense vector (a list of numbers) or {\"nearest\": vector}"
	if qdrantNull(raw) {
		return nil, errors.New(msg)
	}
	var nearest struct {
		Nearest json.RawMessage `json:"nearest"`
	}
	if json.Unmarshal(raw, &nearest) == nil && nearest.Nearest != nil {
		raw = nearest.Nearest
	}
	var vec []float32
	if err := json.Unmarshal(raw, &vec); err != nil || len(vec) == 0 {
		return nil, errors.New(msg)
	}
	return vec, nil
}

// qdrantSet decodes raw into dst unless it is absent; a number below lowest
// is refused (lowest applies to int and float64 destinations only).
func qdrantSet[T int | float64 | bool](raw json.RawMessage, dst *T, field string, lowest T) error {
	if qdrantNull(raw) {
		return nil
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil || !qdrantAtLeast(v, lowest) {
		return fmt.Errorf("%s: invalid value %s", field, raw)
	}
	*dst = v
	return nil
}

// qdrantAtLeast compares numbers; booleans have no bound.
func qdrantAtLeast[T int | float64 | bool](v, lowest T) bool {
	switch n := any(v).(type) {
	case int:
		return n >= any(lowest).(int)
	case float64:
		return n >= any(lowest).(float64)
	}
	return true
}

// qdrantString reads an optional string; anything else reads as itself, so
// a non-string using is still named in the refusal.
func qdrantString(raw json.RawMessage) string {
	if qdrantNull(raw) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return string(raw)
	}
	return s
}

// qdrantNull reports an absent or null field.
func qdrantNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}
