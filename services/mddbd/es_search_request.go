package main

import (
	"net/http"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

// Parsing of POST /es/{index}/_search and _count bodies (INT-019): every
// top-level key, sort entry, highlight option and aggregation is either
// translated or refused by name.

// esSearchKeys are the top-level _search keys the subset translates.
var esSearchKeys = []string{"query", "knn", "size", "from", "sort", "_source", "highlight",
	"aggs", "aggregations", "track_total_hits", "timeout"}

// esRefusedParams are URL parameters that would change the answer and are
// not translated; any other parameter is accepted and has no effect.
var esRefusedParams = []string{"q", "sort", "_source", "_source_includes", "_source_excludes",
	"rest_total_hits_as_int", "typed_keys", "search_type", "scroll", "routing"}

const (
	// esDefaultSize is Elasticsearch's default page size.
	esDefaultSize = 10
	// esDefaultAggSize is the default bucket count of a terms aggregation.
	esDefaultAggSize = 10
)

// esSearchSpec is a parsed _search request.
type esSearchSpec struct {
	Query     *esTranslated
	KNN       *esKNN
	From      int
	Size      int
	SortField string // "" = by score; else "key", "addedAt" or "updatedAt"
	SortAsc   bool
	Source    esSourceSpec
	Highlight *esHighlightSpec
	Aggs      []esTermsAgg
}

// esSourceSpec is the _source projection: all fields, none, or a list.
type esSourceSpec struct {
	Off      bool
	Includes []string
}

// esHighlightSpec is the highlight request, mapped onto FTS highlights.
type esHighlightSpec struct {
	Tag          string
	Fragments    int
	FragmentSize int
}

// esTermsAgg is one terms aggregation, answered by facets.
type esTermsAgg struct {
	Name  string
	Field string
	Size  int
}

// parseESSearch reads a _search (or, with count, a _count) body and its URL
// parameters.
func parseESSearch(r *http.Request, body []byte, count bool) (*esSearchSpec, error) {
	for _, p := range esRefusedParams {
		if r.URL.Query().Has(p) {
			return nil, dslErr(p, "URL parameter is not supported; put it in the request body")
		}
	}
	obj := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(body))) > 0 {
		var err error
		if obj, err = esObject(body, "body"); err != nil {
			return nil, err
		}
	}
	allowed := esSearchKeys
	if count {
		allowed = []string{"query"}
	}
	if err := esOnlyKeys("search", obj, allowed...); err != nil {
		return nil, err
	}
	spec := &esSearchSpec{Size: esDefaultSize}
	var err error
	if spec.Query, err = compileESQuery(obj["query"]); err != nil {
		return nil, err
	}
	if spec.KNN, err = parseESKNN(obj["knn"]); err != nil {
		return nil, err
	}
	if err := esPaging(r, obj, spec); err != nil {
		return nil, err
	}
	if spec.SortField, spec.SortAsc, err = parseESSort(obj["sort"]); err != nil {
		return nil, err
	}
	if spec.Source, err = parseESSource(obj["_source"]); err != nil {
		return nil, err
	}
	if spec.Highlight, err = parseESHighlight(obj["highlight"]); err != nil {
		return nil, err
	}
	aggs := obj["aggs"]
	if aggs == nil {
		aggs = obj["aggregations"]
	}
	if spec.Aggs, err = parseESAggs(aggs); err != nil {
		return nil, err
	}
	return spec, nil
}

// esPaging reads size and from, from the body or the URL.
func esPaging(r *http.Request, obj map[string]json.RawMessage, spec *esSearchSpec) error {
	for key, dst := range map[string]*int{"size": &spec.Size, "from": &spec.From} {
		raw := obj[key]
		if raw == nil && r.URL.Query().Has(key) {
			raw = json.RawMessage(r.URL.Query().Get(key))
		}
		if raw == nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || n < 0 {
			return dslErr(key, "must be a whole number, 0 or more")
		}
		*dst = n
	}
	return nil
}

// parseESSort reads sort: "_score", a field name, {"field": "asc"} or
// {"field": {"order": "desc"}}, alone or as a one-element list. MDDB sorts
// by score, key or a timestamp — the orders its search paths produce.
func parseESSort(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	if len(list) != 1 {
		return "", false, dslErr("sort", "only one sort key is supported, got %d", len(list))
	}
	field, order := "", ""
	if json.Unmarshal(list[0], &field) != nil {
		var opts json.RawMessage
		var err error
		if field, opts, err = esSingle(list[0], "sort"); err != nil {
			return "", false, err
		}
		if json.Unmarshal(opts, &order) != nil {
			var o struct {
				Order string `json:"order"`
			}
			if err := json.Unmarshal(opts, &o); err != nil {
				return "", false, dslErr("sort."+field, "must be \"asc\", \"desc\" or {\"order\": ...}")
			}
			order = o.Order
		}
	}
	return esSortField(field, strings.ToLower(order))
}

// esSortField maps a sort field and order onto MDDB's.
func esSortField(field, order string) (string, bool, error) {
	if order != "" && order != "asc" && order != "desc" {
		return "", false, dslErr("sort."+field, "order %q is not asc or desc", order)
	}
	switch field {
	case "_score":
		if order == "asc" {
			return "", false, dslErr("sort._score", "ascending score order is not supported")
		}
		return "", false, nil
	case "_doc", "_id":
		return "key", order != "desc", nil
	case "addedAt", "updatedAt":
		return field, order != "desc", nil
	default:
		return "", false, dslErr("sort."+field, "MDDB sorts by _score, _id or _doc (key), addedAt or updatedAt")
	}
}

// parseESSource reads _source: a bool, a field, a list or {"includes": [...]}.
func parseESSource(raw json.RawMessage) (esSourceSpec, error) {
	if len(raw) == 0 {
		return esSourceSpec{}, nil
	}
	var on bool
	if json.Unmarshal(raw, &on) == nil {
		return esSourceSpec{Off: !on}, nil
	}
	if list, err := stringList(raw); err == nil {
		return esSourceSpec{Includes: list}, nil
	}
	obj, err := esObject(raw, "_source")
	if err != nil {
		return esSourceSpec{}, err
	}
	if err := esOnlyKeys("_source", obj, "includes"); err != nil {
		return esSourceSpec{}, err
	}
	list, err := stringList(obj["includes"])
	if err != nil {
		return esSourceSpec{}, dslErr("_source.includes", "must be a list of field names")
	}
	return esSourceSpec{Includes: list}, nil
}

// parseESHighlight reads highlight. The highlighted field is the document
// content; pre_tags/post_tags are one tag and its closing tag.
func parseESHighlight(raw json.RawMessage) (*esHighlightSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	obj, err := esObject(raw, "highlight")
	if err != nil {
		return nil, err
	}
	if err := esOnlyKeys("highlight", obj, "fields", "pre_tags", "post_tags", "fragment_size", "number_of_fragments", "type"); err != nil {
		return nil, err
	}
	if err := esHighlightFields(obj["fields"]); err != nil {
		return nil, err
	}
	spec := &esHighlightSpec{}
	if err := esHighlightTags(obj, spec); err != nil {
		return nil, err
	}
	if err := esHighlightSizes(obj, spec); err != nil {
		return nil, err
	}
	return spec, nil
}

// esHighlightFields checks highlight.fields, an object or a list of
// one-field objects: only the document body can be highlighted, and without
// per-field options.
func esHighlightFields(f json.RawMessage) error {
	fields, err := esHighlightFieldMap(f)
	if err != nil {
		return err
	}
	for name, opts := range fields {
		path := "highlight.fields." + name
		if name != esContentField && name != "*" {
			return dslErr(path, "only %q (the document body) can be highlighted", esContentField)
		}
		if o, err := esObject(opts, path); err != nil || len(o) > 0 {
			return dslErr(path, "per-field options are not supported; set them on highlight")
		}
	}
	return nil
}

// esHighlightFieldMap reads highlight.fields, an object or a list of
// one-field objects, into one map; empty when absent.
func esHighlightFieldMap(f json.RawMessage) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if f == nil {
		return fields, nil
	}
	var list []map[string]json.RawMessage
	if json.Unmarshal(f, &list) != nil {
		return esObject(f, "highlight.fields")
	}
	for _, m := range list {
		for k, v := range m {
			fields[k] = v
		}
	}
	return fields, nil
}

// esHighlightSizes reads fragment_size and number_of_fragments, whole
// numbers of 1 or more.
func esHighlightSizes(obj map[string]json.RawMessage, spec *esHighlightSpec) error {
	for key, dst := range map[string]*int{"fragment_size": &spec.FragmentSize, "number_of_fragments": &spec.Fragments} {
		if v, ok := obj[key]; ok {
			if n, err := strconv.Atoi(string(v)); err != nil || n < 1 {
				return dslErr("highlight."+key, "must be a whole number, 1 or more")
			} else {
				*dst = n
			}
		}
	}
	return nil
}

// esHighlightTags reads pre_tags and post_tags: MDDB wraps a match in one
// tag and derives the closing one, so post_tags must be that closing tag.
func esHighlightTags(obj map[string]json.RawMessage, spec *esHighlightSpec) error {
	pre, post := []string{"<em>"}, []string{"</em>"}
	for key, dst := range map[string]*[]string{"pre_tags": &pre, "post_tags": &post} {
		if v, ok := obj[key]; ok {
			list, err := stringList(v)
			if err != nil || len(list) != 1 {
				return dslErr("highlight."+key, "must be exactly one tag")
			}
			*dst = list
		}
	}
	name, _, _ := strings.Cut(strings.Trim(pre[0], "<>"), " ")
	if !strings.HasPrefix(pre[0], "<") || post[0] != "</"+name+">" {
		return dslErr("highlight.post_tags", "must close pre_tags (%s → </%s>)", pre[0], name)
	}
	spec.Tag = pre[0]
	return nil
}

// parseESAggs reads aggs: only terms, without sub-aggregations.
func parseESAggs(raw json.RawMessage) ([]esTermsAgg, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	obj, err := esObject(raw, "aggs")
	if err != nil {
		return nil, err
	}
	aggs := make([]esTermsAgg, 0, len(obj))
	for name, def := range obj {
		agg, err := parseESTermsAgg(name, def)
		if err != nil {
			return nil, err
		}
		aggs = append(aggs, agg)
	}
	return aggs, nil
}

// parseESTermsAgg reads one named aggregation, which must be terms with a
// field and an optional size of 1 or more.
func parseESTermsAgg(name string, def json.RawMessage) (esTermsAgg, error) {
	path := "aggs." + name
	kind, body, err := esSingle(def, path)
	if err != nil {
		return esTermsAgg{}, err
	}
	if kind != "terms" {
		return esTermsAgg{}, dslErr(path+"."+kind, "aggregation is not supported; only terms is")
	}
	opts, err := esObject(body, path+".terms")
	if err != nil {
		return esTermsAgg{}, err
	}
	if err := esOnlyKeys(path+".terms", opts, "field", "size"); err != nil {
		return esTermsAgg{}, err
	}
	agg := esTermsAgg{Name: name, Size: esDefaultAggSize}
	var field string
	if json.Unmarshal(opts["field"], &field) != nil {
		return esTermsAgg{}, dslErr(path+".terms.field", "must be a field name")
	}
	if agg.Field, err = esField(path+".terms.field", field); err != nil {
		return esTermsAgg{}, err
	}
	if v, ok := opts["size"]; ok {
		if agg.Size, err = strconv.Atoi(string(v)); err != nil || agg.Size < 1 {
			return esTermsAgg{}, dslErr(path+".terms.size", "must be a whole number, 1 or more")
		}
	}
	return agg, nil
}
