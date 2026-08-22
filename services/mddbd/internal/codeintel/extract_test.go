package codeintel

import (
	"fmt"
	"strings"
	"testing"
)

// CODE-004. Full-text search cannot tell a definition from a mention, so every
// file using `.hero-banner` ranks with the one that declares it. These pin
// what counts as a declaration.

func joined(v []string) string { return strings.Join(v, " ") }

func hasAll(t *testing.T, got []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("missing %q; got [%s]", w, joined(got))
		}
	}
}

func hasNone(t *testing.T, got []string, unwanted ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, u := range unwanted {
		if set[u] {
			t.Errorf("unexpected %q in [%s]", u, joined(got))
		}
	}
}

func TestExtractCSS(t *testing.T) {
	css := `/* .commented-out { color: red } */
:root {
  --brand: #2563eb;
}

.hero-banner, .hero-banner .title {
  background: url("/images/hero.png");
  color: var(--brand);
}

#nav > .item:hover {
  color: red;
}

@import url("reset.css");
`
	s := extract("css", css)

	hasAll(t, s.Defines, ".hero-banner", ".title", "#nav", ".item", "--brand")
	// A commented-out rule is not a declaration.
	hasNone(t, s.Defines, ".commented-out")
	// A pseudo-class is not part of the name people search for.
	hasNone(t, s.Defines, ".item:hover", ":hover")

	hasAll(t, s.Imports, "reset.css")
	hasAll(t, s.Uses, "/images/hero.png")
}

func TestExtractCSSIgnoresDataURIs(t *testing.T) {
	s := extract("css", `.icon { background: url(data:image/png;base64,AAAA); }`)
	for _, u := range s.Uses {
		if strings.HasPrefix(u, "data:") {
			t.Errorf("a data URI is not a link to anything: %q", u)
		}
	}
}

func TestExtractJavaScript(t *testing.T) {
	js := `// import "commented-out";
import { render } from "./render.js";
import helpers from "../lib/helpers";
const legacy = require("./legacy");

export function mountWidget(el) {}
export default class WidgetController {}
const handleClick = (e) => {};
let boot = async function () {};
const notAFunction = 42;
`
	s := extract("javascript", js)

	hasAll(t, s.Defines, "mountWidget", "WidgetController", "handleClick", "boot")
	// A plain value is not a declaration worth indexing as a symbol.
	hasNone(t, s.Defines, "notAFunction")

	hasAll(t, s.Imports, "./render.js", "../lib/helpers", "./legacy")
	hasNone(t, s.Imports, "commented-out")
}

func TestExtractHTML(t *testing.T) {
	html := `<!-- <div id="commented"></div> -->
<section id="hero" class="hero-banner dark">
  <h2 class="title">Hi</h2>
  <img src="/images/hero.png" alt="">
  <a href="https://example.com/external">out</a>
  <a href="#anchor">anchor</a>
  <link rel="stylesheet" href="css/style.css">
  <script src="js/app.js"></script>
  <button onclick="handleClick(event)">go</button>
</section>
`
	s := extract("html", html)

	hasAll(t, s.Defines, "#hero")
	hasNone(t, s.Defines, "#commented")

	hasAll(t, s.Uses, ".hero-banner", ".dark", ".title", "handleClick")
	hasAll(t, s.Imports, "/images/hero.png", "css/style.css", "js/app.js")
	// External URLs and fragments point at nothing this collection holds.
	hasNone(t, s.Imports, "https://example.com/external", "#anchor")
}

// The result goes into meta and travels through the replication binlog, so the
// same input must produce the same bytes.
func TestExtractionIsDeterministic(t *testing.T) {
	css := `.z {} .a {} .m {} .a {} #q {} --dup: 1;`
	first := extract("css", css)
	for range 20 {
		again := extract("css", css)
		if joined(again.Defines) != joined(first.Defines) {
			t.Fatalf("extraction is not reproducible:\n %s\n %s", joined(first.Defines), joined(again.Defines))
		}
	}
	// Sorted and deduplicated.
	for i := 1; i < len(first.Defines); i++ {
		if first.Defines[i-1] >= first.Defines[i] {
			t.Errorf("output is not sorted and deduplicated: %s", joined(first.Defines))
			break
		}
	}
}

func TestExtractionIsCapped(t *testing.T) {
	var b strings.Builder
	for i := range 2000 {
		b.WriteString(".rule-")
		b.WriteString(strings.Repeat("x", 1))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(string(rune('a' + (i/26)%26)))
		b.WriteString(string(rune('a' + (i/676)%26)))
		b.WriteString(" { color: red }\n")
	}
	s := ExtractWithLimit("css", b.String(), 50)
	if len(s.Defines) > 50 {
		t.Errorf("the cap was not applied: %d symbols", len(s.Defines))
	}
	if len(s.Defines) == 0 {
		t.Error("the cap should trim, not empty, the list")
	}
}

// A wrong edge is worse than a missing one, so an unknown language yields
// nothing rather than a guess.
func TestUnknownLanguageYieldsNothing(t *testing.T) {
	for _, lang := range []string{"", "markdown", "csv", "python", "unknown"} {
		if s := extract(lang, ".a { color: red }\nfunction f() {}"); !s.Empty() {
			t.Errorf("language %q produced symbols: %+v", lang, s)
		}
	}
}

func TestEmptyContent(t *testing.T) {
	for _, lang := range []string{"css", "javascript", "html"} {
		if s := extract(lang, ""); !s.Empty() {
			t.Errorf("%s: empty content produced %+v", lang, s)
		}
	}
}

// Minified and malformed input must not panic or produce absurd output — a
// scanner meets both in a real theme.
func TestHostileInput(t *testing.T) {
	inputs := map[string]string{
		"minified css":     strings.Repeat(".a{color:red}.b{color:blue}", 200),
		"unclosed rule":    ".broken { color: red",
		"unclosed tag":     `<div id="x" class="y"`,
		"stray braces":     "}}}{{{",
		"unclosed comment": "/* never ends .a { }",
	}
	for name, in := range inputs {
		for _, lang := range []string{"css", "javascript", "html"} {
			s := ExtractWithLimit(lang, in, 100)
			for _, list := range [][]string{s.Defines, s.Uses, s.Imports} {
				if len(list) > 100 {
					t.Errorf("%s/%s: cap exceeded (%d)", name, lang, len(list))
				}
			}
		}
	}
}

// A stylesheet from the issue #192 case: the selector must be findable as a
// declaration, distinct from documents that merely apply it.
func TestThemeCaseSeparatesDefinitionFromUse(t *testing.T) {
	css := extract("css", `.hero-banner { background: url(hero.png); }`)
	html := extract("html", `<section class="hero-banner"></section>`)

	hasAll(t, css.Defines, ".hero-banner")
	hasNone(t, css.Uses, ".hero-banner")

	hasAll(t, html.Uses, ".hero-banner")
	hasNone(t, html.Defines, ".hero-banner")
}

func TestScssAndLessUseTheCSSExtractor(t *testing.T) {
	for _, lang := range []string{"scss", "sass", "less"} {
		if s := extract(lang, ".nested { .inner { color: red } }"); len(s.Defines) == 0 {
			t.Errorf("%s produced no symbols", lang)
		}
	}
}

func TestTypeScriptUsesTheJSExtractor(t *testing.T) {
	s := extract("typescript", `import type { X } from "./types";
export function convert(v: X): string { return ""; }`)
	hasAll(t, s.Defines, "convert")
	hasAll(t, s.Imports, "./types")
}

// A caller passing 0 (an unset config field) must get the default cap, not an
// empty result.
func TestNonPositiveLimitFallsBackToDefault(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if s := ExtractWithLimit("css", ".a {} .b {}", limit); len(s.Defines) == 0 {
			t.Errorf("limit %d produced nothing instead of using the default", limit)
		}
	}
}

// At-rules and empty selector slots come from ordinary stylesheets; neither is
// a symbol anyone would search for.
func TestAtRulesAndBlankSelectorsAreSkipped(t *testing.T) {
	s := extract("css", `@media (min-width: 40em) { .responsive { color: red } }
, .after-blank {}
@keyframes spin { from {} to {} }`)

	for _, d := range s.Defines {
		if strings.HasPrefix(d, "@") || d == "" {
			t.Errorf("not a symbol: %q", d)
		}
	}
	hasAll(t, s.Defines, ".responsive", ".after-blank")
}

// A shipped theme is usually minified and usually has media queries. A scan
// anchored to line starts finds one selector in either.
func TestSelectorsFoundRegardlessOfLayout(t *testing.T) {
	cases := map[string]string{
		"minified":     `.a{color:red}.hero-banner{color:blue}#nav{color:green}`,
		"media query":  `@media (min-width: 40em) { .hero-banner { color: red } #nav {} .a{} }`,
		"scss nesting": `.wrap { .a { color: red } .hero-banner { #nav { color: red } } }`,
	}
	for name, css := range cases {
		s := extract("css", css)
		hasAll(t, s.Defines, ".a", ".hero-banner", "#nav")
		if t.Failed() {
			t.Fatalf("layout %q lost selectors", name)
		}
	}
}

func TestMinifiedStylesheetFindsEveryRule(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, ".r%03d{color:red}", i)
	}
	s := ExtractWithLimit("css", b.String(), 1000)
	if len(s.Defines) < 200 {
		t.Errorf("found %d of 200 rules in a minified stylesheet", len(s.Defines))
	}
}

// A brace inside a declaration value is not the start of a rule.
func TestQuotedBraceIsNotASelector(t *testing.T) {
	s := extract("css", `.real { content: "{"; }`)
	hasAll(t, s.Defines, ".real")
	for _, d := range s.Defines {
		if strings.Contains(d, "content") {
			t.Errorf("a declaration was recorded as a selector: %q", d)
		}
	}
}

// extract is ExtractWithLimit at the default cap — the shape every server path
// uses when no per-request limit applies.
func extract(language, content string) Symbols {
	return ExtractWithLimit(language, content, 0)
}
