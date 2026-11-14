package main

import "testing"

// CODE-013 found three ways a semantic collection answered worse than the
// facts allow: a definition the scanner does not list in `defines` (an
// interface method, a method past the scanner's cap in a large file) was not
// found even with its path; the heuristic answer that took over counted that
// definition's own line as a reference; and a namespaced PHP class was not
// found by the name an agent types.

// TestPassageAround_KeepsTheSymbolOfItsStart: a window that starts inside a
// function and centres on a line past its end is labelled with the function,
// as in 2.19.2. CODE-012 overwrote the label with the hit line's (none), so a
// collection without facts changed its search answers (CODE-013 regression).
func TestPassageAround_KeepsTheSymbolOfItsStart(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	content := "package x\n\nfunc Serve() {\n\tlisten()\n\tloop()\n}\n\nvar a = 1\nvar b = 2\n"
	doc := addTestDoc(t, s, "notes", "serve.go", "en", content, nil)
	p := passageAround("notes", &doc, 8, 3, 0, "fts") // lines 5-9: Serve's end, then top level
	if p.Lines.Start != 5 || p.Symbol != "Serve" {
		t.Fatalf("passage: %+v", p)
	}
}

// TestLookupSymbolSemantic_PHPClassByItsName: Kernel finds App\Http\Kernel.
func TestLookupSymbolSemantic_PHPClassByItsName(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		content := "<?php\nnamespace App\\Http;\n\nclass Kernel\n{\n    public function handle() {}\n}\n"
		addSemDoc(t, s, "app/Http/Kernel.php", semFile{content: content,
			defs: []string{`App\Http\Kernel class 4-7`, `App\Http\Kernel.handle method 6-6 App\Http\Kernel`}})
		addSemDoc(t, s, "app/boot.php", semFile{content: "<?php\n$k = new \\App\\Http\\Kernel();\n",
			refs: []string{`app/Http/Kernel.php#App\Http\Kernel 2`}})
		res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Kernel"})
		if got := targets(res.References); res.Precision != PrecisionSemantic || got != `app/boot.php:2@>app/Http/Kernel.php#App\Http\Kernel` {
			t.Fatalf("precision %s, references %s", res.Precision, got)
		}
		if !matchesSymbol(`App\Http\Kernel.handle`, `Kernel.handle`) || matchesSymbol(`App\Http\MyKernel`, "Kernel") {
			t.Fatal("matchesSymbol")
		}
	})
}

// TestLookupSymbolSemantic_PathFindsWhatTheScannerMisses: Closer.Close is an
// interface method, which the scanner does not list as a definition; with
// its path the facts answer.
func TestLookupSymbolSemantic_PathFindsWhatTheScannerMisses(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		seedSemRepo(t, s)
		res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close", Path: "io/closer.go"})
		if got := passageKeys(res.Definitions); got != "io/closer.go:4-4@Closer.Close" || res.Precision != PrecisionSemantic {
			t.Fatalf("definitions %s, precision %s", got, res.Precision)
		}
		if res.Ambiguous || len(res.References) != 0 {
			t.Fatalf("ambiguous %v, references %s", res.Ambiguous, targets(res.References))
		}
	})
}

// TestLookupSymbolSemantic_FallbackSkipsTheDefinitionItself: without a path
// the interface method is not found in the facts, and the heuristic answers.
// Its declaration line lies in the facts' Closer.Close, which is the name
// asked — the definition itself, not a use of it.
func TestLookupSymbolSemantic_FallbackSkipsTheDefinitionItself(t *testing.T) {
	forBothLayouts(t, func(t *testing.T, s *Server) {
		addSemDoc(t, s, "io/closer.go", semRepo["io/closer.go"])
		res := lookup(t, s, SymbolRequest{Collection: semColl, Name: "Close"})
		if len(res.References) != 0 {
			t.Fatalf("references: %s", targets(res.References))
		}
	})
}
