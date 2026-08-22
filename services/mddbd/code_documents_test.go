package main

import (
	"testing"

	"mddb/internal/fts"
	"mddb/internal/storage"
	proto "mddb/proto"
)

// CODE-001. Storing code needs no new document type — only a convention on the
// flat meta map. These pin the convention, including the precedence that lets
// a caller override the guess.

func docWith(key string, meta map[string][]string) *storage.Doc {
	return &storage.Doc{Key: key, Meta: meta}
}

func TestInferCodeLanguageFromExtension(t *testing.T) {
	for key, want := range map[string]string{
		"css/style.css":       "css",
		"templates/page.html": "html",
		"js/app.mjs":          "javascript",
		"src/index.tsx":       "typescript",
		"config.yaml":         "yaml",
		"CSS/STYLE.CSS":       "css", // extension matching is case-insensitive
		"guides/intro.md":     "",    // prose, deliberately not code
		"notes":               "",    // no extension
		"data/export.csv":     "",    // not source: tokenising it would index noise
	} {
		if got := InferCodeLanguage(key); got != want {
			t.Errorf("InferCodeLanguage(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestDocumentKindPrefersExplicitMeta(t *testing.T) {
	// An explicit kind wins over the extension, in both directions.
	if got := DocumentKind(docWith("guides/intro.md", map[string][]string{"kind": {"code"}})); got != fts.KindCode {
		t.Errorf("an explicit kind=code should win over a .md key, got %q", got)
	}
	if got := DocumentKind(docWith("css/style.css", map[string][]string{"kind": {"prose"}})); got != "prose" {
		t.Errorf("an explicit kind should not be second-guessed by the extension, got %q", got)
	}
}

func TestDocumentKindFallsBackToTheExtension(t *testing.T) {
	if got := DocumentKind(docWith("css/style.css", nil)); got != fts.KindCode {
		t.Errorf("a .css document with no meta should be treated as code, got %q", got)
	}
	if got := DocumentKind(docWith("guides/intro.md", nil)); got != "" {
		t.Errorf("a markdown document is prose, got %q", got)
	}
	if got := DocumentKind(nil); got != "" {
		t.Errorf("DocumentKind(nil) = %q, want empty", got)
	}
}

// A theme ingested with slugs for keys keeps its path in meta; that is what the
// extension should be read from.
func TestDocumentKindUsesThePathWhenTheKeyIsASlug(t *testing.T) {
	doc := docWith("theme-main-stylesheet", map[string][]string{"path": {"css/style.css"}})
	if got := DocumentKind(doc); got != fts.KindCode {
		t.Errorf("the path should decide when the key carries no extension, got %q", got)
	}
	if got := CodeLanguage(doc); got != "css" {
		t.Errorf("CodeLanguage = %q, want css", got)
	}
}

func TestCodeLanguagePrecedence(t *testing.T) {
	// Explicit language wins over the extension.
	doc := docWith("css/style.css", map[string][]string{"kind": {"code"}, "language": {"scss"}})
	if got := CodeLanguage(doc); got != "scss" {
		t.Errorf("an explicit language should win, got %q", got)
	}
	// Prose has no code language, whatever meta says about language.
	prose := docWith("guides/intro.md", map[string][]string{"language": {"css"}})
	if got := CodeLanguage(prose); got != "" {
		t.Errorf("a prose document has no code language, got %q", got)
	}
	if got := CodeLanguage(nil); got != "" {
		t.Errorf("CodeLanguage(nil) = %q, want empty", got)
	}
}

// End to end: a stylesheet written through the batch path must be findable by
// selector, by either word of it, and by a property value.
func TestCodeDocumentIsSearchableBySelector(t *testing.T) {
	s, cleanup := newTestServerWithFTS(t)
	defer cleanup()

	css := `.hero-banner { background: url(hero.png); }
.checkoutButton { color: #ff0000; }
a { text-decoration: none; }`

	_, processed, err := s.processBatchWithDocs(t.Context(), "theme", []*proto.BatchDocument{{
		Key:       "css/style.css",
		Lang:      "en",
		ContentMd: css,
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.firePostBatchHooks("theme", processed, postBatchOptions{})

	for _, query := range []string{
		"hero-banner",    // the selector itself
		"hero",           // one word of it
		"banner",         // the other
		"checkoutButton", // camelCase identifier
		"checkout",       // its parts
		"button",
		"background",      // a property
		"text-decoration", // a hyphenated property
	} {
		res, err := s.FTSIndex.Search("theme", query, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) == 0 {
			t.Errorf("query %q found nothing in the stylesheet", query)
		}
	}
}

// Markdown must keep behaving exactly as before — the convention is opt-in.
func TestProseDocumentsAreUnaffected(t *testing.T) {
	s, cleanup := newTestServerWithFTS(t)
	defer cleanup()

	_, processed, err := s.processBatchWithDocs(t.Context(), "docs", []*proto.BatchDocument{{
		Key:       "guides/intro.md",
		Lang:      "en",
		ContentMd: "# Getting started\n\nThe quick brown foxes are jumping over lazy dogs.",
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.firePostBatchHooks("docs", processed, postBatchOptions{})

	// Stemming still applies to prose: a search for the singular finds the plural.
	res, err := s.FTSIndex.Search("docs", "fox", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Error("prose lost its stemming: searching \"fox\" should find \"foxes\"")
	}
}

// newTestServerWithFTS builds a server with a working full-text index, which
// the default test server leaves out.
func newTestServerWithFTS(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := newTestServer(t)
	s.FTSIndex = fts.NewFTSIndex(s.DB)
	if err := s.FTSIndex.EnsureBuckets(); err != nil {
		cleanup()
		t.Fatal(err)
	}
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
	s.FTSIndex.SetLangRegistry(reg)
	return s, cleanup
}
