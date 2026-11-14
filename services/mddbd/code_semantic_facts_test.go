package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"mddb/internal/storage"
)

// The semantic-facts cases are shared: mddb-cli encodes `facts` into
// `encoded`, and the server must keep exactly `stored` of what it is sent
// (CODE-010). Both sides run testdata/semantic-facts/cases.json.
const semanticFactsCasesPath = "../../testdata/semantic-facts/cases.json"

type semanticFactsCase struct {
	Name          string              `json:"name"`
	Key           string              `json:"key"`
	Path          string              `json:"path"`
	Content       string              `json:"content"`
	StoredContent *string             `json:"storedContent"`
	IsCode        bool                `json:"isCode"`
	MaxFacts      int                 `json:"maxFacts"`
	Encoded       map[string][]string `json:"encoded"`
	Sent          map[string][]string `json:"sent"`
	Stored        map[string][]string `json:"stored"`
}

func loadSemanticFactsCases(t *testing.T) []semanticFactsCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(semanticFactsCasesPath))
	if err != nil {
		t.Fatalf("read shared cases: %v", err)
	}
	var cases []semanticFactsCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse shared cases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no shared cases")
	}
	return cases
}

// input is the document the server receives for a case: its key and path,
// the content it stores and the meta the client sent.
func (c semanticFactsCase) input() (meta map[string][]string, content string) {
	meta = map[string][]string{"path": {c.Path}}
	sent := c.Sent
	if sent == nil {
		sent = c.Encoded
	}
	for k, v := range sent {
		meta[k] = append([]string(nil), v...)
	}
	content = c.Content
	if c.StoredContent != nil {
		content = *c.StoredContent
	}
	return meta, content
}

// symMeta is the sym* part of meta, empty map when there is none.
func symMeta(meta map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, k := range semanticFactKeys {
		if v, ok := meta[k]; ok {
			out[k] = v
		}
	}
	return out
}

func TestSemanticFactsSharedCases(t *testing.T) {
	for _, tc := range loadSemanticFactsCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.IsCode != (InferCodeLanguage(tc.Key) != "") {
				t.Fatalf("case key %q does not classify as isCode=%v", tc.Key, tc.IsCode)
			}
			t.Setenv("MDDB_CODE_MAX_FACTS", strconv.Itoa(tc.MaxFacts))
			meta, content := tc.input()
			doc := storage.Doc{Key: tc.Key, Meta: meta, ContentMD: content}
			EnrichCodeSymbolsWith(&doc, nil)
			if got := symMeta(doc.Meta); !reflect.DeepEqual(got, tc.Stored) {
				t.Errorf("kept %#v\nwant %#v", got, tc.Stored)
			}
			// The same through the write path every transport shares, and
			// back through a single-document read.
			s, cleanup := newHandlerTestServer(t)
			defer cleanup()
			meta, content = tc.input()
			if _, _, err := s.addDocumentIf("facts", tc.Key, "en", meta, content, 0, false, ""); err != nil {
				t.Fatalf("add: %v", err)
			}
			if got := symMeta(getDocMeta(t, s, "facts", tc.Key)); !reflect.DeepEqual(got, tc.Stored) {
				t.Errorf("stored %#v\nwant %#v", got, tc.Stored)
			}
		})
	}
}

func TestKeepSemanticFactsIsIdempotent(t *testing.T) {
	// A write that changes nothing but the TTL runs the check again on what
	// is stored; a document the server already trimmed must come out the
	// same, " truncated" and all.
	for _, tc := range loadSemanticFactsCases(t) {
		meta, content := tc.input()
		doc := storage.Doc{Key: tc.Key, Meta: meta, ContentMD: content}
		keepSemanticFacts(&doc, tc.MaxFacts)
		once := symMeta(doc.Meta)
		keepSemanticFacts(&doc, tc.MaxFacts)
		if again := symMeta(doc.Meta); !reflect.DeepEqual(again, once) {
			t.Errorf("%s: second pass %#v, first %#v", tc.Name, again, once)
		}
	}
}

func TestKeepSemanticFactsEdges(t *testing.T) {
	keepSemanticFacts(nil, 1) // no panic
	enforceSemanticFacts(nil)

	plain := &storage.Doc{Key: "a.go", Meta: map[string][]string{"tag": {"x"}}, ContentMD: "x"}
	keepSemanticFacts(plain, 1)
	if !reflect.DeepEqual(plain.Meta, map[string][]string{"tag": {"x"}}) {
		t.Errorf("a document without facts changed: %v", plain.Meta)
	}

	content := "package a\n"
	index := "v1 " + semanticContentHash(content) + " d gopls/1"
	doc := &storage.Doc{Key: "a.go", ContentMD: content, Meta: map[string][]string{
		MetaKeySymIndex: {index},
		MetaKeySymDefs:  {"A function 1-1"},
		MetaKeySymRefs:  {"b.go#B 1"},
	}}
	keepSemanticFacts(doc, -5) // a negative limit keeps nothing, like zero
	want := map[string][]string{MetaKeySymIndex: {index + " truncated"}}
	if got := symMeta(doc.Meta); !reflect.DeepEqual(got, want) {
		t.Errorf("negative limit kept %v", got)
	}

	// symIndex values the server refuses outright.
	for _, idx := range [][]string{
		nil,
		{index, index},
		{index + " extra"},
		{index + " truncated extra"},
		{"v1 " + semanticContentHash(content) + " d bad|source"},
		{"v1 000000000000 d gopls/1"},
	} {
		doc := &storage.Doc{Key: "a.go", ContentMD: content, Meta: map[string][]string{
			MetaKeySymIndex: idx, MetaKeySymDefs: {"A function 1-1"},
		}}
		if idx == nil {
			delete(doc.Meta, MetaKeySymIndex)
		}
		keepSemanticFacts(doc, 10)
		if hasSemanticFacts(doc.Meta) {
			t.Errorf("symIndex %q: kept %v", idx, doc.Meta)
		}
	}
}

func TestEnforceSemanticFactsReadsTheLimit(t *testing.T) {
	t.Setenv("MDDB_CODE_MAX_FACTS", "1")
	content := "package a\n"
	doc := &storage.Doc{Key: "a.go", ContentMD: content, Meta: map[string][]string{
		MetaKeySymIndex: {"v1 " + semanticContentHash(content) + " d gopls/1"},
		MetaKeySymDefs:  {"B function 1-1", "A function 1-1"},
	}}
	enforceSemanticFacts(doc)
	if got := doc.Meta[MetaKeySymDefs]; !reflect.DeepEqual(got, []string{"A function 1-1"}) {
		t.Errorf("symDefs = %v, want the first one in sorted order", got)
	}
	if !strings.HasSuffix(doc.Meta[MetaKeySymIndex][0], " truncated") {
		t.Errorf("symIndex = %v, want truncated", doc.Meta[MetaKeySymIndex])
	}
}

func TestLoadResultDocHidesFacts(t *testing.T) {
	doc := &storage.Doc{ID: "c|a.go|en", Key: "a.go", Lang: "en", ContentMD: "x", Meta: map[string][]string{
		MetaKeySymIndex: {"v1"}, MetaKeySymDefs: {"d"}, MetaKeySymRefs: {"r"}, MetaKeySymImpls: {"i"},
		MetaKeyDefines: {"A"}, "tag": {"t"},
	}}
	buf, err := marshalDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadResultDoc(buf)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{MetaKeyDefines: {"A"}, "tag": {"t"}}
	if !reflect.DeepEqual(got.Meta, want) {
		t.Errorf("result meta = %v, want %v", got.Meta, want)
	}
	if full, _ := loadDoc(buf); len(full.Meta) != 6 {
		t.Errorf("loadDoc must keep every key, got %v", full.Meta)
	}
	if _, err := loadResultDoc(nil); err == nil {
		t.Error("loadResultDoc(nil) should fail like loadDoc")
	}
	hideSemanticFacts(nil) // no panic
}

func TestSemanticContentHash(t *testing.T) {
	// The first 12 hex digits of SHA-256, as the reference encoder computes.
	if got := semanticContentHash("package a\n"); got != "7b39baa38a2e" {
		t.Errorf("hash = %q", got)
	}
}
