package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mddb/internal/metrics"
	proto "mddb/proto"
)

// Coverage of POST /v1/ingest and its shared core, ingestDocuments.

func ingestCovServer(t *testing.T) *Server {
	t.Helper()
	s := covGServer(t)
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	return s
}

func ingestPost(s *Server, method, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handleIngest(rec, httptest.NewRequest(method, "/v1/ingest", strings.NewReader(body)))
	return rec
}

func TestIngestCov_HandlerRefusals(t *testing.T) {
	s := ingestCovServer(t)
	if rec := ingestPost(s, http.MethodGet, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	for name, body := range map[string]string{
		"not json":      "{",
		"no collection": `{"documents":[]}`,
		"bad profile":   `{"collection":"c","documents":[{"key":"a","lang":"en","contentMd":"x"}],"options":{"profile":"turbo"}}`,
	} {
		if rec := ingestPost(s, http.MethodPost, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	rec := ingestPost(s, http.MethodPost, `{"collection":"c","documents":[]}`)
	var resp IngestResponseHTTP
	covGDecode(t, rec, &resp)
	if rec.Code != http.StatusOK || resp.Collection != "c" || resp.Added != 0 {
		t.Fatalf("empty ingest: %d %+v", rec.Code, resp)
	}
}

func TestIngestCov_PreProcessingAndHooks(t *testing.T) {
	s := ingestCovServer(t)
	body := `{"collection":"scraped","options":{"autoConfigureCollection":true,"skipDuplicates":true},"documents":[
		{"url":"https://example.com/docs/page","lang":"en","contentMd":"---\ntitle: Page\ntag: a\n---\nBody text","extractFrontmatter":true,
		 "meta":{"tag":["override"]},"scrapedAt":1700000000,"scraper":"crawler","ttl":3600},
		{"lang":"en","contentMd":"no key or url"},
		{"key":"nolang","contentMd":"x"}
	]}`
	rec := ingestPost(s, http.MethodPost, body)
	var resp IngestResponseHTTP
	covGDecode(t, rec, &resp)
	if rec.Code != http.StatusOK || resp.Added != 1 || resp.Failed != 2 || len(resp.Errors) != 2 {
		t.Fatalf("first ingest: %d %+v", rec.Code, resp)
	}
	if cfg, ok := s.CollectionManager.Get("scraped"); !ok || cfg.Type != "scraping" {
		t.Fatalf("collection not auto-configured: %+v", cfg)
	}
	doc, err := NewDirectClient(s).Get(context.Background(), &MCPGetRequest{Collection: "scraped", Key: deriveKeyFromURL("https://example.com/docs/page"), Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.ContentMD != "Body text" || doc.Meta["tag"][0] != "override" || doc.Meta["title"][0] != "Page" ||
		doc.Meta["source_url"][0] == "" || doc.Meta["scraped_at"][0] != "1700000000" || doc.Meta["scraper"][0] != "crawler" {
		t.Fatalf("pre-processing: %+v", doc)
	}

	// The same content again is skipped as a duplicate; nothing new to
	// write ends early with the skip counted.
	rec = ingestPost(s, http.MethodPost, body)
	resp = IngestResponseHTTP{}
	covGDecode(t, rec, &resp)
	if resp.Skipped != 1 || resp.Added != 0 {
		t.Fatalf("second ingest: %+v", resp)
	}
}

func TestIngestCov_ContentHashMapAndTTLs(t *testing.T) {
	s := ingestCovServer(t)
	addTestDoc(t, s, "h", "k", "en", "hello", nil)
	hashes := s.buildContentHashMap("h", []*proto.BatchDocument{
		{Key: "k", Lang: "en"}, {Key: "absent", Lang: "en"},
	})
	if len(hashes) != 1 {
		t.Fatalf("hashes: %v", hashes)
	}
	if got := s.buildContentHashMap("never-written", []*proto.BatchDocument{{Key: "k", Lang: "en"}}); len(got) != 0 {
		t.Fatalf("hashes of an empty collection: %v", got)
	}

	// TTLs go to the document they were asked for, by ID: failed, missing
	// and zero-TTL entries are passed over; no manager, nothing.
	id := genID("h", "k", "en")
	ttls := map[string]int64{id: 60, "other": 0}
	processed := []*ProcessedDoc{nil, {DocID: "failed", Error: errors.New("no")}, {DocID: "other"}, {DocID: id}}
	s.applyIngestTTLs("h", ttls, processed)
	s.TTLManager = nil
	s.applyIngestTTLs("h", ttls, processed)
}

func TestIngestCov_FromProtoAndBack(t *testing.T) {
	s := ingestCovServer(t)
	resp, err := s.ingestDocumentsFromProto(context.Background(), &proto.IngestRequest{
		Collection: "p",
		Documents: []*proto.IngestDocument{{Key: "a", Lang: "en", ContentMd: "alpha", Ttl: 60,
			Meta: map[string]*proto.MetaValues{"tag": {Values: []string{"x"}}}},
			{Key: "bad-utf8", Lang: "en", ContentMd: "caf\xe9"}}, // GO-036: sanitised, and counted
		Options: &proto.IngestOptions{SkipEmbeddings: true, SkipWebhooks: true, SaveRevision: true},
	})
	if err != nil || resp.Added != 2 || resp.Sanitized != 1 {
		t.Fatalf("proto ingest: %+v %v", resp, err)
	}
	out := protoFromIngestResponse(&IngestResponseHTTP{Added: 1, Errors: []string{"e"}, Collection: "p", DurationMs: 3})
	if out.Added != 1 || len(out.Errors) != 1 || out.DurationMs != 3 {
		t.Fatalf("proto response: %+v", out)
	}
	if protoFromIngestResponse(&IngestResponseHTTP{}).Errors != nil {
		t.Fatal("nil errors became a slice")
	}

	// A batch the quota refuses fails the whole ingest.
	setTestQuota(t, s, "p", CollectionQuota{MaxDocuments: 1})
	if _, err := s.ingestDocuments(context.Background(), "p", []IngestDocumentHTTP{{Key: "b", Lang: "en", ContentMD: "beta"}}, IngestOptionsHTTP{}); err == nil {
		t.Fatal("quota did not refuse")
	}
	rec := ingestPost(s, http.MethodPost, `{"collection":"p","documents":[{"key":"c","lang":"en","contentMd":"gamma"}]}`)
	if rec.Code != http.StatusInsufficientStorage {
		t.Fatalf("refused ingest over HTTP: %d", rec.Code)
	}
}
