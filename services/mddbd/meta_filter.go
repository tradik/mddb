package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	json "mddb/internal/jsonx"
)

// Metadata filter operators (SRCH-017).
//
// filterMeta answers one question — "is this value one of these" — and every
// surface that took it wrote its own index walk for it. MetaFilter keeps that
// question and adds the ones agents actually ask: "path starts with internal/",
// "modified after September", "not a test", "has a summary".
//
// One condition per meta key; keys are ANDed, as in filterMeta. Inside one
// condition the value operators (in, prefix, contains, gt/gte/lt/lte) are
// ANDed and must hold for the same value; a document with several values for
// the key matches when any one of them does. not and exists are judged on the
// whole document, so "not: [test]" also keeps documents without the key.
//
// filterMeta is the special case {key: {in: values}} and is evaluated by the
// same code: see filterMetaAsFilter.

// MetaFilter maps a metadata key to the condition its values must meet.
type MetaFilter map[string]MetaCondition

// MetaCondition is the set of operators applied to one metadata key.
// The zero value matches every document.
type MetaCondition struct {
	In       []string `json:"in,omitempty"`
	Prefix   string   `json:"prefix,omitempty"`
	Contains string   `json:"contains,omitempty"`
	Gt       *string  `json:"gt,omitempty"`
	Gte      *string  `json:"gte,omitempty"`
	Lt       *string  `json:"lt,omitempty"`
	Lte      *string  `json:"lte,omitempty"`
	Not      []string `json:"not,omitempty"`
	Exists   *bool    `json:"exists,omitempty"`

	// hasIn records that "in" was given even when empty: filterMeta with an
	// empty value list has always matched nothing, and stays that way.
	hasIn bool
}

// metaFilterOperators lists the accepted operator names, for the 400 message.
var metaFilterOperators = []string{"in", "prefix", "contains", "gt", "gte", "lt", "lte", "not", "exists"}

// UnmarshalJSON rejects operators it does not know, so a typo is a 400 that
// names the operator instead of a filter that silently matches everything.
// Bounds accept JSON numbers as well as strings; meta values are strings
// (DOC-012) and a number is compared as one.
func (c *MetaCondition) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("filter condition must be an object of operators: %w", err)
	}
	for op, v := range raw {
		var err error
		switch op {
		case "in":
			c.In, err = stringList(v)
			c.hasIn = true
		case "not":
			c.Not, err = stringList(v)
		case "prefix":
			c.Prefix, err = scalarString(v)
		case "contains":
			c.Contains, err = scalarString(v)
		case "gt", "gte", "lt", "lte":
			var s string
			if s, err = scalarString(v); err == nil {
				*c.bound(op) = &s
			}
		case "exists":
			var b bool
			if err = json.Unmarshal(v, &b); err == nil {
				c.Exists = &b
			}
		default:
			return fmt.Errorf("unsupported filter operator %q (supported: %s)", op, strings.Join(metaFilterOperators, ", "))
		}
		if err != nil {
			return fmt.Errorf("filter operator %q: %w", op, err)
		}
	}
	return nil
}

// MarshalJSON keeps an explicit empty "in", which matches nothing, distinct
// from an absent one, which matches everything — the search cache keys on the
// marshalled request, so the two must not serialize alike.
func (c MetaCondition) MarshalJSON() ([]byte, error) {
	type plain MetaCondition
	out := struct {
		plain
		In *[]string `json:"in,omitempty"`
	}{plain: plain(c)}
	if c.hasIn || len(c.In) > 0 {
		in := c.In
		if in == nil {
			in = []string{}
		}
		out.In = &in
	}
	return json.Marshal(out)
}

func (c *MetaCondition) bound(op string) **string {
	switch op {
	case "gt":
		return &c.Gt
	case "gte":
		return &c.Gte
	case "lt":
		return &c.Lt
	default:
		return &c.Lte
	}
}

// scalarString accepts a JSON string, number or boolean and returns its text.
func scalarString(v json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s, nil
	}
	t := string(bytes.TrimSpace(v))
	if _, err := strconv.ParseFloat(t, 64); err == nil || t == "true" || t == "false" {
		return t, nil
	}
	return "", fmt.Errorf("expected a string or number, got %s", t)
}

// stringList accepts one scalar or an array of scalars.
func stringList(v json.RawMessage) ([]string, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(v, &arr); err != nil {
		s, err := scalarString(v)
		if err != nil {
			return nil, err
		}
		return []string{s}, nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, err := scalarString(item)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// hasValuePredicate reports whether the condition constrains individual values.
func (c MetaCondition) hasValuePredicate() bool {
	return c.hasIn || len(c.In) > 0 || c.Prefix != "" || c.Contains != "" ||
		c.Gt != nil || c.Gte != nil || c.Lt != nil || c.Lte != nil
}

// matchValue reports whether one value satisfies every value operator.
// contains is case-insensitive; in, prefix and the bounds are exact.
func (c MetaCondition) matchValue(v string) bool {
	if (c.hasIn || len(c.In) > 0) && !containsString(c.In, v) {
		return false
	}
	if c.Prefix != "" && !strings.HasPrefix(v, c.Prefix) {
		return false
	}
	if c.Contains != "" && !strings.Contains(strings.ToLower(v), strings.ToLower(c.Contains)) {
		return false
	}
	return (c.Gt == nil || compareMeta(v, *c.Gt) > 0) &&
		(c.Gte == nil || compareMeta(v, *c.Gte) >= 0) &&
		(c.Lt == nil || compareMeta(v, *c.Lt) < 0) &&
		(c.Lte == nil || compareMeta(v, *c.Lte) <= 0)
}

// compareMeta orders a metadata value against a bound the way rangeMeta
// always has: numerically when both are numbers, by time when the value is a
// date and the bound is a date or a unix timestamp, lexically otherwise.
func compareMeta(a, b string) int {
	fa, errA := strconv.ParseFloat(a, 64)
	fb, errB := strconv.ParseFloat(b, 64)
	if errA == nil && errB == nil {
		return cmpOrdered(fa, fb)
	}
	if ta, err := parseFlexibleDate(a); err == nil {
		if tb, err := parseFlexibleDate(b); err == nil {
			return cmpOrdered(ta.Unix(), tb.Unix())
		}
		if errB == nil {
			return cmpOrdered(float64(ta.Unix()), fb)
		}
	}
	return strings.Compare(a, b)
}

func cmpOrdered[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Matches evaluates the filter against a document's metadata in memory, for
// paths that already hold the document (memory sessions, custom tools).
func (f MetaFilter) Matches(meta map[string][]string) bool {
	for key, c := range f {
		vals, has := meta[key]
		has = has && len(vals) > 0
		if c.Exists != nil && *c.Exists != has {
			return false
		}
		if c.hasValuePredicate() && !anyValue(vals, c.matchValue) {
			return false
		}
		if len(c.Not) > 0 && anyValue(vals, func(v string) bool { return containsString(c.Not, v) }) {
			return false
		}
	}
	return true
}

func anyValue(vals []string, pred func(string) bool) bool {
	for _, v := range vals {
		if pred(v) {
			return true
		}
	}
	return false
}

// filterMetaAsFilter turns the equality form into the operator form, so both
// are evaluated by one implementation.
func filterMetaAsFilter(fm map[string][]string) MetaFilter {
	if len(fm) == 0 {
		return nil
	}
	f := make(MetaFilter, len(fm))
	for k, vals := range fm {
		f[k] = MetaCondition{In: vals, hasIn: true}
	}
	return f
}

// mergeFilters ANDs filterMeta and filter. A key present in both keeps both
// constraints: the equality list is folded into the operator condition.
func mergeFilters(fm map[string][]string, f MetaFilter) MetaFilter {
	out := filterMetaAsFilter(fm)
	if len(f) == 0 {
		return out
	}
	if out == nil {
		out = make(MetaFilter, len(f))
	}
	for k, c := range f {
		if eq, dup := out[k]; dup {
			c.In = intersectStrings(eq.In, c.In, c.hasIn)
			c.hasIn = true
		}
		out[k] = c
	}
	return out
}

func intersectStrings(eq, in []string, hasIn bool) []string {
	if !hasIn {
		return eq
	}
	out := []string{}
	for _, v := range eq {
		if containsString(in, v) {
			out = append(out, v)
		}
	}
	return out
}

// sortedKeys returns the filter keys in a stable order for deterministic walks.
func (f MetaFilter) sortedKeys() []string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
