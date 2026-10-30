package main

import (
	"slices"
	"strings"
	"time"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"
)

// The mapping between an Elasticsearch _source and an MDDB document
// (INT-019): "content" is the body, "lang" the language, and every other
// top-level field is metadata. Metadata values are strings (DOC-012), so a
// number or a boolean comes back as its text.

// esDocument is a _source mapped onto an MDDB document.
type esDocument struct {
	Content string
	Lang    string
	Meta    map[string][]string
}

// esFromSource maps a _source object. A nested object has no metadata
// counterpart and is refused, naming the field, rather than flattened in a
// way the caller did not choose.
func esFromSource(raw []byte) (*esDocument, error) {
	obj, err := esObject(raw, "_source")
	if err != nil {
		return nil, err
	}
	doc := &esDocument{Meta: map[string][]string{}}
	for field, v := range obj {
		if string(v) == "null" {
			continue
		}
		switch field {
		case esContentField, esLangField:
			var s string
			if json.Unmarshal(v, &s) != nil {
				return nil, dslErr("_source."+field, "must be a string")
			}
			if field == esContentField {
				doc.Content = s
			} else {
				doc.Lang = s
			}
			continue
		}
		values, err := stringList(v)
		if err != nil {
			return nil, dslErr("_source."+field, "must be a string, number, boolean or a list of them: nested objects are not supported")
		}
		doc.Meta[field] = values
	}
	return doc, nil
}

// esSource maps a document back onto a _source, keeping only includes when
// the request named fields.
func esSource(doc *storage.Doc, includes []string) map[string]any {
	src := make(map[string]any, len(doc.Meta)+2)
	for k, vals := range doc.Meta {
		if len(vals) == 1 {
			src[k] = vals[0]
		} else {
			src[k] = vals
		}
	}
	src[esContentField] = doc.ContentMD
	src[esLangField] = doc.Lang
	if len(includes) == 0 || slices.Contains(includes, "*") {
		return src
	}
	kept := make(map[string]any, len(includes))
	for _, f := range includes {
		if v, ok := src[strings.TrimSuffix(f, ".keyword")]; ok {
			kept[f] = v
		}
	}
	return kept
}

// esShards is the _shards block of a response: MDDB is one shard.
func esShards() map[string]int {
	return map[string]int{"total": 1, "successful": 1, "skipped": 0, "failed": 0}
}

// esSearchResponse builds the _search response.
func esSearchResponse(index string, spec *esSearchSpec, res *esResult, took time.Duration) map[string]any {
	hits := make([]map[string]any, 0, len(res.Hits))
	var maxScore *float64
	for _, h := range res.Hits {
		hit := map[string]any{"_index": index, "_id": h.Doc.Key, "_score": h.Score}
		if h.Score != nil && (maxScore == nil || *h.Score > *maxScore) {
			maxScore = h.Score
		}
		if !spec.Source.Off {
			hit["_source"] = esSource(&h.Doc, spec.Source.Includes)
		}
		if len(h.Highlight) > 0 {
			hit["highlight"] = map[string][]string{esContentField: h.Highlight}
		}
		hits = append(hits, hit)
	}
	out := map[string]any{
		"took":      took.Milliseconds(),
		"timed_out": false,
		"_shards":   esShards(),
		"hits": map[string]any{
			"total":     map[string]any{"value": res.Total, "relation": "eq"},
			"max_score": maxScore,
			"hits":      hits,
		},
	}
	if len(spec.Aggs) > 0 {
		out["aggregations"] = esAggregations(spec.Aggs, res.Facets)
	}
	if res.Limit.LimitCapped {
		out["limitCapped"], out["limit"] = true, res.Limit.Limit
	}
	return out
}

// esAggregations shapes facets as terms aggregations: the top size buckets,
// and the documents counted in the buckets left out.
func esAggregations(aggs []esTermsAgg, facets FacetResult) map[string]any {
	out := make(map[string]any, len(aggs))
	for _, a := range aggs {
		all := facets[a.Field]
		top := all[:min(a.Size, len(all))]
		other := 0
		for _, b := range all[len(top):] {
			other += b.Count
		}
		buckets := make([]map[string]any, 0, len(top))
		for _, b := range top {
			buckets = append(buckets, map[string]any{"key": b.Value, "doc_count": b.Count})
		}
		out[a.Name] = map[string]any{"doc_count_error_upper_bound": 0, "sum_other_doc_count": other, "buckets": buckets}
	}
	return out
}
