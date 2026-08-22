package main

import (
	"path"
	"strings"

	"mddb/internal/codeintel"
	"mddb/internal/envconf"

	"mddb/internal/fts"
	"mddb/internal/sliceutil"
	"mddb/internal/storage"
)

// Code documents (CODE-001).
//
// Storing source in MDDB needs no new document type, table or API variant —
// only a convention on the flat meta map every transport already carries:
//
//	meta["kind"]     = ["code"]        selects the source-aware tokeniser
//	meta["language"] = ["css"]         the source language; inferred when absent
//	meta["path"]     = ["css/style.css"]  original path, when the key is a slug
//
// The flat model is deliberate (issue #187), so the convention lives in the
// data rather than in the schema. Nothing here changes proto or storage.

// codeExtensions maps a file extension to the language name recorded in
// meta["language"]. An extension outside this set is not code as far as the
// tokeniser is concerned — marking, say, a .csv as code would split its
// contents on punctuation and index noise.
var codeExtensions = map[string]string{
	".html": "html", ".htm": "html",
	".css": "css", ".scss": "scss", ".sass": "sass", ".less": "less",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript",
	".json": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml",
	".go": "go", ".py": "python", ".rb": "ruby", ".php": "php",
	".rs": "rust", ".java": "java", ".sh": "shell", ".bash": "shell",
	".sql": "sql", ".xml": "xml", ".svg": "svg",
}

// InferCodeLanguage returns the language for a document key or path, or "" when
// the extension is not one this treats as source.
//
// It looks at the key because that is what an ingesting tool has: ssg and
// wpexporter store a theme one file per document, keyed by its path.
func InferCodeLanguage(keyOrPath string) string {
	ext := strings.ToLower(path.Ext(keyOrPath))
	return codeExtensions[ext]
}

// firstMeta returns the first value for a meta key, or "".
func firstMeta(meta map[string][]string, key string) string {
	if vals, ok := meta[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// DocumentKind reports how a document should be tokenised.
//
// An explicit meta["kind"] always wins — a caller that says "code" gets code,
// whatever the extension suggests, and one that says anything else is never
// second-guessed. Only in the absence of a kind is the extension consulted, so
// a theme ingested without the convention still indexes usefully.
func DocumentKind(doc *storage.Doc) string {
	if doc == nil {
		return ""
	}
	if kind := firstMeta(doc.Meta, fts.MetaKeyKind); kind != "" {
		return kind
	}
	source := firstMeta(doc.Meta, fts.MetaKeyPath)
	if source == "" {
		source = doc.Key
	}
	if InferCodeLanguage(source) != "" {
		return fts.KindCode
	}
	return ""
}

// CodeLanguage returns the language recorded for a document, inferring it from
// the path or key when meta does not say. Empty for documents that are not
// code.
func CodeLanguage(doc *storage.Doc) string {
	if doc == nil || DocumentKind(doc) != fts.KindCode {
		return ""
	}
	if lang := firstMeta(doc.Meta, fts.MetaKeyLanguage); lang != "" {
		return lang
	}
	source := firstMeta(doc.Meta, fts.MetaKeyPath)
	if source == "" {
		source = doc.Key
	}
	return InferCodeLanguage(source)
}

// ChunkModeFor reports how a document's content should be segmented.
//
// Precedence: the collection override first — an operator who set
// MDDB_EMBEDDING_CHUNK_MODE meant it — then the document's own kind, then
// prose. The override exists because a collection may hold source whose
// documents were ingested without the convention.
func ChunkModeFor(doc *storage.Doc) ChunkMode {
	switch strings.ToLower(envconf.String("MDDB_EMBEDDING_CHUNK_MODE", "")) {
	case string(ChunkModeCode):
		return ChunkModeCode
	case string(ChunkModeProse):
		return ChunkModeProse
	}
	if DocumentKind(doc) == fts.KindCode {
		return ChunkModeCode
	}
	return ChunkModeProse
}

// Symbol meta keys (CODE-004). These are owned by the extractor: they are
// rewritten on every save of a code document and removed when a document stops
// being code, so they always describe the current content rather than whatever
// a caller once supplied.
const (
	MetaKeyDefines = "defines"
	MetaKeyUses    = "uses"
	MetaKeyImports = "imports"
)

// EnrichCodeSymbols fills a document's meta with what its source declares,
// references and imports.
//
// It runs before the document is written, so the symbols land in the ordinary
// flat meta map and are queryable through the existing metadata filter —
// `defines=.hero-banner` finds the stylesheet that declares the selector,
// without ranking every file that merely applies it alongside.
//
// The three keys are owned by this function. A caller cannot supply them:
// letting a stale hand-written `defines` survive a content change would make
// the graph describe a document that no longer exists.
func EnrichCodeSymbols(doc *storage.Doc) {
	if doc == nil {
		return
	}
	// A document that is no longer code must not keep symbols from when it was.
	if DocumentKind(doc) != fts.KindCode || doc.ContentMD == "" {
		clearSymbolMeta(doc)
		return
	}

	language := CodeLanguage(doc)
	syms := codeintel.ExtractWithLimit(
		language,
		doc.ContentMD,
		envconf.Int("MDDB_CODE_MAX_SYMBOLS", codeintel.DefaultMaxSymbols),
	)

	clearSymbolMeta(doc)
	if syms.Empty() {
		return
	}
	if doc.Meta == nil {
		doc.Meta = make(map[string][]string, 3)
	}
	for key, vals := range map[string][]string{
		MetaKeyDefines: syms.Defines,
		MetaKeyUses:    syms.Uses,
		MetaKeyImports: resolveImportPaths(doc.Key, language, syms.Imports),
	} {
		if len(vals) > 0 {
			doc.Meta[key] = vals
		}
	}
}

func clearSymbolMeta(doc *storage.Doc) {
	if doc.Meta == nil {
		return
	}
	delete(doc.Meta, MetaKeyDefines)
	delete(doc.Meta, MetaKeyUses)
	delete(doc.Meta, MetaKeyImports)
}

// resolveImportPaths rewrites raw import specifiers into collection-relative
// document keys.
//
// `./render.js` inside `assets/app.js` means `assets/render.js`, and that is
// the form the graph needs: an edge between two documents must be answerable
// from the metadata index alone, so the value stored has to equal the key of
// the document it points at. Storing the raw specifier would mean resolving
// paths at query time against every candidate directory.
//
// Resolution is lexical — an extension-less specifier stays extension-less and
// the graph resolver tries the code extensions when it looks the target up.
func resolveImportPaths(fromKey, language string, specs []string) []string {
	if len(specs) == 0 {
		return nil
	}
	dir := path.Dir(fromKey)
	if dir == "." || dir == "/" {
		dir = ""
	}
	bareIsRelative := !isModuleLanguage(language)
	out := make([]string, 0, len(specs))
	for _, spec := range specs {
		out = append(out, resolveImportPath(dir, spec, bareIsRelative))
	}
	return sliceutil.Unique(out)
}

// isModuleLanguage reports whether a bare specifier names a package rather than
// a sibling file.
//
// This is the one place the languages genuinely disagree. In HTML and CSS,
// `href="style.css"` and `@import "reset.css"` are relative to the document —
// resolving them is what makes `<link>` and `@import` produce edges at all. In
// JavaScript, a bare `import "lodash"` names a package that is not a document
// in this collection, and rewriting it to `theme/lodash` would invent an edge.
func isModuleLanguage(language string) bool {
	switch language {
	case "javascript", "typescript", "jsx", "tsx":
		return true
	default:
		return false
	}
}

func resolveImportPath(dir, spec string, bareIsRelative bool) string {
	// An absolute specifier is collection-relative in every language.
	if strings.HasPrefix(spec, "/") {
		return path.Clean(strings.TrimPrefix(spec, "/"))
	}
	explicitlyRelative := strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
	if !explicitlyRelative && !bareIsRelative {
		return path.Clean(spec)
	}
	if dir == "" {
		return path.Clean(spec)
	}
	// path.Clean keeps a leading "../" when the specifier escapes the
	// collection root; that document cannot exist here, but recording it
	// truthfully beats silently pointing the edge somewhere else.
	return path.Clean(dir + "/" + spec)
}
