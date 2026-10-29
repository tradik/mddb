package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func covBB64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// TestCovBAdvancedToolsPassClientErrorsThrough checks each advanced tool
// returns the client's own error when its arguments are valid.
func TestCovBAdvancedToolsPassClientErrorsThrough(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"get_collection_config":   {"collection": "c"},
		"set_collection_config":   {"collection": "c"},
		"list_collection_configs": {},
		"cross_search":            {"target_collections": []interface{}{"a"}},
		"aggregate":               {"collection": "c"},
		"find_duplicates":         {"collection": "c"},
		"ingest_documents":        {"collection": "c", "documents": []interface{}{}},
		"upload_file":             {"collection": "c", "filename": "a.md", "content": covBB64("x"), "lang": "en"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			fake := covBNewFake()
			fake.err = errCovBClient
			if out, err := covBRun(t, fake, name, args); !errors.Is(err, errCovBClient) || out != "" {
				t.Fatalf("out=%q err=%v, want errCovBClient", out, err)
			}
		})
	}
}

func TestCovBAdvancedToolsRequireArguments(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"get_collection_config", map[string]interface{}{}, "collection is required"},
		{"set_collection_config", map[string]interface{}{}, "collection is required"},
		{"cross_search", map[string]interface{}{"target_collections": []interface{}{1.0}}, "target_collections is required"},
		{"ingest_documents", map[string]interface{}{"documents": "x"}, "documents must be an array"},
		{"ingest_documents", map[string]interface{}{"documents": []interface{}{"x"}}, "invalid document at index 0"},
		{"upload_file", map[string]interface{}{"collection": "c", "filename": "a.md", "content": "eA=="}, "missing required fields: collection, filename, content, lang"},
		{"upload_file", map[string]interface{}{"collection": "c", "filename": "a.md", "content": "!!!not base64!!!", "lang": "en"}, "invalid base64 content"},
		{"upload_file", map[string]interface{}{"collection": "c", "filename": "a.exe", "content": covBB64("x"), "lang": "en"}, "unsupported format: exe"},
		{"upload_file", map[string]interface{}{"collection": "c", "filename": "a.pdf", "content": covBB64("not a pdf"), "lang": "en"}, "pdf conversion"},
		{"upload_file", map[string]interface{}{"collection": "c", "filename": "a.docx", "content": covBB64("not a zip"), "lang": "en"}, "docx conversion"},
		{"upload_file", map[string]interface{}{"collection": "c", "filename": "a.odt", "content": covBB64("not a zip"), "lang": "en"}, "odt conversion"},
		{"upload_file", map[string]interface{}{"collection": "c", "filename": ".md", "content": covBB64("x"), "lang": "en"}, "cannot derive key"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			fake := covBNewFake()
			_, err := covBRun(t, fake, tc.name, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(fake.calls) != 0 {
				t.Errorf("invalid input reached the client: %v", fake.calls)
			}
		})
	}
}

func TestCovBSetCollectionConfigForwardsEveryOption(t *testing.T) {
	fake := covBNewFake()
	out, err := covBRun(t, fake, "set_collection_config", map[string]interface{}{
		"collection": "docs", "type": "code", "description": "d", "icon": "i", "color": "#123456",
		"custom_meta":   map[string]interface{}{"owner": "me", "skip": 1.0},
		"wordpress":     map[string]interface{}{"url": "https://wp", "api_key": "k"},
		"max_revisions": 5.0,
		"code":          map[string]interface{}{"include_paths": []interface{}{"src/"}},
		"fts":           map[string]interface{}{"enabled": false},
		"embedding":     map[string]interface{}{"chunk_size": 256.0, "chunk_mode": "markdown"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != `Collection "docs" config updated` {
		t.Errorf("out = %q", out)
	}
	r := fake.covBCall(t, "SetCollectionConfig").(*MCPSetCollectionConfigRequest)
	if r.Type != "code" || r.Description != "d" || r.Icon != "i" || r.Color != "#123456" {
		t.Errorf("scalars = %+v", r)
	}
	if len(r.CustomMeta) != 1 || r.CustomMeta["owner"] != "me" {
		t.Errorf("custom meta = %v", r.CustomMeta)
	}
	if r.WordPress == nil || r.WordPress.URL != "https://wp" || r.WordPress.APIKey != "k" {
		t.Errorf("wordpress = %+v", r.WordPress)
	}
	if r.MaxRevisions == nil || *r.MaxRevisions != 5 {
		t.Errorf("max revisions = %v", r.MaxRevisions)
	}
	if r.Code == nil || len(r.Code.IncludePaths) != 1 || r.Code.IncludePaths[0] != "src/" {
		t.Errorf("code = %+v", r.Code)
	}
	if r.FTS == nil || r.FTS.Enabled == nil || *r.FTS.Enabled {
		t.Errorf("fts = %+v", r.FTS)
	}
	if r.Embedding == nil || r.Embedding.ChunkSize != 256 || r.Embedding.ChunkMode != "markdown" {
		t.Errorf("embedding = %+v", r.Embedding)
	}

	// fts without a boolean "enabled" leaves FTS unset rather than guessing.
	if _, err := covBRun(t, fake, "set_collection_config", map[string]interface{}{"collection": "docs", "fts": map[string]interface{}{"enabled": "yes"}}); err != nil {
		t.Fatal(err)
	}
	if r := fake.covBCall(t, "SetCollectionConfig").(*MCPSetCollectionConfigRequest); r.FTS != nil || r.CustomMeta != nil || r.WordPress != nil {
		t.Errorf("unset options populated: %+v", r)
	}
}

func TestCovBCollectionConfigReadTools(t *testing.T) {
	fake := covBNewFake()
	if out, err := covBRun(t, fake, "get_collection_config", map[string]interface{}{"collection": "docs"}); err != nil || !strings.HasPrefix(out, "{") {
		t.Fatalf("get: %q %v", out, err)
	}
	if c := fake.covBCall(t, "GetCollectionConfig").(string); c != "docs" {
		t.Errorf("collection = %q", c)
	}
	if out, err := covBRun(t, fake, "list_collection_configs", nil); err != nil || !strings.HasPrefix(out, "{") {
		t.Fatalf("list: %q %v", out, err)
	}
}

func TestCovBCrossSearchAggregateDuplicatesForwardArgs(t *testing.T) {
	fake := covBNewFake()
	if _, err := covBRun(t, fake, "cross_search", map[string]interface{}{
		"source_collection": "a", "source_doc_id": "d", "query": "q", "top_k": 4.0, "threshold": 0.5,
		"include_content": true, "target_collections": []interface{}{"x", 1.0, "y"},
		"filter_meta": map[string]interface{}{"lang": "en"},
	}); err != nil {
		t.Fatal(err)
	}
	cs := fake.covBCall(t, "CrossSearch").(*MCPCrossSearchRequest)
	if cs.SourceCollection != "a" || cs.SourceDocID != "d" || cs.TopK != 4 || !cs.IncludeContent ||
		len(cs.TargetCollections) != 2 || cs.TargetCollections[1] != "y" || cs.FilterMeta["lang"][0] != "en" {
		t.Errorf("cross search = %+v", cs)
	}

	if _, err := covBRun(t, fake, "aggregate", map[string]interface{}{
		"collection": "c", "max_facet_size": 7.0,
		"facets":     []interface{}{"tag", map[string]interface{}{"field": "author", "order_by": "value"}, 3.0},
		"histograms": []interface{}{"addedAt", map[string]interface{}{"field": "updatedAt", "interval": "day"}, 3.0},
	}); err != nil {
		t.Fatal(err)
	}
	ag := fake.covBCall(t, "Aggregate").(*AggregateRequest)
	if ag.MaxFacetSize != 7 || len(ag.Facets) != 2 || ag.Facets[0].Field != "tag" || ag.Facets[1].OrderBy != "value" ||
		len(ag.Histograms) != 2 || ag.Histograms[0].Field != "addedAt" || ag.Histograms[1].Interval != "day" {
		t.Errorf("aggregate = %+v", ag)
	}

	if _, err := covBRun(t, fake, "find_duplicates", map[string]interface{}{
		"collection": "c", "mode": "both", "max_docs": 9.0, "threshold": 0.8, "include_content": true,
	}); err != nil {
		t.Fatal(err)
	}
	fd := fake.covBCall(t, "FindDuplicates").(*MCPFindDuplicatesRequest)
	if fd.Mode != "both" || fd.MaxDocs != 9 || fd.Threshold != 0.8 || !fd.IncludeContent {
		t.Errorf("find duplicates = %+v", fd)
	}
}

func TestCovBIngestForwardsDocumentsAndOptions(t *testing.T) {
	fake := covBNewFake()
	out, err := covBRun(t, fake, "ingest_documents", map[string]interface{}{
		"collection": "c",
		"documents": []interface{}{map[string]interface{}{
			"url": "https://x", "key": "k", "lang": "en", "content_md": "body", "scraper": "s",
			"scraped_at": 100.0, "ttl": 50.0, "extract_frontmatter": true, "meta": map[string]interface{}{"a": "b"},
		}},
		"options": map[string]interface{}{
			"skip_duplicates": true, "skip_embeddings": true, "skip_fts": true, "skip_webhooks": true,
			"auto_configure_collection": true, "save_revision": true,
		},
	})
	if err != nil || !strings.HasPrefix(out, "{") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	r := fake.covBCall(t, "Ingest").(*MCPIngestRequest)
	d := r.Documents[0]
	if r.Collection != "c" || d.URL != "https://x" || d.Key != "k" || d.ScrapedAt != 100 || d.TTL != 50 || !d.ExtractFrontmatter || d.Meta["a"][0] != "b" || d.Scraper != "s" {
		t.Errorf("document = %+v", d)
	}
	o := r.Options
	if !o.SkipDuplicates || !o.SkipEmbeddings || !o.SkipFTS || !o.SkipWebhooks || !o.AutoConfigureCollection || !o.SaveRevision {
		t.Errorf("options = %+v", o)
	}
}

// TestCovBUploadFileFormats checks each format branch: what is stored, the
// format reported, and the conversion flag in the upload metadata.
func TestCovBUploadFileFormats(t *testing.T) {
	cases := []struct {
		filename, content, content64, wantFormat string
		wantConverted                            bool
		wantContains                             string
	}{
		{filename: "notes.md", content: "plain text", wantFormat: "md", wantContains: "plain text"},
		{filename: "README", content: "no extension", wantFormat: "", wantContains: "no extension"},
		{filename: "conf.yaml", content: "a: 1", wantFormat: "yaml", wantConverted: true, wantContains: "```yaml\na: 1\n```"},
		{filename: "paper.tex", content: `\section{Intro} hello`, wantFormat: "tex", wantConverted: true, wantContains: "Intro"},
		{filename: "page.htm", content: "<h1>Title</h1><p>para</p>", wantFormat: "html", wantConverted: true, wantContains: "Title"},
		{filename: "doc.rtf", content: `{\rtf1 hello rtf}`, wantFormat: "rtf", wantConverted: true, wantContains: "hello rtf"},
		// URL-safe and unpadded base64 are both accepted.
		{filename: "url.txt", content64: base64.URLEncoding.EncodeToString([]byte("??>>")), wantFormat: "txt", wantContains: "??>>"},
		{filename: "raw.txt", content64: base64.RawStdEncoding.EncodeToString([]byte("ab")), wantFormat: "txt", wantContains: "ab"},
	}
	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			fake := covBNewFake()
			c64 := tc.content64
			if c64 == "" {
				c64 = covBB64(tc.content)
			}
			out, err := covBRun(t, fake, "upload_file", map[string]interface{}{
				"collection": "c", "filename": tc.filename, "content": c64, "lang": "en", "key": "fixed",
			})
			if err != nil {
				t.Fatal(err)
			}
			res := covBUnmarshal(t, out)
			if res["format"] != tc.wantFormat || res["converted"] != tc.wantConverted || res["key"] != "fixed" {
				t.Errorf("result = %v", res)
			}
			r := fake.covBCall(t, "Add").(*MCPAddRequest)
			if !strings.Contains(r.ContentMD, tc.wantContains) {
				t.Errorf("stored %q, want it to contain %q", r.ContentMD, tc.wantContains)
			}
			if r.Meta["upload_filename"][0] != tc.filename || r.Meta["upload_format"][0] != tc.wantFormat {
				t.Errorf("meta = %v", r.Meta)
			}
			if _, conv := r.Meta["upload_converted"]; conv != tc.wantConverted {
				t.Errorf("upload_converted present = %v", conv)
			}
		})
	}
}

// TestCovBUploadFileBinaryFormats checks a real PDF, DOCX and ODT are
// converted to markdown and stored with the conversion flag.
func TestCovBUploadFileBinaryFormats(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want string
	}{
		"a.pdf": {[]byte("%PDF-1.4\nstream\nBT (Hello covB PDF) Tj ET\nendstream\n%%EOF"), "Hello covB PDF"},
		"a.docx": {buildZip(t, map[string]string{
			"word/document.xml":   `<w:document><w:body><w:p><w:r><w:t>Hello covB DOCX</w:t></w:r></w:p></w:body></w:document>`,
			"[Content_Types].xml": "<Types/>",
		}), "Hello covB DOCX"},
		"a.odt": {buildZip(t, map[string]string{
			"content.xml": `<office:text><text:p>Hello covB ODT</text:p></office:text>`,
			"mimetype":    "application/vnd.oasis.opendocument.text",
		}), "Hello covB ODT"},
	}
	for filename, tc := range cases {
		t.Run(filename, func(t *testing.T) {
			fake := covBNewFake()
			out, err := covBRun(t, fake, "upload_file", map[string]interface{}{
				"collection": "c", "filename": filename, "content": base64.StdEncoding.EncodeToString(tc.data), "lang": "en",
			})
			if err != nil {
				t.Fatal(err)
			}
			if covBUnmarshal(t, out)["converted"] != true {
				t.Errorf("not marked converted: %s", out)
			}
			r := fake.covBCall(t, "Add").(*MCPAddRequest)
			if !strings.Contains(r.ContentMD, tc.want) || r.Meta["upload_converted"][0] != "true" || r.Key != "a" {
				t.Errorf("stored %+v", r)
			}
		})
	}
}

// TestCovBUploadFileFrontmatterAndKey checks frontmatter extraction merges
// under explicit meta (explicit wins), and the key derives from the filename.
func TestCovBUploadFileFrontmatterAndKey(t *testing.T) {
	fake := covBNewFake()
	md := "---\ntitle: From FM\nauthor: fm\n---\nBody text"
	out, err := covBRun(t, fake, "upload_file", map[string]interface{}{
		"collection": "c", "filename": "My Notes.md", "content": covBB64(md), "lang": "en",
		"meta": map[string]interface{}{"author": "explicit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "Add").(*MCPAddRequest)
	if strings.Contains(r.ContentMD, "title: From FM") || !strings.Contains(r.ContentMD, "Body text") {
		t.Errorf("frontmatter not stripped: %q", r.ContentMD)
	}
	if r.Meta["author"][0] != "explicit" || r.Meta["title"][0] != "From FM" {
		t.Errorf("meta merge = %v", r.Meta)
	}
	if want := deriveKeyFromFilename("My Notes.md"); r.Key != want || covBUnmarshal(t, out)["key"] != want {
		t.Errorf("key = %q, want %q", r.Key, want)
	}

	// Without explicit meta the frontmatter becomes the meta outright.
	if _, err := covBRun(t, fake, "upload_file", map[string]interface{}{
		"collection": "c", "filename": "b.md", "content": covBB64(md), "lang": "en",
	}); err != nil {
		t.Fatal(err)
	}
	if r := fake.covBCall(t, "Add").(*MCPAddRequest); r.Meta["author"][0] != "fm" {
		t.Errorf("frontmatter meta = %v", r.Meta)
	}
}
