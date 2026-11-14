package main

import (
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"

	json "mddb/internal/jsonx"
)

// CODE-012: code_symbol answering from semantic facts.

const semColl = "sem"

// semFile is a Go file and the facts a language server gives for it.
type semFile struct {
	content           string
	defs, refs, impls []string
}

// semRepo: an interface io.Closer with three implementations, two Close
// methods called from one function and a test, and a callee.
var semRepo = map[string]semFile{
	"io/closer.go": {
		content: "package io\n\ntype Closer interface {\n\tClose() error\n}\n",
		defs:    []string{"Closer interface 3-5", "Closer.Close method 4-4 Closer"},
	},
	"net/conn.go": {
		content: "package net\n\ntype Conn struct{}\n\nfunc (c *Conn) Close() error {\n\treturn nil\n}\n",
		defs:    []string{"Conn struct 3-3", "Conn.Close method 5-7 Conn"},
		refs:    []string{"net/conn.go#Conn 5"},
		impls:   []string{"io/closer.go#Closer Conn"},
	},
	"file/file.go": {
		content: "package file\n\ntype File struct{}\n\nfunc (f *File) Close() error {\n\tflush()\n\treturn nil\n}\n\nfunc flush() {}\n",
		defs:    []string{"File struct 3-3", "File.Close method 5-8 File", "flush function 10-10"},
		refs:    []string{"file/file.go#flush 6"},
		impls:   []string{"io/closer.go#Closer File"},
	},
	"buf/buf.go": {
		content: "package buf\n\ntype Buffer struct{}\n\nfunc (b *Buffer) Close() error { return nil }\n",
		defs:    []string{"Buffer struct 3-3", "Buffer.Close method 5-5 Buffer"},
		impls:   []string{"io/closer.go#Closer Buffer"},
	},
	"app/main.go": {
		content: "package app\n\nfunc run(c *net.Conn, f *file.File) {\n\tc.Close()\n\tf.Close()\n\tc.Close()\n}\n",
		defs:    []string{"run function 3-7"},
		refs:    []string{"file/file.go#File 3", "file/file.go#File.Close 5", "net/conn.go#Conn 3", "net/conn.go#Conn.Close 4,6"},
	},
	"app/main_test.go": {
		content: "package app\n\nfunc TestRun(t *testing.T) {\n\tvar c net.Conn\n\tc.Close()\n}\n",
		defs:    []string{"TestRun function 3-6"},
		refs:    []string{"net/conn.go#Conn 4", "net/conn.go#Conn.Close 5"},
	},
}

// addSemDoc stores one file with facts valid for its content.
func addSemDoc(t *testing.T, s *Server, path string, f semFile) {
	t.Helper()
	meta := map[string][]string{"path": {path},
		MetaKeySymIndex: {"v1 " + semanticContentHash(f.content) + " 000000000000 gopls/0.24.0"}}
	for key, vals := range map[string][]string{MetaKeySymDefs: f.defs, MetaKeySymRefs: f.refs, MetaKeySymImpls: f.impls} {
		if len(vals) > 0 {
			meta[key] = vals
		}
	}
	if _, _, err := s.addDocumentIf(semColl, path, "en", meta, f.content, 0, false, ""); err != nil {
		t.Fatal(err)
	}
}

func seedSemRepo(t *testing.T, s *Server) {
	t.Helper()
	for path, f := range semRepo {
		addSemDoc(t, s, path, f)
	}
}

// forBothLayouts runs fn against a server in each storage layout.
func forBothLayouts(t *testing.T, fn func(t *testing.T, s *Server)) {
	for _, layout := range []string{"", "per-collection"} {
		t.Run("layout="+layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			s, cleanup := newHandlerTestServer(t)
			t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
			fn(t, s)
		})
	}
}

func targets(ps []Passage) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Path+":"+itoa(p.Lines.Start)+"@"+p.Symbol+">"+p.Target)
	}
	return strings.Join(out, " ")
}

func TestLookupSymbolSemantic_PathPicksOneDefinition(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		seedSemRepo(t, s)
		res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Path: "net/conn.go", IncludeContent: true})
		if got := passageKeys(res.Definitions); got != "net/conn.go:5-7@Conn.Close" {
			t.Fatalf("definitions: %s", got)
		}
		if d := res.Definitions[0]; d.Kind != "method" || d.Container != "Conn" || !strings.Contains(d.Content, "return nil") {
			t.Fatalf("definition: %+v", d)
		}
		// Only Conn.Close's uses: f.Close() on line 5 of main.go is File.Close.
		want := "app/main.go:4@run>net/conn.go#Conn.Close app/main.go:6@run>net/conn.go#Conn.Close app/main_test.go:5@TestRun>net/conn.go#Conn.Close"
		if got := targets(res.References); got != want {
			t.Fatalf("references: %s", got)
		}
		if got := passageKeys(res.Callers); got != "app/main.go:3-7@run app/main_test.go:3-6@TestRun" {
			t.Fatalf("callers: %s", got)
		}
		if got := passageKeys(res.Tests); got != "app/main_test.go:5-5@TestRun" || res.Tests[0].Source != RelTests {
			t.Fatalf("tests: %s", got)
		}
		if res.Precision != PrecisionSemantic || res.SemanticSource != "gopls/0.24.0" || res.Ambiguous || res.Candidates != nil {
			t.Fatalf("precision %q source %q ambiguous %v %v", res.Precision, res.SemanticSource, res.Ambiguous, res.Candidates)
		}
		if res.Implementations != nil || res.Callees != nil {
			t.Fatalf("Conn.Close implements nothing here and calls nothing: %v %v", res.Implementations, res.Callees)
		}
	})
}

func TestLookupSymbolSemantic_BareNameIsAmbiguous(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSemRepo(t, s)
	res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close"})
	if !res.Ambiguous || len(res.Candidates) != 3 {
		t.Fatalf("ambiguous %v candidates %+v", res.Ambiguous, res.Candidates)
	}
	if c := res.Candidates[1]; c != (SymbolCandidate{Path: "file/file.go", Name: "File.Close", Kind: "method"}) {
		t.Fatalf("candidate: %+v", c)
	}
	// References stay apart, grouped per definition: File.Close's first.
	want := "app/main.go:5@run>file/file.go#File.Close app/main.go:4@run>net/conn.go#Conn.Close " +
		"app/main.go:6@run>net/conn.go#Conn.Close app/main_test.go:5@TestRun>net/conn.go#Conn.Close"
	if got := targets(res.References); got != want {
		t.Fatalf("references: %s", got)
	}
	if got := targets(res.Callees); got != "file/file.go:10@flush>file/file.go#File.Close" {
		t.Fatalf("callees: %s", got)
	}
	// Siblings are the scanner's, from every defining directory.
	if !strings.Contains(passageKeys(res.Siblings), "@Buffer") {
		t.Fatalf("siblings: %s", passageKeys(res.Siblings))
	}
}

func TestLookupSymbolSemantic_QualifiedNameAndImplementations(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSemRepo(t, s)
	res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "File.Close"})
	if got := passageKeys(res.Definitions); got != "file/file.go:5-8@File.Close" || res.Ambiguous {
		t.Fatalf("qualified: %s", got)
	}
	impl := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Closer", Include: []string{RelImplementations}})
	want := "buf/buf.go:3@Buffer>io/closer.go#Closer file/file.go:3@File>io/closer.go#Closer net/conn.go:3@Conn>io/closer.go#Closer"
	if got := targets(impl.Implementations); got != want {
		t.Fatalf("implementations: %s", got)
	}
	if impl.Implementations[0].Source != RelImplementations || impl.Implementations[0].Kind != "struct" {
		t.Fatalf("implementation passage: %+v", impl.Implementations[0])
	}
	if impl.Precision != PrecisionSemantic || len(impl.Definitions) != 0 {
		t.Fatalf("precision %q, definitions %v", impl.Precision, impl.Definitions)
	}
	// A path naming no definition: nothing, and the answer says heuristic.
	none := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Path: "nowhere.go"})
	if len(none.Definitions) != 0 || none.Precision != PrecisionHeuristic {
		t.Fatalf("no definition at path: %+v", none)
	}
}

func TestLookupSymbolSemantic_MixedCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSemRepo(t, s)
	addTestDoc(t, s, semColl, "legacy/old.go", "en", "package legacy\n\ntype Old struct{}\n\nfunc (o *Old) Close() error {\n\treturn nil\n}\n", nil)
	addTestDoc(t, s, semColl, "legacy/use.go", "en", "package legacy\n\nfunc stop(o *Old) {\n\to.Close()\n}\n", nil)
	res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close"})
	if res.Precision != PrecisionMixed {
		t.Fatalf("precision: %q", res.Precision)
	}
	if !strings.Contains(passageKeys(res.Definitions), "legacy/old.go:5-7@Close") || len(res.Candidates) != 3 {
		t.Fatalf("definitions: %s, candidates %v", passageKeys(res.Definitions), res.Candidates)
	}
	// The file without facts is found by text; the files with facts are not
	// found again by text — their uses are the facts' or none.
	refs := targets(res.References)
	if !strings.Contains(refs, "legacy/use.go:4@stop>") || strings.Count(refs, "app/main.go:4") != 1 {
		t.Fatalf("references: %s", refs)
	}
	if !strings.Contains(passageKeys(res.Callers), "legacy/use.go:3-5@stop") {
		t.Fatalf("callers: %s", passageKeys(res.Callers))
	}
}

func TestLookupSymbolSemantic_EditedContentFallsBack(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSemRepo(t, s)
	// The file changes and nobody recomputes its facts: the write drops them.
	edited := semRepo["net/conn.go"].content + "\n// edited\n"
	meta := map[string][]string{"path": {"net/conn.go"}, MetaKeySymIndex: {"v1 " + semanticContentHash(semRepo["net/conn.go"].content) + " 000000000000 gopls/0.24.0"}}
	if _, _, err := s.addDocumentIf(semColl, "net/conn.go", "en", meta, edited, 0, false, ""); err != nil {
		t.Fatal(err)
	}
	res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Path: "net/conn.go"})
	if res.Precision != PrecisionHeuristic || passageKeys(res.Definitions) != "net/conn.go:5-7@Close" {
		t.Fatalf("fallback: %q %s", res.Precision, passageKeys(res.Definitions))
	}
}

func TestLookupSymbol_NoFactsKeepsTheOldShape(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSymbolRepo(t, s)
	res := lookup(t, s, SymbolRequest{Collection: "repo", Name: "SearchHybrid", IncludeContent: true})
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"precision", "semanticSource", "ambiguous", "candidates", "implementations", "container", "target"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Errorf("a collection without facts answers with %q: %s", field, raw)
		}
	}
	if got := lookup(t, s, SymbolRequest{Collection: "repo", Name: "SearchHybrid", Include: []string{RelImplementations}}); got.Precision != PrecisionHeuristic || got.Implementations != nil {
		t.Errorf("implementations without facts: %+v", got)
	}
}

// TestLookupSymbolSemantic_Edges covers what real facts sometimes say: a
// declaration among its own uses, a callee in a file without facts or not
// in its file's facts, a use outside the span, uses split over two values,
// and an implementation whose type fell to the fact limit.
func TestLookupSymbolSemantic_Edges(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSemRepo(t, s)
	addSemDoc(t, s, "two/two.go", semFile{
		content: "package two\n\ntype A struct{}\n\nfunc (a A) Close() { helper(); n.Do() }\n\ntype B struct{}\n\nfunc (b B) Close() {}\n\nfunc helper() {}\n\nvar Phantom = 1\n",
		defs:    []string{"A struct 3-3", "A.Close method 5-5 A", "B struct 7-7", "B.Close method 9-9 B", "helper function 11-11"},
		refs: []string{"io/closer.go#Closer 5", "nofacts/n.go#Do 5", "two/two.go#A.Close 5", "two/two.go#B 9", "two/two.go#helper 5",
			"two/two.go#missing 5"},
		impls: []string{"io/closer.go#Closer Ghost", "io/closer.go#Closer Phantom"},
	})
	addSemDoc(t, s, "two/use.go", semFile{
		content: "package two\n\nfunc use(a A) {\n\ta.Close()\n\ta.Close()\n}\n",
		defs:    []string{"use function 3-6"},
		refs:    []string{"two/two.go#A.Close 4", "two/two.go#A.Close 5"},
	})
	addTestDoc(t, s, semColl, "nofacts/n.go", "en", "package n\n\nfunc Do() {}\n", map[string][]string{"path": {"nofacts/n.go"}})

	res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "A.Close"})
	if got := targets(res.References); got != "two/use.go:4@use>two/two.go#A.Close two/use.go:5@use>two/two.go#A.Close" {
		t.Fatalf("references (declaration left out, split values merged): %s", got)
	}
	if got := targets(res.Callees); got != "io/closer.go:3@Closer>two/two.go#A.Close two/two.go:11@helper>two/two.go#A.Close" {
		t.Fatalf("callees: %s", got)
	}
	impl := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Closer", Path: "io/closer.go", Include: []string{RelImplementations}})
	if got := targets(impl.Implementations); !strings.Contains(got, "two/two.go:13@Phantom>") || strings.Contains(got, "Ghost") {
		t.Fatalf("implementations: %s", got)
	}
	// Five definitions of Close, two in one file ordered by line; the
	// candidate list is cut at the limit like every relation.
	all := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Include: []string{RelDefinitions}})
	if got := passageKeys(all.Definitions); !strings.Contains(got, "two/two.go:5-5@A.Close two/two.go:9-9@B.Close") {
		t.Fatalf("definitions: %s", got)
	}
	cut := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Include: []string{RelDefinitions}, Limit: 2})
	if len(cut.Candidates) != 2 || !cut.Truncated {
		t.Fatalf("candidates at the limit: %+v", cut)
	}

	// Without a metadata index there is nothing to find, and no error.
	if err := s.DBUpdateC(semColl, func(tx *bolt.Tx) error { return tx.DeleteBucket(s.BucketNames.IdxMeta) }); err != nil {
		t.Fatal(err)
	}
	if none := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close"}); len(none.Definitions) != 0 || none.Precision != "" {
		t.Fatalf("no index: %+v", none)
	}
}
