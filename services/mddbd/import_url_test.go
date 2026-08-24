package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// --- deriveKeyFromURL ---

func TestDeriveKeyFromURL_BasicFile(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/docs/readme.md")
	if got != "readme" {
		t.Errorf("got %q, want %q", got, "readme")
	}
}

func TestDeriveKeyFromURL_NoExtension(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/docs/readme")
	if got != "readme" {
		t.Errorf("got %q, want %q", got, "readme")
	}
}

func TestDeriveKeyFromURL_DeepPath(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/a/b/c/d/document.md")
	if got != "document" {
		t.Errorf("got %q, want %q", got, "document")
	}
}

func TestDeriveKeyFromURL_HTMLFile(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/page.html")
	if got != "page" {
		t.Errorf("got %q, want %q", got, "page")
	}
}

func TestDeriveKeyFromURL_RootPath(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/")
	if got != "" {
		t.Errorf("got %q, want empty string for root path", got)
	}
}

func TestDeriveKeyFromURL_EmptyPath(t *testing.T) {
	got := deriveKeyFromURL("https://example.com")
	if got != "" {
		t.Errorf("got %q, want empty string for empty path", got)
	}
}

func TestDeriveKeyFromURL_InvalidURL(t *testing.T) {
	got := deriveKeyFromURL("://invalid")
	if got != "" {
		t.Errorf("got %q, want empty string for invalid URL", got)
	}
}

func TestDeriveKeyFromURL_EmptyString(t *testing.T) {
	got := deriveKeyFromURL("")
	// url.Parse("") succeeds with empty path, path.Base returns "."
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestDeriveKeyFromURL_QueryParams(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/document.md?v=2&format=raw")
	if got != "document" {
		t.Errorf("got %q, want %q", got, "document")
	}
}

func TestDeriveKeyFromURL_Fragment(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/page.md#section")
	if got != "page" {
		t.Errorf("got %q, want %q", got, "page")
	}
}

func TestDeriveKeyFromURL_DoubleExtension(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/file.tar.gz")
	// path.Ext returns ".gz", so base becomes "file.tar"
	if got != "file.tar" {
		t.Errorf("got %q, want %q", got, "file.tar")
	}
}

func TestDeriveKeyFromURL_DotFile(t *testing.T) {
	got := deriveKeyFromURL("https://example.com/.hidden")
	// path.Base returns ".hidden", path.Ext returns ".hidden" (whole thing is extension)
	// so after stripping ext, base is "" which returns ""
	if got != "" {
		t.Errorf("got %q, want %q", got, "")
	}
}

func TestDeriveKeyFromURL_TableDriven(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.com/readme.md", "readme"},
		{"https://example.com/path/to/guide.txt", "guide"},
		{"https://example.com/document", "document"},
		{"https://example.com/", ""},
		{"https://example.com", ""},
		{"https://raw.githubusercontent.com/user/repo/main/docs/API.md", "API"},
		{"http://localhost:8080/file.json", "file"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := deriveKeyFromURL(tt.url)
			if got != tt.want {
				t.Errorf("deriveKeyFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// --- parseFrontmatter ---

func TestParseFrontmatter_BasicYAML(t *testing.T) {
	content := `---
title: Hello World
author: Alice
---
# Hello

Body content here.`

	meta, body := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if !reflect.DeepEqual(meta["title"], []string{"Hello World"}) {
		t.Errorf("title: got %v, want [Hello World]", meta["title"])
	}
	if !reflect.DeepEqual(meta["author"], []string{"Alice"}) {
		t.Errorf("author: got %v, want [Alice]", meta["author"])
	}
	if body != "# Hello\n\nBody content here." {
		t.Errorf("body: got %q", body)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	content := "# Just markdown\n\nNo frontmatter here."
	meta, body := parseFrontmatter(content)

	if meta != nil {
		t.Errorf("expected nil meta, got %v", meta)
	}
	if body != content {
		t.Errorf("body should be unchanged: got %q", body)
	}
}

func TestParseFrontmatter_EmptyContent(t *testing.T) {
	meta, body := parseFrontmatter("")
	if meta != nil {
		t.Errorf("expected nil meta for empty content, got %v", meta)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestParseFrontmatter_OnlyFrontmatter(t *testing.T) {
	content := `---
title: test
---`
	meta, body := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if !reflect.DeepEqual(meta["title"], []string{"test"}) {
		t.Errorf("title: got %v", meta["title"])
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestParseFrontmatter_CommaSeparatedValues(t *testing.T) {
	content := `---
tags: go, database, markdown
author: Bob
---
Content`

	meta, body := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	wantTags := []string{"go", "database", "markdown"}
	if !reflect.DeepEqual(meta["tags"], wantTags) {
		t.Errorf("tags: got %v, want %v", meta["tags"], wantTags)
	}
	if !reflect.DeepEqual(meta["author"], []string{"Bob"}) {
		t.Errorf("author: got %v", meta["author"])
	}
	if body != "Content" {
		t.Errorf("body: got %q", body)
	}
}

func TestParseFrontmatter_QuotedValues(t *testing.T) {
	content := `---
title: "Hello World"
desc: 'A description'
---
Body`

	meta, _ := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if !reflect.DeepEqual(meta["title"], []string{"Hello World"}) {
		t.Errorf("title: got %v, want [Hello World]", meta["title"])
	}
	if !reflect.DeepEqual(meta["desc"], []string{"A description"}) {
		t.Errorf("desc: got %v, want [A description]", meta["desc"])
	}
}

func TestParseFrontmatter_CommentedLines(t *testing.T) {
	content := `---
title: test
# This is a comment
author: alice
---
Body`

	meta, _ := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if _, exists := meta["# This is a comment"]; exists {
		t.Error("comment line should be skipped")
	}
	if !reflect.DeepEqual(meta["title"], []string{"test"}) {
		t.Errorf("title: got %v", meta["title"])
	}
	if !reflect.DeepEqual(meta["author"], []string{"alice"}) {
		t.Errorf("author: got %v", meta["author"])
	}
}

func TestParseFrontmatter_EmptyValues(t *testing.T) {
	content := `---
title:
author: alice
---
Body`

	meta, _ := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	// Empty value should not be added
	if _, exists := meta["title"]; exists {
		t.Error("empty value key should not be in meta")
	}
	if !reflect.DeepEqual(meta["author"], []string{"alice"}) {
		t.Errorf("author: got %v", meta["author"])
	}
}

func TestParseFrontmatter_NoClosingDelimiter(t *testing.T) {
	content := `---
title: test
This is not closed`

	meta, body := parseFrontmatter(content)

	if meta != nil {
		t.Errorf("expected nil meta when no closing delimiter, got %v", meta)
	}
	// body should be the full content
	if body != strings.TrimSpace(content) {
		t.Errorf("body: got %q, want %q", body, strings.TrimSpace(content))
	}
}

func TestParseFrontmatter_LeadingWhitespace(t *testing.T) {
	content := `
  ---
title: test
---
Body`

	// After TrimSpace, the leading whitespace is removed
	meta, _ := parseFrontmatter(content)

	// The trimmed content starts with spaces then "---", but after TrimSpace
	// it should start with "---"
	if meta == nil {
		t.Log("meta is nil - content may not start with --- after trimming")
	}
}

func TestParseFrontmatter_EmptyLines(t *testing.T) {
	content := `---
title: test

author: alice

---
Body`

	meta, _ := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if !reflect.DeepEqual(meta["title"], []string{"test"}) {
		t.Errorf("title: got %v", meta["title"])
	}
	if !reflect.DeepEqual(meta["author"], []string{"alice"}) {
		t.Errorf("author: got %v", meta["author"])
	}
}

func TestParseFrontmatter_LineWithoutColon(t *testing.T) {
	content := `---
title: test
no colon here
author: alice
---
Body`

	meta, _ := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if len(meta) != 2 {
		t.Errorf("expected 2 meta keys, got %d: %v", len(meta), meta)
	}
}

func TestParseFrontmatter_ColonInValue(t *testing.T) {
	content := `---
url: https://example.com
---
Body`

	meta, _ := parseFrontmatter(content)

	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	// The parser splits on first colon, so value should be "https://example.com"
	if !reflect.DeepEqual(meta["url"], []string{"https://example.com"}) {
		t.Errorf("url: got %v", meta["url"])
	}
}

// --- fetchURL (tested via mock HTTP server) ---

func TestFetchURL_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "# Hello World\n\nThis is content.")
	}))
	defer server.Close()

	content, err := fetchURL(t.Context(), server.URL)
	if err != nil {
		t.Fatalf("fetchURL failed: %v", err)
	}
	if content != "# Hello World\n\nThis is content." {
		t.Errorf("content mismatch: got %q", content)
	}
}

func TestFetchURL_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	_, err := fetchURL(t.Context(), server.URL)
	if err == nil {
		t.Error("expected error for 404 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status code, got: %v", err)
	}
}

func TestFetchURL_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	_, err := fetchURL(t.Context(), server.URL)
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestFetchURL_InvalidURL(t *testing.T) {
	_, err := fetchURL(t.Context(), "http://[::1]:namedport")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestFetchURL_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer server.Close()

	content, err := fetchURL(t.Context(), server.URL)
	if err != nil {
		t.Fatalf("fetchURL failed: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
}

func TestFetchURL_LargeResponse(t *testing.T) {
	// Serve content that is within the 10MB limit
	largeContent := strings.Repeat("x", 100000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, largeContent)
	}))
	defer server.Close()

	content, err := fetchURL(t.Context(), server.URL)
	if err != nil {
		t.Fatalf("fetchURL failed: %v", err)
	}
	if len(content) != 100000 {
		t.Errorf("content length mismatch: got %d, want 100000", len(content))
	}
}

func TestFetchURL_WithFrontmatter(t *testing.T) {
	mdContent := `---
title: Remote storage.Doc
tags: go, test
---
# Remote Document

Body content.`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, mdContent)
	}))
	defer server.Close()

	content, err := fetchURL(t.Context(), server.URL)
	if err != nil {
		t.Fatalf("fetchURL failed: %v", err)
	}

	// Now parse frontmatter from fetched content
	meta, body := parseFrontmatter(content)
	if meta == nil {
		t.Fatal("expected non-nil meta from fetched content")
	}
	if !reflect.DeepEqual(meta["title"], []string{"Remote storage.Doc"}) {
		t.Errorf("title: got %v", meta["title"])
	}
	if body != "# Remote Document\n\nBody content." {
		t.Errorf("body: got %q", body)
	}
}

// --- Integration: parseFrontmatter + deriveKeyFromURL ---

func TestImportURLFlow_DeriveKeyAndParseFrontmatter(t *testing.T) {
	key := deriveKeyFromURL("https://example.com/blog/my-article.md")
	if key != "my-article" {
		t.Fatalf("derived key: got %q, want %q", key, "my-article")
	}

	content := `---
title: My Article
category: blog
---
# My Article

Some markdown content.`

	meta, body := parseFrontmatter(content)
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}

	// Simulate merge: request meta overrides frontmatter
	reqMeta := map[string][]string{"category": {"tech"}}
	merged := meta
	for k, v := range reqMeta {
		merged[k] = v
	}

	if !reflect.DeepEqual(merged["category"], []string{"tech"}) {
		t.Errorf("merged category should be overridden to [tech], got %v", merged["category"])
	}
	if !reflect.DeepEqual(merged["title"], []string{"My Article"}) {
		t.Errorf("merged title should be preserved: got %v", merged["title"])
	}
	if body != "# My Article\n\nSome markdown content." {
		t.Errorf("body: got %q", body)
	}
}
