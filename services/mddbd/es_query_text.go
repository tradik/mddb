package main

import (
	"strings"
	"unicode"

	json "mddb/internal/jsonx"
)

// Text clauses of the Query DSL subset (INT-019): match, match_phrase and
// multi_match, reduced to words for the full-text expression.

// errMustBeString is the DSL error for a clause value that is not a string.
const errMustBeString = "must be a string"

// match compiles match and match_phrase: {"field": "text"} or
// {"field": {"query": "text", "operator": "and"}}. The field does not narrow
// the search — see docs/ELASTICSEARCH-COMPAT.md.
func (c *esCompiler) match(name string, body json.RawMessage, scope esScope) (string, error) {
	_, v, err := esSingle(body, name)
	if err != nil {
		return "", err
	}
	text, operator, err := esMatchText(name, v)
	if err != nil {
		return "", err
	}
	if name == "match_phrase" {
		return c.textLeaf(name, text, "phrase", scope)
	}
	return c.textLeaf(name, text, operator, scope)
}

// esMatchText reads a match value: a scalar, or an object with query and
// operator.
func esMatchText(name string, v json.RawMessage) (text, operator string, err error) {
	if text, err = scalarString(v); err == nil {
		return text, "or", nil
	}
	obj, err := esObject(v, name)
	if err != nil {
		return "", "", err
	}
	allowed := []string{"query", "boost"}
	if name == "match" {
		allowed = append(allowed, "operator")
	}
	if err := esOnlyKeys(name, obj, allowed...); err != nil {
		return "", "", err
	}
	if text, err = scalarString(obj["query"]); err != nil {
		return "", "", dslErr(name+".query", errMustBeString)
	}
	operator = "or"
	if raw, ok := obj["operator"]; ok {
		if err := json.Unmarshal(raw, &operator); err != nil {
			return "", "", dslErr(name+".operator", errMustBeString)
		}
		operator = strings.ToLower(operator)
	}
	return text, operator, nil
}

// multiMatch compiles {"query": "...", "fields": [...], "type": ...}. Every
// field is searched as the whole full-text index, so the fields list only
// has to be well formed.
func (c *esCompiler) multiMatch(body json.RawMessage, scope esScope) (string, error) {
	obj, err := esObject(body, "multi_match")
	if err != nil {
		return "", err
	}
	if err := esOnlyKeys("multi_match", obj, "query", "fields", "type", "operator", "boost"); err != nil {
		return "", err
	}
	text, err := scalarString(obj["query"])
	if err != nil {
		return "", dslErr("multi_match.query", errMustBeString)
	}
	if raw, ok := obj["fields"]; ok {
		if _, err := stringList(raw); err != nil {
			return "", dslErr("multi_match.fields", "must be a list of field names")
		}
	}
	var typ, operator string
	for key, dst := range map[string]*string{"type": &typ, "operator": &operator} {
		if raw, ok := obj[key]; ok && json.Unmarshal(raw, dst) != nil {
			return "", dslErr("multi_match."+key, errMustBeString)
		}
	}
	operator = strings.ToLower(operator)
	switch typ {
	case "", "best_fields", "most_fields", "cross_fields":
		if operator == "" {
			operator = "or"
		}
	case "phrase":
		operator = "phrase"
	default:
		return "", dslErr("multi_match.type", "%q is not supported (best_fields, most_fields, cross_fields, phrase)", typ)
	}
	return c.textLeaf("multi_match", text, operator, scope)
}

// textLeaf turns query text into an expression fragment. Words are what the
// full-text index sees anyway; reducing the text to them keeps a quote, a
// parenthesis or an AND typed by a user from being read as operators.
func (c *esCompiler) textLeaf(clause, text, operator string, scope esScope) (string, error) {
	words := esWords(text)
	if len(words) == 0 {
		return "", dslErr(clause, "query text has no searchable words")
	}
	c.textLeaves++
	switch operator {
	case "phrase":
		if len(words) == 1 {
			return words[0], nil
		}
		return `"` + strings.Join(words, " ") + `"`, nil
	case "or":
		if scope == esMust {
			c.simple = strings.Join(words, " ")
		}
		return esJoin(words, " OR "), nil
	case "and":
		return esJoin(words, " AND "), nil
	default:
		return "", dslErr(clause+".operator", "%q is not supported (or, and)", operator)
	}
}

// esWords lowercases text and splits it into letter-and-digit words, which is
// also what keeps AND/OR/NOT from being operators: they are uppercase only.
func esWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

// esJoin joins parts with sep, in parentheses when there is more than one.
func esJoin(parts []string, sep string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, sep) + ")"
}
