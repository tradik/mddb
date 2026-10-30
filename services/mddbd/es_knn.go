package main

import (
	"strconv"

	json "mddb/internal/jsonx"
)

// The top-level knn of the Elasticsearch-compatible subset (INT-019), mapped
// onto /v1/vector-search with algorithm "auto" (SRCH-013), or onto
// /v1/hybrid-search (rrf) when the request also has a text query.

// esKNN is a parsed knn section.
type esKNN struct {
	// Vector is query_vector; Text is
	// query_vector_builder.text_embedding.model_text, which MDDB embeds with
	// the collection's own provider. Exactly one is set.
	Vector []float32
	Text   string
	K      int // 0 = the page size
	Filter *esTranslated
}

// parseESKNN reads knn. A list is accepted when it holds one entry.
func parseESKNN(raw json.RawMessage) (*esKNN, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		if len(list) != 1 {
			return nil, dslErr("knn", "only one knn search is supported, got %d", len(list))
		}
		raw = list[0]
	}
	obj, err := esObject(raw, "knn")
	if err != nil {
		return nil, err
	}
	if err := esOnlyKeys("knn", obj, "field", "query_vector", "query_vector_builder", "k", "num_candidates", "filter", "boost"); err != nil {
		return nil, err
	}
	knn := &esKNN{}
	if err := knn.readQuery(obj); err != nil {
		return nil, err
	}
	if v, ok := obj["k"]; ok {
		if knn.K, err = strconv.Atoi(string(v)); err != nil || knn.K < 1 {
			return nil, dslErr("knn.k", "must be a whole number, 1 or more")
		}
	}
	if knn.Filter, err = esKNNFilter(obj["filter"]); err != nil {
		return nil, err
	}
	return knn, nil
}

// readQuery reads what knn searches with: exactly one of query_vector and
// query_vector_builder's text.
func (knn *esKNN) readQuery(obj map[string]json.RawMessage) error {
	if v, ok := obj["query_vector"]; ok {
		if err := json.Unmarshal(v, &knn.Vector); err != nil || len(knn.Vector) == 0 {
			return dslErr("knn.query_vector", "must be a non-empty list of numbers")
		}
	}
	if v, ok := obj["query_vector_builder"]; ok {
		var b struct {
			TextEmbedding struct {
				ModelText string `json:"model_text"`
			} `json:"text_embedding"`
		}
		if err := json.Unmarshal(v, &b); err != nil || b.TextEmbedding.ModelText == "" {
			return dslErr("knn.query_vector_builder", "must be {\"text_embedding\": {\"model_text\": \"...\"}}")
		}
		knn.Text = b.TextEmbedding.ModelText
	}
	if (knn.Vector == nil) == (knn.Text == "") {
		return dslErr("knn", "needs exactly one of query_vector and query_vector_builder")
	}
	return nil
}

// esKNNFilter compiles knn.filter: one query or a list of them, ANDed, which
// may only filter.
func esKNNFilter(raw json.RawMessage) (*esTranslated, error) {
	if len(raw) == 0 {
		return &esTranslated{Filter: MetaFilter{}}, nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		b, _ := json.Marshal(map[string]any{"bool": map[string]any{"filter": list}})
		raw = b
	}
	t, err := compileESQuery(raw)
	if err != nil {
		return nil, err
	}
	if t.hasText() {
		return nil, dslErr("knn.filter", "may hold term, terms, range and bool filters only, not text clauses")
	}
	return t, nil
}

// mergeFilter ANDs the knn filter into the query's. A field both constrain
// with "in" cannot be ANDed by one condition, as in term.
func (t *esTranslated) mergeFilter(o *esTranslated) error {
	for field, c := range o.Filter {
		mine, ok := t.Filter[field]
		if ok && mine.In != nil && c.In != nil {
			return dslErr("knn.filter", "field %q is also filtered by the query", field)
		}
		if c.In != nil {
			mine.In = c.In
		}
		mine.Not = append(mine.Not, c.Not...)
		t.Filter[field] = mine
	}
	t.Ranges = append(t.Ranges, o.Ranges...)
	return nil
}
