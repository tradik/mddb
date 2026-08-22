package schema

import (
	"strings"
	"testing"
)

// DOC-012 / issue #187. A false alarm on legitimate text costs more than a
// missed stringification: the first teaches callers to ignore warnings, the
// second is still findable in the document.

// The exact repro from the issue: an ssg `faq:` list of objects, stringified
// by a naive importer.
func TestLintCatchesTheIssue187Repro(t *testing.T) {
	meta := map[string][]string{
		"faq": {"[map[answer:Yes, it is free. question:Is it free?] map[answer:No. question:Is there a trial?]]"},
	}
	warnings := LintMeta(meta)
	if len(warnings) != 1 {
		t.Fatalf("the repro from #187 produced %d warnings, want 1: %+v", len(warnings), warnings)
	}
	if warnings[0].Key != "faq" {
		t.Errorf("warning names key %q, want faq", warnings[0].Key)
	}
	// The message has to say what to do, not only that something is wrong.
	for _, want := range []string{"flat", "JSON", "docs/API.md"} {
		if !strings.Contains(warnings[0].Message, want) {
			t.Errorf("message does not mention %q: %s", want, warnings[0].Message)
		}
	}
}

func TestLintCatchesASingleStringifiedMap(t *testing.T) {
	meta := map[string][]string{
		"schema": {"map[@type:Recipe name:Pancakes]"},
	}
	if got := LintMeta(meta); len(got) != 1 {
		t.Errorf("a stringified map produced %d warnings: %+v", len(got), got)
	}
}

// Everything here is text someone legitimately stored. A warning on any of it
// is a bug.
func TestLintStaysQuietOnOrdinaryValues(t *testing.T) {
	meta := map[string][]string{
		"tag":         {"go", "rust", "python"},
		"title":       {"How to read a map[string]int in Go"},
		"note":        {"[a b]", "[draft]", "[]"},
		"code":        {"map[string]string"},
		"description": {"The map[] syntax confuses beginners"},
		"empty":       {""},
		"short":       {"map[a]"},
		"path":        {"maps/europe.svg"},
		"json":        {`{"question":"Is it free?","answer":"Yes"}`},
		"list":        {"[1, 2, 3]"},
	}
	if got := LintMeta(meta); len(got) != 0 {
		t.Errorf("ordinary values raised warnings: %+v", got)
	}
}

// The JSON-in-one-value pattern is what the docs recommend, so it must never
// warn.
func TestLintApprovesTheRecommendedWorkaround(t *testing.T) {
	meta := map[string][]string{
		"faq": {`[{"question":"Is it free?","answer":"Yes"}]`},
	}
	if got := LintMeta(meta); len(got) != 0 {
		t.Errorf("the documented workaround warns: %+v", got)
	}
}

func TestLintOnEmptyMeta(t *testing.T) {
	if got := LintMeta(nil); got != nil {
		t.Errorf("nil meta gave %+v", got)
	}
	if got := LintMeta(map[string][]string{}); got != nil {
		t.Errorf("empty meta gave %+v", got)
	}
}

// The same document must lint the same way every time — warnings reach callers
// that diff them.
func TestLintIsDeterministic(t *testing.T) {
	meta := map[string][]string{
		"zeta":  {"map[a:1]"},
		"alpha": {"map[b:2]"},
		"mid":   {"map[c:3]", "map[d:4]"},
	}
	first := LintMetaStrings(meta)
	if len(first) != 4 {
		t.Fatalf("expected 4 warnings, got %d: %v", len(first), first)
	}
	for range 10 {
		again := LintMetaStrings(meta)
		if strings.Join(again, "|") != strings.Join(first, "|") {
			t.Fatalf("warning order varies:\n %v\n %v", first, again)
		}
	}
	if !strings.HasPrefix(first[0], "meta.alpha") {
		t.Errorf("warnings are not sorted by key: %v", first)
	}
}

// A whole stringified FAQ in a warning message is unreadable.
func TestLintTruncatesLongValues(t *testing.T) {
	long := "map[question:" + strings.Repeat("x", 300) + " answer:yes]"
	got := LintMeta(map[string][]string{"faq": {long}})
	if len(got) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(got))
	}
	if len(got[0].Value) > 90 {
		t.Errorf("value not truncated: %d chars", len(got[0].Value))
	}
	if !strings.HasSuffix(got[0].Value, "…") {
		t.Errorf("truncation is not marked: %q", got[0].Value)
	}
}

func TestLintMetaStringsFormat(t *testing.T) {
	got := LintMetaStrings(map[string][]string{"faq": {"map[a:1]"}})
	if len(got) != 1 || !strings.HasPrefix(got[0], "meta.faq: ") {
		t.Errorf("unexpected format: %v", got)
	}
	if got := LintMetaStrings(nil); got != nil {
		t.Errorf("nil meta gave %v", got)
	}
}

func TestMetaWarningString(t *testing.T) {
	w := MetaWarning{Key: "faq", Message: "looks wrong"}
	if w.String() != "meta.faq: looks wrong" {
		t.Errorf("String() = %q", w.String())
	}
}
