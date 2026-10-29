package main

import (
	"context"
	"strings"
	"testing"

	"mddb/internal/fts"
	"mddb/internal/storage"
)

// covC: metadata, schema, synonym and stop-word tools of the direct client.

// A server built without a subsystem must answer with the reason, not panic.
func TestCovCMetadataToolsReportAMissingSubsystem(t *testing.T) {
	c := NewDirectClient(&Server{})
	ctx := context.Background()
	errs := map[string]error{}
	_, errs["RegisterWebhook"] = c.RegisterWebhook(ctx, &MCPRegisterWebhookRequest{URL: "https://x", Events: []string{"doc.added"}})
	_, errs["ListWebhooks"] = c.ListWebhooks(ctx)
	errs["DeleteWebhook"] = c.DeleteWebhook(ctx, &MCPDeleteWebhookRequest{ID: "x"})
	errs["SetSchema"] = c.SetSchema(ctx, &MCPSetSchemaRequest{Collection: "c", Schema: "{}"})
	_, errs["GetSchema"] = c.GetSchema(ctx, "c")
	errs["DeleteSchema"] = c.DeleteSchema(ctx, "c")
	_, errs["ListSchemas"] = c.ListSchemas(ctx)
	_, errs["ListSynonyms"] = c.ListSynonyms(ctx, "c")
	errs["SetSynonym"] = c.SetSynonym(ctx, "c", "car", []string{"auto"})
	errs["DeleteSynonym"] = c.DeleteSynonym(ctx, "c", "car")
	_, errs["ListStopWords"] = c.ListStopWords(ctx, "c")
	errs["AddStopWords"] = c.AddStopWords(ctx, "c", []string{"foo"})
	errs["DeleteStopWords"] = c.DeleteStopWords(ctx, "c", []string{"foo"})
	for name, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "not initialized") {
			t.Errorf("%s: err = %v, want a not-initialized error", name, err)
		}
	}
	// Validation needs no schema manager: everything is valid without one.
	v, err := c.ValidateDocument(ctx, &MCPValidateRequest{Collection: "c", Meta: map[string][]string{"a": {"b"}}})
	if err != nil || !v.Valid {
		t.Errorf("validate without a schema manager = %+v, %v", v, err)
	}
}

func TestCovCRegisterWebhookPassesOnTheManagersRefusal(t *testing.T) {
	c, _ := covCServer(t)
	if wh, err := c.RegisterWebhook(context.Background(), &MCPRegisterWebhookRequest{URL: "https://example.com/hook"}); err == nil {
		t.Fatalf("a webhook without events was registered: %+v", wh)
	}
}

func TestCovCValidateDocumentListsSchemaViolations(t *testing.T) {
	c, s := covCServer(t)
	if err := s.SchemaManager.Set("v", `{"required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	resp, err := c.ValidateDocument(context.Background(), &MCPValidateRequest{Collection: "v", Meta: map[string][]string{"x": {"y"}}})
	if err != nil || resp.Valid || len(resp.Errors) == 0 || !strings.Contains(strings.Join(resp.Errors, " "), "author") {
		t.Fatalf("validate = %+v, %v", resp, err)
	}
}

func TestCovCGetDocumentMeta(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	addTestDoc(t, s, "m", "ok", "en", "body", map[string][]string{"author": {"ann"}})
	gone := addTestDoc(t, s, "m", "gone", "en", "body", nil)
	covCDel(t, s, "docs", storage.DocKey("m", gone.ID))
	bad := addTestDoc(t, s, "m", "bad", "en", "body", nil)
	covCPut(t, s, "docs", storage.DocKey("m", bad.ID), covCGarbage)

	got, err := c.GetDocumentMeta(ctx, &MCPGetDocMetaRequest{Collection: "m", Key: "ok", Lang: "en"})
	if err != nil || got.Key != "ok" || got.Meta["author"][0] != "ann" || got.AddedAt == 0 {
		t.Fatalf("GetDocumentMeta = %+v, %v", got, err)
	}
	if _, err := c.GetDocumentMeta(ctx, &MCPGetDocMetaRequest{Collection: "m", Key: "ok"}); err == nil {
		t.Error("a request without lang was accepted")
	}
	for _, key := range []string{"never", "gone"} {
		if _, err := c.GetDocumentMeta(ctx, &MCPGetDocMetaRequest{Collection: "m", Key: key, Lang: "en"}); err == nil || err.Error() != "not found" {
			t.Errorf("%s: err = %v, want not found", key, err)
		}
	}
	if _, err := c.GetDocumentMeta(ctx, &MCPGetDocMetaRequest{Collection: "m", Key: "bad", Lang: "en"}); err == nil {
		t.Error("a damaged document returned metadata")
	}
}

func TestCovCClassify(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	if _, err := c.Classify(ctx, &MCPClassifyRequest{Text: "hello", Labels: []string{"a"}}); err == nil {
		t.Fatal("classify without an embedding provider succeeded")
	}
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	resp, err := c.Classify(ctx, &MCPClassifyRequest{Text: "hello", Labels: []string{"news", "sport"}, TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 || resp.Model != "test-model" || resp.Dimensions != 3 {
		t.Fatalf("classify = %+v", resp)
	}
}

func TestCovCSynonymsRoundTrip(t *testing.T) {
	c, s := covCServer(t)
	s.SynonymManager = fts.NewSynonymManager(s.DB)
	if err := s.SynonymManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.SetSynonym(ctx, "syn", "car", []string{"auto"}); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListSynonyms(ctx, "syn")
	if err != nil || list.Total != 1 || list.Entries[0].Term != "car" || list.Entries[0].Synonyms[0] != "auto" {
		t.Fatalf("ListSynonyms = %+v, %v", list, err)
	}
	if err := c.DeleteSynonym(ctx, "syn", "car"); err != nil {
		t.Fatal(err)
	}
	if list, _ = c.ListSynonyms(ctx, "syn"); list.Total != 0 {
		t.Errorf("synonym survived its deletion: %+v", list)
	}
}

func TestCovCStopWordsRoundTrip(t *testing.T) {
	c, s := covCServer(t)
	s.StopWordManager = fts.NewStopWordManager(s.DB)
	if err := s.StopWordManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.AddStopWords(ctx, "sw", []string{"zork"}); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListStopWords(ctx, "sw")
	if err != nil || list.Custom != 1 || list.Defaults == 0 || list.Total != list.Custom+list.Defaults {
		t.Fatalf("ListStopWords = %+v, %v", list, err)
	}
	if err := c.DeleteStopWords(ctx, "sw", []string{"zork"}); err != nil {
		t.Fatal(err)
	}
	// A default stop word cannot be removed, and the reason comes back.
	err = c.DeleteStopWords(ctx, "sw", []string{"the"})
	if err == nil || !strings.Contains(err.Error(), "some deletions failed") {
		t.Fatalf("deleting a default stop word: %v", err)
	}
	if list, _ = c.ListStopWords(ctx, "sw"); list.Custom != 0 {
		t.Errorf("custom stop word survived its deletion: %+v", list)
	}
}

func TestCovCGetMetaKeysListsEachValueOnce(t *testing.T) {
	c, s := covCServer(t)
	addTestDoc(t, s, "mk", "a", "en", "x", map[string][]string{"tag": {"go", "db"}})
	addTestDoc(t, s, "mk", "b", "en", "x", map[string][]string{"tag": {"go"}})
	// A malformed index key (no value part) is skipped, not reported.
	covCPut(t, s, "idxmeta", []byte("meta|mk|orphan"), nil)

	resp, err := c.GetMetaKeys(context.Background(), "mk")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(resp.Meta["tag"], ","); got != "db,go" {
		t.Errorf("tag values = %s, want db,go", got)
	}
	if _, ok := resp.Meta["orphan"]; ok {
		t.Error("a malformed index key was reported as a meta key")
	}
	// A database without the index answers with nothing, not an error.
	bare, err := covCBareClient(t).GetMetaKeys(context.Background(), "mk")
	if err != nil || len(bare.Meta) != 0 {
		t.Errorf("bare database = %+v, %v", bare, err)
	}
}

func TestCovCGetChecksumCountsDocuments(t *testing.T) {
	c, s := covCServer(t)
	addTestDoc(t, s, "ck", "a", "en", "x", nil)
	first, err := c.GetChecksum(context.Background(), "ck")
	if err != nil || first.DocumentCount != 1 || first.Checksum == "" || first.Collection != "ck" {
		t.Fatalf("checksum = %+v, %v", first, err)
	}
	addTestDoc(t, s, "ck", "b", "en", "y", nil)
	second, _ := c.GetChecksum(context.Background(), "ck")
	if second.DocumentCount != 2 || second.Checksum == first.Checksum {
		t.Errorf("checksum did not change with the collection: %+v then %+v", first, second)
	}
}
