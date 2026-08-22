package schema

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Metadata lint (DOC-012, issue #187).
//
// MDDB's meta is deliberately flat: map<string, repeated string>, the same
// shape in proto, REST, GraphQL and MCP. Structured frontmatter — an `faq:`
// list of objects, a `schema:` Recipe JSON-LD block — does not fit, and a naive
// importer reaches for Go's `%v`. The result is a value like
//
//	map[answer:Yes question:Is it free?]
//
// which MDDB stores faithfully, because it is a valid string. The damage
// surfaces much later, in a template that cannot render it (#187).
//
// So: a warning, never an error. Rejecting the value would break every caller
// legitimately storing text that happens to look like this, and MDDB has no
// business deciding that a string is not what its author meant. The warning
// says what was probably intended and where the alternatives are documented.

// goMapPattern matches Go's fmt %v rendering of a map: `map[` followed by at
// least one `key:value` pair.
//
// The pair is required. Bare `map[...]` with no colon is far more likely to be
// prose about maps than a stringified one.
var goMapPattern = regexp.MustCompile(`^map\[[^\[\]]*:[^\[\]]*\]$`)

// goSliceOfMapsPattern matches `[map[a:1] map[b:2]]` — a stringified list of
// objects, which is exactly the `faq:` case from #187.
var goSliceOfMapsPattern = regexp.MustCompile(`^\[\s*map\[.*\]\s*\]$`)

// MetaWarning is one lint finding: which key tripped it, and what to do.
type MetaWarning struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Message string `json:"message"`
}

func (w MetaWarning) String() string {
	return fmt.Sprintf("meta.%s: %s", w.Key, w.Message)
}

const flatMetaDocs = "see docs/API.md#structured-frontmatter-and-flat-meta"

// LintMeta reports metadata values that look like a structure lost on the way
// in.
//
// The bar is deliberately high: a false alarm on legitimate text costs more
// than a missed stringification, because the first teaches callers to ignore
// warnings and the second is still findable in the document. Results are sorted
// so the same document always lints the same way.
func LintMeta(meta map[string][]string) []MetaWarning {
	if len(meta) == 0 {
		return nil
	}

	var out []MetaWarning
	for key, values := range meta {
		for _, v := range values {
			if w, found := lintValue(key, v); found {
				out = append(out, w)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func lintValue(key, value string) (MetaWarning, bool) {
	trimmed := strings.TrimSpace(value)
	// Anything this short cannot be a stringified structure and is very
	// likely to be ordinary text.
	if len(trimmed) < len("map[a:b]") {
		return MetaWarning{}, false
	}

	switch {
	case goSliceOfMapsPattern.MatchString(trimmed):
		return MetaWarning{
			Key:   key,
			Value: truncateForMessage(trimmed),
			Message: "looks like a Go-stringified list of objects (`[map[...] map[...]]`). " +
				"Meta is flat (map<string, repeated string>), so the structure was lost on the way in. " +
				"Store it as JSON in one value, flatten the keys, or leave it in the markdown body — " +
				flatMetaDocs,
		}, true

	case goMapPattern.MatchString(trimmed):
		return MetaWarning{
			Key:   key,
			Value: truncateForMessage(trimmed),
			Message: "looks like a Go-stringified map (`map[key:value]`). " +
				"Meta is flat (map<string, repeated string>), so the structure was lost on the way in. " +
				"Store it as JSON in one value, flatten the keys, or leave it in the markdown body — " +
				flatMetaDocs,
		}, true
	}

	return MetaWarning{}, false
}

// truncateForMessage keeps a warning readable when the offending value is a
// whole stringified FAQ.
func truncateForMessage(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// LintMetaStrings returns the warnings as plain sentences, for the transports
// that carry a list of strings rather than a structure.
func LintMetaStrings(meta map[string][]string) []string {
	warnings := LintMeta(meta)
	if len(warnings) == 0 {
		return nil
	}
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w.String())
	}
	return out
}
