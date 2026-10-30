package main

import (
	"fmt"
	"strings"

	json "mddb/internal/jsonx"
)

// Filter clauses of the Query DSL subset (INT-019): term, terms and range,
// compiled into the operator filter and rangeMeta the search paths take.

// esField maps a field name onto a metadata key. ".keyword" is how an
// Elasticsearch mapping names the exact-value twin of a text field; MDDB
// metadata values are exact already, so the suffix is dropped.
func esField(clause, field string) (string, error) {
	field = strings.TrimSuffix(field, ".keyword")
	switch {
	case field == "" || strings.HasPrefix(field, "_"):
		return "", dslErr(clause, "field %q is not a metadata key", field)
	case field == esContentField || field == esLangField:
		return "", dslErr(clause, "field %q cannot be filtered; use match for the content", field)
	}
	return field, nil
}

// term compiles term ({"f": v} or {"f": {"value": v}}) and terms
// ({"f": [v, ...]}) into an "in" filter, or a "not" one under must_not.
func (c *esCompiler) term(name string, body json.RawMessage, scope esScope) error {
	field, v, err := esSingle(body, name)
	if err != nil {
		return err
	}
	if field, err = esField(name, field); err != nil {
		return err
	}
	var values []string
	if name == "term" {
		values, err = esTermValue(v)
	} else {
		values, err = stringList(v)
	}
	if err != nil {
		return dslErr(name+"."+field, "%v", err)
	}
	cond := c.out.Filter[field]
	switch scope {
	case esMust:
		if cond.In != nil {
			return dslErr(name+"."+field, "two term/terms clauses on one field cannot be ANDed by MDDB filters")
		}
		cond.In = values
	case esMustNot:
		cond.Not = append(cond.Not, values...)
	default:
		return dslErr(name, "is only supported in the query, bool.must, bool.filter and bool.must_not: MDDB filters are ANDed")
	}
	c.out.Filter[field] = cond
	return nil
}

// esTermValue reads a term value: a scalar, or {"value": scalar}.
func esTermValue(v json.RawMessage) ([]string, error) {
	if s, err := scalarString(v); err == nil {
		return []string{s}, nil
	}
	var obj struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(v, &obj); err != nil || obj.Value == nil {
		return nil, fmt.Errorf("must be a value or {\"value\": ...}")
	}
	s, err := scalarString(obj.Value)
	return []string{s}, err
}

// rangeClause compiles {"field": {"gte": a, "lt": b}} into rangeMeta.
func (c *esCompiler) rangeClause(body json.RawMessage, scope esScope) error {
	field, v, err := esSingle(body, "range")
	if err != nil {
		return err
	}
	if field, err = esRangeField(field); err != nil {
		return err
	}
	if scope != esMust {
		return dslErr("range", "is only supported in the query, bool.must and bool.filter")
	}
	obj, err := esObject(v, "range."+field)
	if err != nil {
		return err
	}
	if err := esOnlyKeys("range."+field, obj, "gt", "gte", "lt", "lte", "boost"); err != nil {
		return err
	}
	rf := RangeFilter{Field: field}
	for op, dst := range map[string]*string{"gt": &rf.Gt, "gte": &rf.Gte, "lt": &rf.Lt, "lte": &rf.Lte} {
		if raw, ok := obj[op]; ok {
			if *dst, err = scalarString(raw); err != nil {
				return dslErr("range."+field+"."+op, "%v", err)
			}
		}
	}
	c.out.Ranges = append(c.out.Ranges, rf)
	return nil
}

// esRangeField accepts the two document timestamps besides metadata keys.
func esRangeField(field string) (string, error) {
	if field == "addedAt" || field == "updatedAt" {
		return field, nil
	}
	return esField("range", field)
}
