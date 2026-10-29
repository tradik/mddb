package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// GO-046 on every surface: upload, MCP and ingest store an email's
// attachments as documents of their own, report how well each file
// converted, and a scan is findable by its score.

func TestUploadReportsConfidenceAndParts(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	fixtures := convertFixtures()

	got := decodeUpload(t, uploadFile(t, s, "Q3 figures.eml", fixtures["sample.eml"], map[string]string{"collection": "mail", "lang": "en"}))
	if got.Key != "q3-figures" || got.Format != "eml" || got.ConversionConfidence == nil || *got.ConversionConfidence != 1 ||
		got.Doc.Meta["subject"][0] != "Quarterly figures — Q3" || len(got.Parts) != 1 {
		t.Fatalf("eml upload: %+v", got)
	}
	part := got.Parts[0]
	if part.Key != "q3-figures/attachments/q3.csv" || part.Format != "csv" || part.Doc.Meta["parent"][0] != "q3-figures" ||
		part.Doc.Meta["upload_filename"][0] != "Q3 figures.eml" || !strings.Contains(part.Doc.ContentMD, "| North | 1200 |") {
		t.Fatalf("attachment: %+v", part)
	}

	scan := decodeUpload(t, uploadFile(t, s, "scan.pdf", fixtures["scanned.pdf"], map[string]string{"collection": "mail", "lang": "en"}))
	if scan.ConversionConfidence == nil || *scan.ConversionConfidence != 0 || len(scan.ConversionWarnings) == 0 ||
		scan.Doc.Meta["conversionConfidence"][0] != "0.00" {
		t.Fatalf("scan: %+v", scan)
	}
	md := decodeUpload(t, uploadFile(t, s, "plain.md", []byte("# x"), map[string]string{"collection": "mail", "lang": "en"}))
	if md.ConversionConfidence != nil || md.Doc.Meta["conversionConfidence"] != nil {
		t.Fatalf("markdown is not scored: %+v", md)
	}

	// A part the schema refuses, and one the store refuses, fail the upload
	// by name after the message itself is stored.
	if err := s.SchemaManager.Set("strict", `{"required":["from"]}`); err != nil {
		t.Fatal(err)
	}
	rec := uploadFile(t, s, "m.eml", fixtures["sample.eml"], map[string]string{"collection": "strict", "lang": "en"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "m/attachments/q3.csv") {
		t.Fatalf("schema on a part: %d %s", rec.Code, rec.Body.String())
	}
	bad := mailWith("Content-Type: text/plain\r\nContent-Disposition: attachment; filename=\"bad.txt\"", "ok\xff\xfe")
	rec = uploadFile(t, s, "bad.eml", bad, map[string]string{"collection": "mail", "lang": "en"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bad/attachments/bad.txt") {
		t.Fatalf("store on a part: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMCPUploadReportsConfidenceAndParts(t *testing.T) {
	fake := covBNewFake()
	out, err := covBRun(t, fake, "upload_file", map[string]interface{}{
		"collection": "c", "filename": "inbox.mbox", "content": covBB64(fixtureMbox), "lang": "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := covBUnmarshal(t, out)
	if res["conversionConfidence"] != 1.0 || len(res["parts"].([]interface{})) != 2 {
		t.Fatalf("result: %v", res)
	}
	fake = covBNewFake()
	fake.err = errCovBClient
	if _, err := covBRun(t, fake, "upload_file", map[string]interface{}{
		"collection": "c", "filename": "inbox.mbox", "content": covBB64(fixtureMbox), "lang": "en",
	}); err == nil {
		t.Fatal("a store error is returned")
	}
}

func TestIngestReportsConversionsAndScansAreFindable(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	fixtures := convertFixtures()

	resp, err := s.ingestDocuments(context.Background(), "code", []IngestDocumentHTTP{
		{Key: "scan.pdf", Lang: "en", Content: fixtures["scanned.pdf"], TTL: 3600},
		{Key: "report.pdf", Lang: "en", Content: fixtures["sample.pdf"]},
		{Key: "q3.eml", Lang: "en", Content: fixtures["sample.eml"], Meta: map[string][]string{"path": {"mail/q3.eml"}}},
	}, IngestOptionsHTTP{SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true})
	if err != nil || resp.Added != 4 || len(resp.Conversions) != 4 {
		t.Fatalf("ingest: %+v %v", resp, err)
	}
	if c := resp.Conversions[0]; c.Key != "scan.pdf" || c.Confidence != 0 || len(c.Warnings) == 0 {
		t.Errorf("scan: %+v", c)
	}
	if c := resp.Conversions[3]; c.Key != "q3.eml/attachments/q3.csv" || c.Format != "csv" {
		t.Errorf("attachment: %+v", c)
	}
	att, err := s.loadDocByRef("code", "q3.eml/attachments/q3.csv", "en")
	if err != nil || att.Meta["parent"][0] != "q3.eml" || att.Meta["sourceFormat"][0] != "csv" || att.Meta["path"][0] != "mail/q3.eml" {
		t.Fatalf("attachment doc: %+v %v", att, err)
	}
	if out := protoFromIngestResponse(resp); len(out.Conversions) != 4 || out.Conversions[0].ConversionConfidence != 0 {
		t.Errorf("proto: %+v", out.Conversions)
	}

	// SRCH-017: the numeric filter finds the scan, and only the scan.
	ids, active := s.allowedDocIDs("code", nil, parseFilter(t, `{"conversionConfidence":{"lt":0.5}}`))
	if keys := filterKeys(t, s, ids); !active || strings.Join(keys, ",") != "scan.pdf" {
		t.Fatalf("low-confidence filter: %v", keys)
	}

	// SRCH-010: the advisor counts it and says how to find it.
	rec, err := s.RecommendSearch("code")
	if err != nil {
		t.Fatal(err)
	}
	p := rec.Profile
	if p.ConvertedDocuments != 4 || p.LowConfidenceDocuments != 1 || p.LowConfidenceRatio != 0.25 {
		t.Fatalf("profile: %+v", p)
	}
	if w := strings.Join(rec.Warnings, " "); !strings.Contains(w, "1 of 4 sampled documents (25%) converted with conversionConfidence below 0.5") ||
		!strings.Contains(w, `{"conversionConfidence": {"lt": 0.5}}`) {
		t.Fatalf("warnings: %v", rec.Warnings)
	}

	// The direct MCP client passes the conversions on.
	out, err := NewDirectClient(s).Ingest(context.Background(), &MCPIngestRequest{Collection: "code", Documents: []MCPIngestDocument{
		{Key: "again.pdf", Lang: "en", Content: fixtures["scanned.pdf"]},
	}, Options: MCPIngestOptions{SkipEmbeddings: true, SkipFTS: true, SkipWebhooks: true}})
	if err != nil || len(out.Conversions) != 1 {
		t.Fatalf("direct client: %+v %v", out, err)
	}
}

func TestAdvisorCountsConversions(t *testing.T) {
	p := &CollectionProfile{}
	p.countConversion(map[string][]string{})
	p.countConversion(map[string][]string{"conversionConfidence": {"0.9"}})
	p.countConversion(map[string][]string{"conversionConfidence": {"0.1"}})
	p.countConversion(map[string][]string{"conversionConfidence": {"not a number"}})
	if p.ConvertedDocuments != 3 || p.LowConfidenceDocuments != 1 {
		t.Fatalf("%+v", p)
	}
	r := &SearchRecommendation{}
	adviseConversions(r, &CollectionProfile{})
	if len(r.Warnings) != 0 {
		t.Fatal("no low-confidence documents, no warning")
	}
}
