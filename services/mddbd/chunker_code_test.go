package main

import (
	"fmt"
	"strings"
	"testing"
)

// CODE-003 / issue #192. The prose chunker splits on sentence boundaries, and
// a period inside url(a.png) is not one — a split there leaves half a
// declaration in each chunk: a passage that reads as nothing and embeds as
// noise. Code chunks must close where a construct closes.

// bracketDepth counts unclosed brackets in a fragment, ignoring those inside
// strings and comments. Written independently of the scanner it checks, so a
// bug in one does not hide in the other.
func bracketDepth(s string) int {
	depth := 0
	var inStr bool
	var quote byte
	var inLine, inBlock bool

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inLine {
			if c == '\n' {
				inLine = false
			}
			continue
		}
		if inBlock {
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		}
		if inStr {
			switch c {
			case '\\':
				i++
			case quote:
				inStr = false
			}
			continue
		}
		if c == '/' && i+1 < len(s) {
			switch s[i+1] {
			case '/':
				inLine = true
				i++
				continue
			case '*':
				inBlock = true
				i++
				continue
			}
		}
		switch c {
		case '"', '\'', '`':
			inStr, quote = true, c
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			depth--
		}
	}
	return depth
}

func cssCorpus(rules int) string {
	var b strings.Builder
	for i := range rules {
		fmt.Fprintf(&b, ".rule-%02d {\n  color: #cccccc;\n  background: url(img-%02d.png);\n  margin: 0;\n}\n\n", i, i)
	}
	return b.String()
}

func jsCorpus(funcs int) string {
	var b strings.Builder
	for i := range funcs {
		fmt.Fprintf(&b, `function handler%02d(req, res) {
  const items = [1, 2, 3].map((n) => {
    return { id: n, label: "item " + n };
  });
  if (items.length) {
    res.send({ ok: true, items });
  }
}

`, i)
	}
	return b.String()
}

func htmlCorpus(blocks int) string {
	var b strings.Builder
	for i := range blocks {
		fmt.Fprintf(&b, "<section class=\"card-%02d\">\n  <h2>Title %02d</h2>\n  <p>Body text for card %02d.</p>\n</section>\n\n", i, i, i)
	}
	return b.String()
}

func codeCorpora() map[string]string {
	return map[string]string{
		"css":               cssCorpus(30),
		"javascript":        jsCorpus(12),
		"html":              htmlCorpus(25),
		"minified":          strings.Repeat(".a{color:red}.b{color:blue}", 200), // one enormous line
		"braces in string":  strings.Repeat("const s = \"}{ not real }\";\nconst t = 1;\n\n", 60),
		"braces in comment": strings.Repeat("// } stray brace\n/* { another } */\nconst v = 2;\n\n", 60),
		"no blank lines":    strings.Repeat("const a = { x: 1 };\n", 200),
	}
}

// The acceptance criterion: a chunk must never end mid-construct.
func TestCodeChunksNeverEndInsideBrackets(t *testing.T) {
	for name, src := range codeCorpora() {
		for _, size := range []int{150, 400, 1000} {
			for i, span := range ChunkSpansCode(src, size) {
				if d := bracketDepth(span.Text); d != 0 {
					t.Errorf("%s (max=%d): chunk %d closes at bracket depth %d\n%q",
						name, size, i, d, trunc(span.Text))
				}
			}
		}
	}
}

// One chunk should be roughly one editable unit — a whole rule, not a fragment
// of one.
func TestCSSChunksHoldWholeRules(t *testing.T) {
	spans := ChunkSpansCode(cssCorpus(30), 400)
	if len(spans) < 3 {
		t.Fatalf("expected the stylesheet to split, got %d chunks", len(spans))
	}
	for i, s := range spans {
		opens := strings.Count(s.Text, "{")
		closes := strings.Count(s.Text, "}")
		if opens != closes {
			t.Errorf("chunk %d has %d opening and %d closing braces:\n%q", i, opens, closes, trunc(s.Text))
		}
		if opens == 0 {
			t.Errorf("chunk %d contains no complete rule:\n%q", i, trunc(s.Text))
		}
	}
}

// Nested function bodies must survive intact.
func TestJavaScriptChunksKeepFunctionBodiesWhole(t *testing.T) {
	for i, s := range ChunkSpansCode(jsCorpus(12), 500) {
		if strings.Count(s.Text, "function") == 0 {
			continue
		}
		if bracketDepth(s.Text) != 0 {
			t.Errorf("chunk %d cut a function body:\n%q", i, trunc(s.Text))
		}
		// A chunk that starts a function must also end it.
		if strings.Contains(s.Text, "function handler") && !strings.Contains(s.Text, "}") {
			t.Errorf("chunk %d starts a function it never closes:\n%q", i, trunc(s.Text))
		}
	}
}

// A minified file is one line, so there is nowhere safe to cut. Hard-splitting
// is the last resort — but it must still respect the budget rather than
// returning one huge chunk.
func TestMinifiedFileIsStillChunked(t *testing.T) {
	src := strings.Repeat(".a{color:red}.b{color:blue}", 200)
	spans := ChunkSpansCode(src, 400)
	if len(spans) < 2 {
		t.Fatalf("a minified file should still be split, got %d chunks", len(spans))
	}
	for i, s := range spans {
		if len(s.Text) > 400 {
			t.Errorf("chunk %d is %d bytes, over the 400 budget", i, len(s.Text))
		}
	}
}

// Braces inside strings and comments must not move the depth.
func TestScannerIgnoresBracesInStringsAndComments(t *testing.T) {
	src := `const a = "}{";

/* } */
const b = 1;

// {
const c = 2;
`
	lines := scanCodeLines(src)
	for i, ln := range lines {
		if ln.depthAfter != 0 {
			t.Errorf("line %d reports depth %d; braces in strings and comments should not count:\n%q",
				i, ln.depthAfter, src[ln.start:ln.end])
		}
	}
}

func TestCodeChunkPositionsAreSaneAndOrdered(t *testing.T) {
	for name, src := range codeCorpora() {
		prevEnd := 0
		for i, s := range ChunkSpansCode(src, 300) {
			if s.Start < 0 || s.End > len(src) || s.End < s.Start {
				t.Errorf("%s: chunk %d spans %d-%d of %d bytes", name, i, s.Start, s.End, len(src))
			}
			if s.Start < prevEnd {
				t.Errorf("%s: chunk %d starts at %d, before the previous ended at %d",
					name, i, s.Start, prevEnd)
			}
			if got := src[s.Start:s.End]; got != s.Text {
				t.Errorf("%s: chunk %d text does not match its span\n span: %q\n text: %q",
					name, i, trunc(got), trunc(s.Text))
			}
			prevEnd = s.End
		}
	}
}

func TestCodeChunkerOnSmallAndEmptyInput(t *testing.T) {
	if got := ChunkSpansCode("", 100); got != nil {
		t.Errorf("empty input gave %v", got)
	}
	if got := ChunkSpansCode("   \n\n ", 100); got != nil {
		t.Errorf("whitespace-only input gave %v", got)
	}
	small := ".a { color: red; }"
	spans := ChunkSpansCode(small, 100)
	if len(spans) != 1 || spans[0].Text != small {
		t.Errorf("a file under the budget should be one chunk, got %v", spans)
	}
}

// Whatever the corpus, no chunk may be lost: concatenating the chunks must
// account for every non-whitespace byte of the source.
func TestCodeChunkingLosesNothing(t *testing.T) {
	for name, src := range codeCorpora() {
		var joined strings.Builder
		for _, s := range ChunkSpansCode(src, 300) {
			joined.WriteString(s.Text)
		}
		want := strings.Join(strings.Fields(src), "")
		got := strings.Join(strings.Fields(joined.String()), "")
		if got != want {
			t.Errorf("%s: chunking lost or duplicated content (%d bytes vs %d)",
				name, len(got), len(want))
		}
	}
}

// The acceptance case from issue #192: a real theme stylesheet, chunked so
// that a vector hit lands on something an agent can edit.
func TestThemeStylesheetChunksIntoEditableUnits(t *testing.T) {
	css := `/* Theme: base layout */
:root {
  --brand: #2563eb;
  --ink: #111827;
}

.site-header {
  display: flex;
  align-items: center;
  padding: 1rem 2rem;
  background: var(--brand);
}

.hero-banner {
  min-height: 24rem;
  background: url("/images/hero.png") center / cover no-repeat;
  color: white;
}

.hero-banner .title {
  font-size: clamp(2rem, 5vw, 4rem);
  margin: 0 0 1rem;
}

.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(16rem, 1fr));
  gap: 1.5rem;
}

.site-footer {
  padding: 2rem;
  color: #94a3b8;
}
`
	spans := ChunkSpansCode(css, 200)
	if len(spans) < 3 {
		t.Fatalf("expected the stylesheet to split into several units, got %d", len(spans))
	}

	for i, s := range spans {
		if bracketDepth(s.Text) != 0 {
			t.Errorf("chunk %d is not a complete unit:\n%q", i, s.Text)
		}
		// Every chunk should contain at least one complete rule, and no rule
		// should be left half-open.
		if opens, closes := strings.Count(s.Text, "{"), strings.Count(s.Text, "}"); opens != closes {
			t.Errorf("chunk %d has %d { and %d }:\n%q", i, opens, closes, s.Text)
		}
	}

	// The declaration an agent would look for must live inside one chunk, not
	// straddle two.
	var found bool
	for _, s := range spans {
		if strings.Contains(s.Text, "background: url(") {
			found = true
			if !strings.Contains(s.Text, ".hero-banner {") {
				t.Errorf("the background declaration was separated from its selector:\n%q", s.Text)
			}
		}
	}
	if !found {
		t.Error("the background declaration disappeared from the chunks")
	}
}

// Prose must keep its old segmentation exactly: the code mode is opt-in.
func TestProseChunkingUnchangedByCodeMode(t *testing.T) {
	prose := strings.Repeat("A paragraph about something. It has two sentences.\n\n", 40)
	before := ChunkText(prose, 300)
	after := chunkTextsMode(prose, 300, ChunkModeProse)

	if len(before) != len(after) {
		t.Fatalf("prose chunk count changed: %d vs %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("prose chunk %d changed:\n old: %q\n new: %q", i, trunc(before[i]), trunc(after[i]))
		}
	}
}
