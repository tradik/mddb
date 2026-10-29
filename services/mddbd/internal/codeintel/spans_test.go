package codeintel

import (
	"fmt"
	"strings"
	"testing"
)

// spanList renders spans as "symbol:kind:start-end<enclosing" for compact
// comparison.
func spanList(spans []Span) string {
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		p := fmt.Sprintf("%s:%s:%d-%d", s.Symbol, s.Kind, s.StartLine, s.EndLine)
		if s.Enclosing != "" {
			p += "<" + s.Enclosing
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

func TestSpans_Languages(t *testing.T) {
	cases := []struct {
		lang, src, want string
	}{
		{"go", "package x\n\n// Search finds\nfunc Search(q string,\n\tlimit int) []int {\n\ts := `a\n{b`\n\treturn nil\n}\n\ntype ID int\n\ntype Index struct {\n\tn int\n}\n\nfunc (i *Index) Len() int { return i.n }\n",
			"Search:function:4-9 ID:type:11-11 Index:type:13-15 Len:method:17-17"},
		{"rust", "struct Index {\n    n: usize,\n}\n\nimpl Index {\n    pub fn search(&self) -> usize\n    where Self: Sized\n    {\n        self.n\n    }\n}\n\nfn helper() {}\n",
			"Index:type:1-3 Index:impl:5-11 search:function:6-10<Index helper:function:13-13"},
		{"java", "public class Repo {\n    public List<Doc> find(String q)\n        throws IOException {\n        return null;\n    }\n}\n",
			"Repo:class:1-6 find:method:2-5<Repo"},
		{"javascript", "class Cart {\n  total(items) {\n    if (items) {\n      return 1;\n    }\n  }\n}\nfunction render() {\n  return `${x}`;\n}\nconst add = (a, b) => {\n  return a + b;\n};\nconst one = () => 1;\n",
			"Cart:class:1-7 total:method:2-6<Cart render:function:8-10 add:function:11-13 one:function:14-14"},
		{"typescript", "export async function load(): Promise<void> {\n}\n", "load:function:1-2"},
		{"php", "<?php\nclass Shop {\n    public function pay($x) {\n        return \"}\";\n    }\n}\n",
			"Shop:class:2-6 pay:function:3-5<Shop"},
		{"python", "import os\n\nclass Store:\n    \"\"\"Docs\n    span lines\n    \"\"\"\n    def get(self, k):\n        return k\n\n    async def put(self):\n        pass\n\ndef main():\n    pass\n",
			"Store:class:3-11 get:function:7-8<Store put:function:10-11<Store main:function:13-14"},
		{"ruby", "class Cart\n  def total\n    1\n  end\n\n  def self.empty?\n    true\n  end\nend\n",
			"Cart:class:1-9 total:method:2-4<Cart empty?:method:6-8<Cart"},
		{"css", "/* a\n   b */\n.card, .card > .title {\n  color: red;\n}\n@media (x) {\n  .hero { margin: 0 }\n}\n",
			".card:rule:3-5 .card > .title:rule:3-5 .hero:rule:7-7"},
		{"c", "#define MAX 10\nstruct node {\n  int v;\n};\nint walk(struct node *n,\n         int depth)\n{\n  return helper(n);\n}\nint proto(int);\n",
			"MAX:macro:1-1 node:type:2-4 walk:function:5-9"},
		{"terraform", "resource \"aws_instance\" \"web\" {\n  ami = \"x\"\n}\n\ndata \"aws_ami\" \"base\" {\n}\nvariable \"region\" {}\n",
			"aws_instance.web:block:1-3 data.aws_ami.base:block:5-6 var.region:block:7-7"},
		{"asm", "ENTRY(start)\nstart:\n  call init\n  ret\ninit:\n  ret\n", "start:label:2-4 init:label:5-6"},
		{"yaml", "a: 1\n", ""},
		{"html", "<div id=\"x\"></div>", ""},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			got := spanList(Spans(tc.lang, tc.src))
			if got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
			// Deterministic: a second run is identical.
			if again := spanList(Spans(tc.lang, tc.src)); again != got {
				t.Errorf("not deterministic: %s", again)
			}
		})
	}
}

func TestSymbolAt(t *testing.T) {
	spans := Spans("go", "package x\n\ntype T struct{}\n\nfunc (t T) A() {\n\tB()\n}\n\nfunc B() {}\n")
	if s := SymbolAt(spans, 6); s == nil || s.Symbol != "A" {
		t.Fatalf("line 6: %+v", s)
	}
	if s := SymbolAt(spans, 1); s != nil {
		t.Fatalf("line 1 has no definition: %+v", s)
	}
	if !(Span{StartLine: 2, EndLine: 4}).Contains(3) {
		t.Fatal("Contains")
	}
}

func TestSpans_Edges(t *testing.T) {
	// An unterminated body runs to the end of the file.
	if got := spanList(Spans("go", "func f() {\n\tx()\n")); got != "f:function:1-2" {
		t.Fatalf("unterminated: %s", got)
	}
	// A continued signature with an operator at the line end.
	if got := spanList(Spans("go", "func f() int {\n\treturn 1 +\n\t\t2\n}\n")); got != "f:function:1-4" {
		t.Fatalf("continued: %s", got)
	}
	if indentOf("\t  x") != 10 || indentOf("") != 0 {
		t.Fatal("indentOf")
	}
}

func TestWithoutComments(t *testing.T) {
	got := WithoutComments("go", "a // x\n/* y\nz */ b \"// q\"\n")
	if strings.Contains(got, "x") || strings.Contains(got, "y") || !strings.Contains(got, `"// q"`) || strings.Count(got, "\n") != 3 {
		t.Fatalf("go: %q", got)
	}
	if got := WithoutComments("css", "a { b: url(//cdn/x) } /* c */"); !strings.Contains(got, "//cdn") || strings.Contains(got, " c ") {
		t.Fatalf("css: %q", got)
	}
	if WithoutComments("html", "<!-- x -->") != "<!-- x -->" {
		t.Fatal("html unchanged")
	}
}
