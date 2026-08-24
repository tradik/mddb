package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	"mddb/internal/storage"
	proto "mddb/proto"
)

// CODE-005. The graph answers questions full-text search cannot: what breaks if
// this selector changes, which pages load this script, what nothing references.
// These run against a small theme, because that is the shape of the case in
// issue #192.

// themeFixture writes a theme whose files reference each other the way a real
// one does — HTML applies selectors a stylesheet declares and loads a script,
// the script imports a module, the stylesheet imports a reset.
func themeFixture(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, cleanup := newTestServerForBatch(t)

	code := map[string]*proto.MetaValues{"kind": {Values: []string{"code"}}}
	docs := []*proto.BatchDocument{
		makeBatchDoc("theme/index.html", "en", `<!doctype html>
<section id="hero" class="hero-banner">
  <h2 class="title">Hi</h2>
</section>
<link rel="stylesheet" href="style.css">
<script src="app.js"></script>`, code, false),

		makeBatchDoc("theme/style.css", "en", `@import "reset.css";
.hero-banner { color: red }
.title { font-weight: 700 }`, code, false),

		makeBatchDoc("theme/reset.css", "en", `* { margin: 0 }`, code, false),

		makeBatchDoc("theme/app.js", "en", `import { boot } from "./boot";
export function start() { boot() }`, code, false),

		makeBatchDoc("theme/boot.js", "en", `export function boot() {}`, code, false),

		// Nothing references this one.
		makeBatchDoc("theme/orphan.css", "en", `.nobody-uses-me { color: blue }`, code, false),
	}

	bp := NewBatchProcessor(srv, 4)
	resp, err := bp.ProcessBatch(context.Background(), "theme", docs)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if resp.Failed > 0 {
		cleanup()
		t.Fatalf("fixture did not store cleanly: %v", resp.Errors)
	}
	return srv, cleanup
}

func graphKeys(res *GraphResult) []string {
	out := make([]string, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		out = append(out, n.Key)
	}
	return out
}

func mustGraph(t *testing.T, srv *Server, req GraphRequest) *GraphResult {
	t.Helper()
	res, err := srv.CodeGraph(req)
	if err != nil {
		t.Fatalf("CodeGraph(%+v): %v", req, err)
	}
	return res
}

func containsKey(res *GraphResult, key string) bool {
	for _, n := range res.Nodes {
		if n.Key == key {
			return true
		}
	}
	return false
}

func edgeFor(res *GraphResult, from, to string) *GraphEdge {
	for i := range res.Edges {
		if res.Edges[i].From == from && res.Edges[i].To == to {
			return &res.Edges[i]
		}
	}
	return nil
}

// The question from issue #192: change this selector, what breaks?
func TestGraphFindsWhatDependsOnAStylesheet(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/style.css", Direction: GraphIn})

	if !containsKey(res, "theme/index.html") {
		t.Fatalf("the template applying its selectors is not in the graph: %v", graphKeys(res))
	}
	e := edgeFor(res, "theme/index.html", "theme/style.css")
	if e == nil {
		t.Fatal("no edge from the template to the stylesheet")
	}
	// Without the symbol the answer is only "these files are related".
	if e.Symbol != ".hero-banner" && e.Symbol != ".title" && e.Symbol != "theme/style.css" {
		t.Errorf("edge carries no usable reason: %+v", e)
	}
}

func TestGraphFindsWhatADocumentDependsOn(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/index.html", Direction: GraphOut})

	for _, want := range []string{"theme/style.css", "theme/app.js"} {
		if !containsKey(res, want) {
			t.Errorf("%s missing from what the page loads: %v", want, graphKeys(res))
		}
	}
}

// An ES specifier that omits the extension must still find the module.
func TestGraphResolvesExtensionlessImport(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/app.js", Direction: GraphOut})
	if !containsKey(res, "theme/boot.js") {
		t.Errorf(`import "./boot" did not resolve to boot.js: %v`, graphKeys(res))
	}

	// And the reverse direction must agree.
	back := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/boot.js", Direction: GraphIn})
	if !containsKey(back, "theme/app.js") {
		t.Errorf("the importer is invisible from the imported module: %v", graphKeys(back))
	}
}

// @import inside a stylesheet is an edge like any other.
func TestGraphFollowsCSSImport(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/style.css", Direction: GraphOut})
	if !containsKey(res, "theme/reset.css") {
		t.Errorf(`@import "reset.css" produced no edge: %v`, graphKeys(res))
	}
}

// "What does nothing reference any more" is the other half of the feature.
func TestGraphReportsAnOrphan(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/orphan.css", Direction: GraphIn})

	if len(res.Edges) != 0 {
		t.Errorf("an unreferenced stylesheet has edges: %+v", res.Edges)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Key != "theme/orphan.css" {
		t.Errorf("expected only the root node, got %v", graphKeys(res))
	}
	// A caller reading "nothing depends on this" must know the walk was
	// complete before acting on it.
	if res.Truncated {
		t.Error("an orphan lookup reported truncation, which makes the answer unusable")
	}
}

func TestGraphDepthReachesTransitiveDependency(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	shallow := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/index.html", Direction: GraphOut, Depth: 1})
	if containsKey(shallow, "theme/boot.js") {
		t.Errorf("depth 1 reached two hops away: %v", graphKeys(shallow))
	}

	deep := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/index.html", Direction: GraphOut, Depth: 2})
	if !containsKey(deep, "theme/boot.js") {
		t.Errorf("depth 2 did not reach the module app.js imports: %v", graphKeys(deep))
	}
	for _, n := range deep.Nodes {
		if n.Key == "theme/boot.js" && n.Depth != 2 {
			t.Errorf("boot.js recorded at depth %d, want 2", n.Depth)
		}
	}
}

// Breadth-first means "two hops away" is the shortest path, not whichever path
// arrived first.
func TestGraphRecordsShortestDepth(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/index.html", Direction: GraphBoth, Depth: 3})
	for _, n := range res.Nodes {
		switch n.Key {
		case "theme/index.html":
			if n.Depth != 0 {
				t.Errorf("the root is at depth %d", n.Depth)
			}
		case "theme/style.css", "theme/app.js":
			if n.Depth != 1 {
				t.Errorf("%s is one hop from the page but recorded at %d", n.Key, n.Depth)
			}
		}
	}
}

func TestGraphLimitsAreClamped(t *testing.T) {
	req := GraphRequest{Collection: "theme", Key: "x", Depth: 99, MaxDegree: 9999}
	if err := req.Normalise(); err != nil {
		t.Fatal(err)
	}
	if req.Depth != GraphMaxDepth {
		t.Errorf("depth = %d, want it clamped to %d", req.Depth, GraphMaxDepth)
	}
	if req.MaxDegree != GraphMaxDegree {
		t.Errorf("maxDegree = %d, want it clamped to %d", req.MaxDegree, GraphMaxDegree)
	}
	if req.Direction != GraphBoth {
		t.Errorf("direction defaulted to %q, want both", req.Direction)
	}
}

func TestGraphDegreeLimitReportsTruncation(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{
		Collection: "theme", Key: "theme/style.css", Direction: GraphIn, MaxDegree: 1,
	})
	if !res.Truncated {
		t.Error("a degree limit cut the walk but the result claims to be complete")
	}
}

func TestGraphRejectsBadInput(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	cases := map[string]GraphRequest{
		"no collection": {Key: "theme/style.css"},
		"no key":        {Collection: "theme"},
	}
	for name, req := range cases {
		if _, err := srv.CodeGraph(req); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	_, err := srv.CodeGraph(GraphRequest{Collection: "theme", Key: "theme/nope.css"})
	if !errors.Is(err, errGraphDocNotFound) {
		// A missing document is the caller's typo, and anything retrying on
		// 5xx needs that distinction.
		t.Errorf("a missing document gave %v, want errGraphDocNotFound", err)
	}
}

func TestParseGraphDirection(t *testing.T) {
	for in, want := range map[string]GraphDirection{
		"":     GraphBoth,
		"in":   GraphIn,
		"out":  GraphOut,
		"both": GraphBoth,
	} {
		got, err := ParseGraphDirection(in)
		if err != nil || got != want {
			t.Errorf("ParseGraphDirection(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseGraphDirection("sideways"); err == nil {
		t.Error("an unknown direction was accepted")
	}
}

// Two identical requests must produce identical bytes: a caller diffing graphs
// to see what a change did should see only what changed.
func TestGraphIsDeterministic(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	req := GraphRequest{Collection: "theme", Key: "theme/index.html", Direction: GraphBoth, Depth: 2}
	first := mustGraph(t, srv, req)
	firstKeys := strings.Join(graphKeys(first), ",")

	for range 10 {
		again := mustGraph(t, srv, req)
		if strings.Join(graphKeys(again), ",") != firstKeys {
			t.Fatalf("node order varies between runs:\n %s\n %s", firstKeys, strings.Join(graphKeys(again), ","))
		}
		if len(again.Edges) != len(first.Edges) {
			t.Fatalf("edge count varies: %d then %d", len(first.Edges), len(again.Edges))
		}
		for i := range again.Edges {
			if again.Edges[i] != first.Edges[i] {
				t.Fatalf("edge %d differs: %+v vs %+v", i, first.Edges[i], again.Edges[i])
			}
		}
	}
}

// A selector shared with an unrelated collection is a coincidence, not a
// dependency.
func TestGraphDoesNotCrossCollections(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	other := []*proto.BatchDocument{
		makeBatchDoc("other/page.html", "en", `<div class="hero-banner"></div>`,
			map[string]*proto.MetaValues{"kind": {Values: []string{"code"}}}, false),
	}
	bp := NewBatchProcessor(srv, 2)
	if _, err := bp.ProcessBatch(context.Background(), "elsewhere", other); err != nil {
		t.Fatal(err)
	}

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/style.css", Direction: GraphIn})
	if containsKey(res, "other/page.html") {
		t.Errorf("a document from another collection leaked in: %v", graphKeys(res))
	}
}

// The graph is derived, never stored — so rewriting the documents unchanged
// must reproduce it exactly.
func TestGraphSurvivesReindexUnchanged(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	req := GraphRequest{Collection: "theme", Key: "theme/index.html", Direction: GraphBoth, Depth: 2}
	before := mustGraph(t, srv, req)

	// Same content, written again.
	code := map[string]*proto.MetaValues{"kind": {Values: []string{"code"}}}
	bp := NewBatchProcessor(srv, 4)
	if _, err := bp.ProcessBatch(context.Background(), "theme", []*proto.BatchDocument{
		makeBatchDoc("theme/index.html", "en", `<!doctype html>
<section id="hero" class="hero-banner">
  <h2 class="title">Hi</h2>
</section>
<link rel="stylesheet" href="style.css">
<script src="app.js"></script>`, code, false),
	}); err != nil {
		t.Fatal(err)
	}

	after := mustGraph(t, srv, req)
	if strings.Join(graphKeys(before), ",") != strings.Join(graphKeys(after), ",") {
		t.Errorf("rewriting a document changed the graph:\n before %v\n after  %v",
			graphKeys(before), graphKeys(after))
	}
}

func TestImportFormsCoversExtensionlessSpecifiers(t *testing.T) {
	forms := importForms("assets/render.js")
	if len(forms) != 2 || forms[0] != "assets/render.js" || forms[1] != "assets/render" {
		t.Errorf("importForms = %v, want both the full key and the extension-less form", forms)
	}
	// A file whose extension is not a module extension has only one form.
	if got := importForms("images/hero.png"); len(got) != 1 {
		t.Errorf("importForms(png) = %v, want just the key", got)
	}
}

func TestResolveImportPaths(t *testing.T) {
	cases := []struct {
		from, language, spec, want string
	}{
		{"assets/app.js", "javascript", "./render.js", "assets/render.js"},
		{"assets/app.js", "javascript", "../lib/helpers", "lib/helpers"},
		{"assets/app.js", "javascript", "/global/reset.css", "global/reset.css"},
		{"app.js", "javascript", "./boot.js", "boot.js"},
		// A bare JS specifier names a package, not a sibling file — rewriting
		// it to theme/lodash would invent an edge to a document that will
		// never exist.
		{"theme/app.js", "javascript", "lodash", "lodash"},
		// In HTML and CSS a bare path *is* relative to the document, which is
		// what makes <link href> and @import produce edges at all.
		{"theme/index.html", "html", "style.css", "theme/style.css"},
		{"theme/style.css", "css", "reset.css", "theme/reset.css"},
	}
	for _, c := range cases {
		got := resolveImportPaths(c.from, c.language, []string{c.spec})
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("from %s (%s), %q resolved to %v, want %s", c.from, c.language, c.spec, got, c.want)
		}
	}
	if got := resolveImportPaths("a.js", "javascript", nil); got != nil {
		t.Errorf("no specifiers should give nil, got %v", got)
	}
}

// The remaining paths through the resolver: missing buckets, a dangling index
// entry, self-references, and the degree cut inside a single symbol lookup.

func TestGraphOnAServerWithNoBuckets(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	// A collection that was never written to has no documents to walk.
	if _, err := srv.CodeGraph(GraphRequest{Collection: "empty", Key: "a.css"}); !errors.Is(err, errGraphDocNotFound) {
		t.Errorf("got %v, want errGraphDocNotFound", err)
	}
}

// An index entry outliving its document must be skipped, not crash the walk or
// appear as a phantom neighbour.
func TestGraphSkipsDanglingIndexEntries(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	// Delete the document body but leave the metadata index pointing at it.
	ref, err := srv.loadCodeDocByKey("theme", "theme/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.DBUpdate(func(tx *bolt.Tx) error {
		return tx.Bucket(srv.BucketNames.Docs).Delete(storage.DocKey("theme", ref.DocID))
	}); err != nil {
		t.Fatal(err)
	}

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/style.css", Direction: GraphIn})
	if containsKey(res, "theme/index.html") {
		t.Errorf("a deleted document still appears in the graph: %v", graphKeys(res))
	}
}

// A stylesheet that both declares and applies the same selector must not become
// its own neighbour.
func TestGraphIgnoresSelfReferences(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	code := map[string]*proto.MetaValues{"kind": {Values: []string{"code"}}}
	bp := NewBatchProcessor(srv, 2)
	if _, err := bp.ProcessBatch(context.Background(), "solo", []*proto.BatchDocument{
		makeBatchDoc("solo/page.html", "en",
			`<div id="hero" class="hero"></div><link href="page.html">`, code, false),
	}); err != nil {
		t.Fatal(err)
	}

	res := mustGraph(t, srv, GraphRequest{Collection: "solo", Key: "solo/page.html", Direction: GraphBoth})
	for _, e := range res.Edges {
		if e.From == e.To {
			t.Errorf("a document is its own neighbour: %+v", e)
		}
	}
}

// The degree limit must cut inside one symbol's fan-out, not only across
// symbols — a selector such as .title appears in every template.
func TestGraphDegreeLimitCutsWithinOneSymbol(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	code := map[string]*proto.MetaValues{"kind": {Values: []string{"code"}}}
	docs := []*proto.BatchDocument{
		makeBatchDoc("wide/style.css", "en", ".title { color: red }", code, false),
	}
	for i := range 8 {
		docs = append(docs, makeBatchDoc(
			"wide/page"+string(rune('a'+i))+".html", "en",
			`<h2 class="title">x</h2>`, code, false))
	}
	bp := NewBatchProcessor(srv, 4)
	if _, err := bp.ProcessBatch(context.Background(), "wide", docs); err != nil {
		t.Fatal(err)
	}

	res := mustGraph(t, srv, GraphRequest{
		Collection: "wide", Key: "wide/style.css", Direction: GraphIn, MaxDegree: 3,
	})
	if len(res.Edges) > 3 {
		t.Errorf("the degree limit let %d edges through, want at most 3", len(res.Edges))
	}
	if !res.Truncated {
		t.Error("the walk was cut but the result claims to be complete")
	}
}

// An import naming a document that does not exist yields no edge rather than a
// node with an empty key.
func TestGraphIgnoresUnresolvableImports(t *testing.T) {
	srv, cleanup := newTestServerForBatch(t)
	defer cleanup()

	code := map[string]*proto.MetaValues{"kind": {Values: []string{"code"}}}
	bp := NewBatchProcessor(srv, 2)
	if _, err := bp.ProcessBatch(context.Background(), "dangling", []*proto.BatchDocument{
		makeBatchDoc("dangling/app.js", "en",
			`import "./nowhere"; import "lodash"; import "./gone.js";`, code, false),
	}); err != nil {
		t.Fatal(err)
	}

	res := mustGraph(t, srv, GraphRequest{Collection: "dangling", Key: "dangling/app.js", Direction: GraphOut})
	if len(res.Edges) != 0 {
		t.Errorf("imports pointing nowhere produced edges: %+v", res.Edges)
	}
}

func TestEnrichCodeSymbolsIgnoresDocumentsWithoutMeta(t *testing.T) {
	// A prose document with no meta map at all must not gain one.
	doc := &storage.Doc{Key: "notes.md", ContentMD: "# Prose"}
	EnrichCodeSymbols(doc)
	if len(doc.Meta) != 0 {
		t.Errorf("a prose document gained meta: %v", doc.Meta)
	}
}

func TestChunkModeForFollowsTheDocument(t *testing.T) {
	code := &storage.Doc{Key: "a.css", Meta: map[string][]string{"kind": {"code"}}}
	if got := ChunkModeFor(code); got != ChunkModeCode {
		t.Errorf("a code document chunks as %q", got)
	}

	prose := &storage.Doc{Key: "notes.md", Meta: map[string][]string{"kind": {"prose"}}}
	if got := ChunkModeFor(prose); got != ChunkModeProse {
		t.Errorf("a prose document chunks as %q", got)
	}

	// The server-wide override exists for collections ingested before the
	// convention, and must win over what the document says.
	t.Setenv("MDDB_EMBEDDING_CHUNK_MODE", "code")
	if got := ChunkModeFor(prose); got != ChunkModeCode {
		t.Errorf("the server override was ignored: %q", got)
	}
	t.Setenv("MDDB_EMBEDDING_CHUNK_MODE", "prose")
	if got := ChunkModeFor(code); got != ChunkModeProse {
		t.Errorf("the server override was ignored: %q", got)
	}
}

// CODE-005 step 4: the line each edge's symbol sits on, reusing the line index
// from CODE-002. Opt-in, because it is the only part of a traversal that reads
// document content.

func TestGraphLinesAreOmittedByDefault(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{Collection: "theme", Key: "theme/style.css", Direction: GraphIn})
	for _, e := range res.Edges {
		if e.Lines != nil {
			t.Errorf("a plain traversal read document content: %+v", e)
		}
	}
}

func TestGraphLinesPointAtTheSymbol(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{
		Collection: "theme", Key: "theme/style.css", Direction: GraphIn, IncludeLines: true,
	})
	if len(res.Edges) == 0 {
		t.Fatal("no edges to annotate")
	}

	var checked bool
	for _, e := range res.Edges {
		if e.Lines == nil {
			t.Fatalf("lines were requested but not filled: %+v", e)
		}
		if e.Symbol != ".hero-banner" {
			continue
		}
		checked = true
		// index.html line 2 carries class="hero-banner"; style.css line 2
		// declares it (line 1 is the @import).
		if e.Lines.FromLine != 2 {
			t.Errorf("the template's use of %s is on line %d, want 2", e.Symbol, e.Lines.FromLine)
		}
		if e.Lines.ToLine != 2 {
			t.Errorf("the stylesheet's declaration of %s is on line %d, want 2", e.Symbol, e.Lines.ToLine)
		}
	}
	if !checked {
		t.Error("the .hero-banner edge was not present to check")
	}
}

// A wrong line number sends someone to the wrong place, which is worse than
// sending them nowhere.
func TestFirstSymbolLine(t *testing.T) {
	const css = "@import \"reset.css\";\n.hero-banner { color: red }\n.title {}"
	const html = "<!doctype html>\n<div class=\"hero-banner\">\n</div>"

	cases := []struct {
		name, content, symbol string
		want                  int
	}{
		{"selector in its stylesheet", css, ".hero-banner", 2},
		{"selector as a class attribute", html, ".hero-banner", 2},
		{"resolved import key by file name", css, "theme/reset.css", 1},
		{"absent symbol", css, ".nowhere", 0},
		{"empty content", "", ".a", 0},
		{"empty symbol", css, "", 0},
	}
	for _, c := range cases {
		if got := firstSymbolLine(c.content, c.symbol); got != c.want {
			t.Errorf("%s: line %d, want %d", c.name, got, c.want)
		}
	}
}

func TestSymbolNeedles(t *testing.T) {
	got := symbolNeedles(".hero-banner")
	if len(got) != 2 || got[0] != ".hero-banner" || got[1] != "hero-banner" {
		t.Errorf("selector needles = %v, want the dotted and bare forms", got)
	}
	got = symbolNeedles("theme/assets/app.js")
	if len(got) != 2 || got[1] != "app.js" {
		t.Errorf("path needles = %v, want the key and the file name", got)
	}
	if got := symbolNeedles("start"); len(got) != 1 {
		t.Errorf("a plain identifier needs one form, got %v", got)
	}
}

func TestAnnotateLinesWithoutEdges(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res := mustGraph(t, srv, GraphRequest{
		Collection: "theme", Key: "theme/orphan.css", Direction: GraphIn, IncludeLines: true,
	})
	if len(res.Edges) != 0 {
		t.Errorf("the orphan gained edges: %+v", res.Edges)
	}
}
