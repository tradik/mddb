// Package codeintel extracts the symbols a source document defines and uses
// (CODE-004).
//
// Full-text search answers "where does this word appear". The question a person
// editing a theme has is "where is this *defined*" — and search cannot tell a
// definition from a mention, so every file that merely uses `.hero-banner`
// ranks alongside the one stylesheet that declares it.
//
// The symbols go into the ordinary flat meta map, which is already filterable,
// so `defines=.hero-banner` works through the existing metadata filter with no
// new query surface. They are also the raw material for the link graph
// (CODE-005): one document's `uses` matched against another's `defines` is an
// edge.
//
// Extraction is by scanner rather than by parser. A parser per language is a
// dependency, a version to track and a failure mode for malformed input, for
// gains this does not need: HTML, CSS and JS have distinctive enough surface
// syntax that a scanner finds the declarations people actually search for.
package codeintel

import (
	"regexp"
	"sort"
	"strings"
)

// Symbols is what a document declares, references and imports.
//
// All three are sorted and deduplicated, so indexing the same content twice
// produces identical meta — which matters because these bytes travel through
// the replication binlog.
type Symbols struct {
	// Defines are the names this document declares: CSS selectors, JS
	// functions and classes, HTML element ids.
	Defines []string
	// Uses are the names it references: CSS classes applied in HTML, handler
	// names, assets loaded.
	Uses []string
	// Imports are the modules and stylesheets it pulls in.
	Imports []string
}

// Empty reports whether nothing was found.
func (s Symbols) Empty() bool {
	return len(s.Defines) == 0 && len(s.Uses) == 0 && len(s.Imports) == 0
}

// DefaultMaxSymbols bounds each list. A generated or minified file can mention
// thousands of names, and meta is loaded with the document — an unbounded list
// would make every read of that document expensive to serve a case nobody
// queries.
const DefaultMaxSymbols = 512

var (
	// CSS: rule selectors up to the opening brace, and custom properties.
	cssVarRe    = regexp.MustCompile(`(--[A-Za-z_][\w-]*)\s*:`)
	cssImportRe = regexp.MustCompile(`@import\s+(?:url\()?["']([^"')]+)`)
	cssURLRe    = regexp.MustCompile(`url\(\s*["']?([^"')]+)`)

	// JS/TS declarations. Arrow functions assigned to a const are the common
	// modern shape and are matched with the plain declarations.
	jsFuncRe   = regexp.MustCompile(`(?m)\bfunction\s+([A-Za-z_$][\w$]*)`)
	jsClassRe  = regexp.MustCompile(`(?m)\bclass\s+([A-Za-z_$][\w$]*)`)
	jsConstFn  = regexp.MustCompile(`(?m)\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\(|function\b)`)
	jsExportRe = regexp.MustCompile(`(?m)\bexport\s+(?:default\s+)?(?:async\s+)?(?:function|class|const|let|var)\s+([A-Za-z_$][\w$]*)`)
	jsImportRe = regexp.MustCompile(`(?:\bimport\b[^;'"]*from\s*|\bimport\s*|\brequire\s*\(\s*)["']([^"']+)["']`)

	// HTML.
	htmlIDRe      = regexp.MustCompile(`(?i)\bid\s*=\s*["']([^"']+)["']`)
	htmlClassRe   = regexp.MustCompile(`(?i)\bclass\s*=\s*["']([^"']+)["']`)
	htmlSrcHrefRe = regexp.MustCompile(`(?i)\b(?:src|href)\s*=\s*["']([^"']+)["']`)
	htmlHandlerRe = regexp.MustCompile(`(?i)\bon[a-z]+\s*=\s*["']\s*([A-Za-z_$][\w$]*)\s*\(`)

	// Shared: strip comments before scanning, so a commented-out rule is not
	// reported as a declaration.
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCommentRe  = regexp.MustCompile(`(?m)//.*$`)
	htmlCommentRe  = regexp.MustCompile(`(?s)<!--.*?-->`)
)

// Extract finds the symbols in content for the given language.
//
// An unknown language yields nothing rather than a guess: reporting selectors
// from a file that is not CSS would put noise into the graph, and a wrong edge
// is worse than a missing one.
func Extract(language, content string) Symbols {
	return ExtractWithLimit(language, content, DefaultMaxSymbols)
}

// ExtractWithLimit is Extract with an explicit cap per list.
func ExtractWithLimit(language, content string, max int) Symbols {
	if max <= 0 {
		max = DefaultMaxSymbols
	}
	var s Symbols
	switch strings.ToLower(language) {
	case "css", "scss", "sass", "less":
		s = extractCSS(content)
	case "javascript", "typescript":
		s = extractJS(content)
	case "html", "htm":
		s = extractHTML(content)
	default:
		return Symbols{}
	}
	s.Defines = normalise(s.Defines, max)
	s.Uses = normalise(s.Uses, max)
	s.Imports = normalise(s.Imports, max)
	return s
}

// normalise deduplicates, sorts and caps a list. Sorting is what makes the
// result reproducible; the cap is what keeps a generated file from bloating
// the document's meta.
func normalise(in []string, max int) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func stripComments(s string) string {
	s = blockCommentRe.ReplaceAllString(s, " ")
	return lineCommentRe.ReplaceAllString(s, "")
}

// cssSelectors returns the selector text preceding every rule block.
//
// It walks the source rather than matching line-anchored patterns, because the
// three shapes that matter most in a real theme all put a selector somewhere
// other than the start of a line: rules nested inside `@media`, SCSS/LESS
// nesting, and minified stylesheets where the whole file is one line. A
// line-anchored scan finds one selector in a 400-rule minified file.
func cssSelectors(body string) []string {
	const maxSelectorLen = 200 // beyond this it is malformed input, not a name

	var out []string
	start := 0
	for i, r := range body {
		switch r {
		case '{', '}', ';':
			if r == '{' {
				sel := strings.TrimSpace(body[start:i])
				// At-rule preludes (`@media (...)`) name a condition, not a
				// symbol; a quote means we captured a declaration value such
				// as `content: "{"` rather than a selector.
				if sel != "" && len(sel) <= maxSelectorLen &&
					!strings.HasPrefix(sel, "@") &&
					!strings.ContainsAny(sel, `"'`) {
					out = append(out, sel)
				}
			}
			start = i + 1
		}
	}
	return out
}

func extractCSS(content string) Symbols {
	body := blockCommentRe.ReplaceAllString(content, " ")
	var s Symbols

	for _, rule := range cssSelectors(body) {
		// A rule's selector list may name several things: ".a, .b > .c".
		for _, sel := range strings.Split(rule, ",") {
			sel = strings.TrimSpace(sel)
			if sel == "" || strings.HasPrefix(sel, "@") {
				continue
			}
			s.Defines = append(s.Defines, sel)
			// The individual classes and ids in a compound selector are what
			// an HTML document will reference, so record them too.
			s.Defines = append(s.Defines, selectorParts(sel)...)
		}
	}
	for _, m := range cssVarRe.FindAllStringSubmatch(body, -1) {
		s.Defines = append(s.Defines, m[1])
	}
	for _, m := range cssImportRe.FindAllStringSubmatch(body, -1) {
		s.Imports = append(s.Imports, m[1])
	}
	for _, m := range cssURLRe.FindAllStringSubmatch(body, -1) {
		if !strings.HasPrefix(m[1], "data:") {
			s.Uses = append(s.Uses, m[1])
		}
	}
	return s
}

// selectorParts pulls the individual class and id names out of a compound
// selector, so `.card > .title#main` yields .card, .title and #main.
func selectorParts(sel string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 1 { // more than the leading . or #
			out = append(out, cur.String())
		}
		cur.Reset()
	}
	for _, r := range sel {
		switch {
		case r == '.' || r == '#':
			flush()
			cur.WriteRune(r)
		case r == '-' || r == '_' || r == ':' && cur.Len() > 0:
			if r == ':' {
				flush() // a pseudo-class ends the name
				continue
			}
			if cur.Len() > 0 {
				cur.WriteRune(r)
			}
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			if cur.Len() > 0 {
				cur.WriteRune(r)
			}
		default:
			flush()
		}
	}
	flush()
	return out
}

func extractJS(content string) Symbols {
	body := stripComments(content)
	var s Symbols

	for _, re := range []*regexp.Regexp{jsFuncRe, jsClassRe, jsConstFn, jsExportRe} {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			s.Defines = append(s.Defines, m[1])
		}
	}
	for _, m := range jsImportRe.FindAllStringSubmatch(body, -1) {
		s.Imports = append(s.Imports, m[1])
	}
	return s
}

func extractHTML(content string) Symbols {
	body := htmlCommentRe.ReplaceAllString(content, " ")
	var s Symbols

	for _, m := range htmlIDRe.FindAllStringSubmatch(body, -1) {
		s.Defines = append(s.Defines, "#"+m[1])
	}
	for _, m := range htmlClassRe.FindAllStringSubmatch(body, -1) {
		// class="a b c" references three separate classes.
		for _, cls := range strings.Fields(m[1]) {
			s.Uses = append(s.Uses, "."+cls)
		}
	}
	for _, m := range htmlSrcHrefRe.FindAllStringSubmatch(body, -1) {
		ref := m[1]
		// Only local assets are edges; an external URL or a fragment link
		// points at nothing this collection holds.
		if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "data:") ||
			strings.Contains(ref, "://") || strings.HasPrefix(ref, "//") ||
			strings.HasPrefix(ref, "mailto:") || strings.HasPrefix(ref, "tel:") {
			continue
		}
		s.Imports = append(s.Imports, ref)
	}
	for _, m := range htmlHandlerRe.FindAllStringSubmatch(body, -1) {
		s.Uses = append(s.Uses, m[1])
	}
	return s
}
