package lspindex

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// casesFile holds the format's shared cases; mddbd runs the same file.
const casesFile = "../../../../testdata/semantic-facts/cases.json"

type sharedCase struct {
	Name    string              `json:"name"`
	Content string              `json:"content"`
	Source  string              `json:"source"`
	Facts   *FileFacts          `json:"facts"`
	Encoded map[string][]string `json:"encoded"`
}

func TestEncodeSharedCases(t *testing.T) {
	data, err := os.ReadFile(casesFile)
	if err != nil {
		t.Fatal(err)
	}
	var cases []sharedCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, c := range cases {
		if c.Facts == nil {
			continue // server-only case
		}
		ran++
		t.Run(c.Name, func(t *testing.T) {
			got := Encode(c.Content, c.Source, c.Facts)
			if !reflect.DeepEqual(got, c.Encoded) {
				t.Errorf("Encode =\n%v\nwant\n%v", got, c.Encoded)
			}
			// Every fact the cases hold is one Index may return.
			for _, d := range c.Facts.Defs {
				if !defEncodable(d) {
					t.Errorf("def %+v reported unencodable", d)
				}
			}
			for _, r := range c.Facts.Refs {
				if !refEncodable(r) {
					t.Errorf("ref %+v reported unencodable", r)
				}
			}
			for _, i := range c.Facts.Impls {
				if !implEncodable(i) {
					t.Errorf("impl %+v reported unencodable", i)
				}
			}
		})
	}
	if ran < 6 {
		t.Fatalf("only %d cases with facts in %s", ran, casesFile)
	}
}

func TestEncodeNilFacts(t *testing.T) {
	got := Encode("x", "gopls/0.24.0", nil)
	if len(got) != 1 || len(got[KeyIndex]) != 1 || !strings.HasPrefix(got[KeyIndex][0], "v1 ") {
		t.Errorf("Encode(nil) = %v", got)
	}
}

func TestRefValuesDedupesAndSorts(t *testing.T) {
	got := refValues(Ref{Target: "a.go#f", Lines: []int{3, 1, 3, 2}})
	if !reflect.DeepEqual(got, []string{"a.go#f 1,2,3"}) {
		t.Errorf("refValues = %v", got)
	}
}

func TestEncodability(t *testing.T) {
	long := strings.Repeat("x", maxValueBytes)
	defs := map[string]bool{
		"ok":            defEncodable(Def{Name: "A.b", Kind: "method", Start: 1, End: 2, Container: "A"}),
		"space":         !defEncodable(Def{Name: "a b", Kind: "function", Start: 1, End: 1}),
		"pipe":          !defEncodable(Def{Name: "a|b", Kind: "function", Start: 1, End: 1}),
		"hash":          !defEncodable(Def{Name: "a#b", Kind: "function", Start: 1, End: 1}),
		"empty":         !defEncodable(Def{Kind: "function", Start: 1, End: 1}),
		"kind":          !defEncodable(Def{Name: "a", Kind: "variable", Start: 1, End: 1}),
		"zero line":     !defEncodable(Def{Name: "a", Kind: "function", Start: 0, End: 1}),
		"backwards":     !defEncodable(Def{Name: "a", Kind: "function", Start: 3, End: 2}),
		"container":     !defEncodable(Def{Name: "a", Kind: "function", Start: 1, End: 1, Container: "x y"}),
		"too long":      !defEncodable(Def{Name: long, Kind: "function", Start: 1, End: 1}),
		"unicode space": !defEncodable(Def{Name: "a b", Kind: "function", Start: 1, End: 1}),
		"separator":     !defEncodable(Def{Name: "a\x1cb", Kind: "function", Start: 1, End: 1}),
	}
	refs := map[string]bool{
		"ok":          refEncodable(Ref{Target: "c#/y.go#Run", Lines: []int{1}}),
		"no lines":    !refEncodable(Ref{Target: "a.go#f"}),
		"no hash":     !refEncodable(Ref{Target: "a.go", Lines: []int{1}}),
		"empty path":  !refEncodable(Ref{Target: "#f", Lines: []int{1}}),
		"path space":  !refEncodable(Ref{Target: "a b.go#f", Lines: []int{1}}),
		"path pipe":   !refEncodable(Ref{Target: "a|b.go#f", Lines: []int{1}}),
		"empty name":  !refEncodable(Ref{Target: "a.go#", Lines: []int{1}}),
		"too long":    !refEncodable(Ref{Target: long + "#f", Lines: []int{1}}),
		"impl ok":     implEncodable(Impl{Target: "a.go#I", Local: "T"}),
		"impl local":  !implEncodable(Impl{Target: "a.go#I", Local: "T U"}),
		"impl target": !implEncodable(Impl{Target: "a.go", Local: "T"}),
		"impl long":   !implEncodable(Impl{Target: "a.go#I", Local: long}),
	}
	for name, ok := range defs {
		if !ok {
			t.Errorf("def case %q", name)
		}
	}
	for name, ok := range refs {
		if !ok {
			t.Errorf("ref/impl case %q", name)
		}
	}
}
