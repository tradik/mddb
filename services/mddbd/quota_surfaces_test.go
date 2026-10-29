package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	json "mddb/internal/jsonx"
	pb "mddb/proto"
)

// GO-043: one gate, every surface. These tests write past a quota through
// REST, gRPC, MCP, GraphQL, batch, the extreme batch processor and ingest,
// and require the same refusal everywhere — and that nothing was stored.

func TestQuotaRefusesNewDocumentsOnEverySurface(t *testing.T) {
	ctx := context.Background()
	batch := []*pb.BatchDocument{{Key: "n", Lang: "en", ContentMd: "new"}}
	cases := []struct {
		name  string
		write func(t *testing.T, gs *GRPCServer, s *Server)
	}{
		{"REST add", func(t *testing.T, _ *GRPCServer, s *Server) {
			rec := doRequest(t, s.handleAdd, map[string]any{"collection": "q", "key": "n", "lang": "en", "contentMd": "new"})
			wantHTTPQuota(t, rec, http.StatusInsufficientStorage, quotaDocuments)
		}},
		{"REST add-batch", func(t *testing.T, _ *GRPCServer, s *Server) {
			rec := doRequest(t, s.handleAddBatch, map[string]any{"collection": "q",
				"documents": []map[string]any{{"key": "n", "lang": "en", "contentMd": "new"}}})
			wantHTTPQuota(t, rec, http.StatusInsufficientStorage, quotaDocuments)
		}},
		{"REST ingest", func(t *testing.T, _ *GRPCServer, s *Server) {
			rec := doRequest(t, s.handleIngest, map[string]any{"collection": "q",
				"documents": []map[string]any{{"key": "n", "lang": "en", "contentMd": "new"}}})
			wantHTTPQuota(t, rec, http.StatusInsufficientStorage, quotaDocuments)
		}},
		{"gRPC Add", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.Add(ctx, &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"})
			wantGRPCQuota(t, err)
		}},
		{"gRPC AddBatch", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.AddBatch(ctx, &pb.AddBatchRequest{Collection: "q", Documents: batch})
			wantGRPCQuota(t, err)
		}},
		{"gRPC AddBatch extreme", func(t *testing.T, gs *GRPCServer, s *Server) {
			s.UseExtreme, s.finalBatchProcessor = true, NewFinalBatchProcessor(s, 2)
			_, err := gs.AddBatch(ctx, &pb.AddBatchRequest{Collection: "q", Documents: batch})
			wantGRPCQuota(t, err)
		}},
		{"REST add-batch extreme", func(t *testing.T, _ *GRPCServer, s *Server) {
			s.UseExtreme, s.finalBatchProcessor = true, NewFinalBatchProcessor(s, 2)
			rec := doRequest(t, s.handleAddBatch, map[string]any{"collection": "q",
				"documents": []map[string]any{{"key": "n", "lang": "en", "contentMd": "new"}}})
			wantHTTPQuota(t, rec, http.StatusInsufficientStorage, quotaDocuments)
		}},
		{"gRPC Ingest", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.Ingest(ctx, &pb.IngestRequest{Collection: "q",
				Documents: []*pb.IngestDocument{{Key: "n", Lang: "en", ContentMd: "new"}}})
			wantGRPCQuota(t, err)
		}},
		{"MCP add_document", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantMCPQuota(t, mcpCall(s, "add_document", map[string]any{"collection": "q", "key": "n", "lang": "en", "content_md": "new"}),
				http.StatusInsufficientStorage)
		}},
		{"MCP add_documents_batch", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantMCPQuota(t, mcpCall(s, "add_documents_batch", map[string]any{"collection": "q",
				"documents": []any{map[string]any{"key": "n", "lang": "en", "content_md": "new"}}}), http.StatusInsufficientStorage)
		}},
		{"MCP ingest_documents", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantMCPQuota(t, mcpCall(s, "ingest_documents", map[string]any{"collection": "q",
				"documents": []any{map[string]any{"key": "n", "lang": "en", "content_md": "new"}}}), http.StatusInsufficientStorage)
		}},
		{"GraphQL addDocument", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantGraphQLQuota(t, graphQLPost(t, s,
				`mutation { addDocument(input:{collection:"q", key:"n", lang:"en", contentMd:"new"}) { key } }`),
				http.StatusInsufficientStorage)
		}},
		{"GraphQL addBatch", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantGraphQLQuota(t, graphQLPost(t, s,
				`mutation { addBatch(collection:"q", documents:[{key:"n", lang:"en", contentMd:"new"}]) { added } }`),
				http.StatusInsufficientStorage)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs, s := quotaTestServer(t)
			setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 2})
			tc.write(t, gs, s)
			if n := quotaDocCount(t, s, "q"); n != 2 {
				t.Fatalf("%d documents stored, want 2", n)
			}
		})
	}
}

func TestQuotaRefusesGrowingUpdatesOnEverySurface(t *testing.T) {
	ctx := context.Background()
	long := "a much longer replacement body than the stored one"
	cases := []struct {
		name  string
		write func(t *testing.T, gs *GRPCServer, s *Server)
	}{
		{"REST update", func(t *testing.T, _ *GRPCServer, s *Server) {
			body, _ := json.Marshal(map[string]any{"collection": "q", "key": "a", "lang": "en", "contentMd": long})
			rec := httptest.NewRecorder()
			s.handleUpdate(rec, httptest.NewRequest(http.MethodPatch, "/v1/update", bytes.NewReader(body)))
			wantHTTPQuota(t, rec, http.StatusInsufficientStorage, quotaBytes)
		}},
		{"gRPC UpdateDocument", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.UpdateDocument(ctx, &pb.UpdateDocumentRequest{Collection: "q", Key: "a", Lang: "en",
				UpdateContent: true, ContentMd: long})
			wantGRPCQuota(t, err)
		}},
		{"gRPC UpdateBatch", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.UpdateBatch(ctx, &pb.UpdateBatchRequest{Collection: "q",
				Documents: []*pb.UpdateDocument{{Key: "a", Lang: "en", ContentMd: long}}})
			wantGRPCQuota(t, err)
		}},
		{"MCP update_document", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantMCPQuota(t, mcpCall(s, "update_document", map[string]any{"collection": "q", "key": "a", "lang": "en", "content_md": long}),
				http.StatusInsufficientStorage)
		}},
		{"MCP client UpdateBatch", func(t *testing.T, _ *GRPCServer, s *Server) {
			_, err := NewDirectClient(s).UpdateBatch(ctx, &MCPUpdateBatchRequest{Collection: "q",
				Documents: []MCPUpdateDocument{{Key: "a", Lang: "en", ContentMD: long}}})
			if qe := asQuotaError(err); qe == nil || qe.Resource != quotaBytes {
				t.Fatalf("err = %v, want a bytes quota error", err)
			}
		}},
		{"GraphQL updateDocument", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantGraphQLQuota(t, graphQLPost(t, s,
				`mutation { updateDocument(input:{collection:"q", key:"a", lang:"en", contentMd:"`+long+`"}) { key } }`),
				http.StatusInsufficientStorage)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs, s := quotaTestServer(t)
			used := s.collectionUsage("q")
			setTestQuota(t, s, "q", CollectionQuota{MaxBytes: used.bytes})
			tc.write(t, gs, s)
			if after := s.collectionUsage("q"); after != used {
				t.Fatalf("usage changed from %+v to %+v", used, after)
			}
		})
	}
}

func TestQuotaAllowsWritesThatDoNotGrow(t *testing.T) {
	gs, s := quotaTestServer(t)
	used := s.collectionUsage("q")
	setTestQuota(t, s, "q", CollectionQuota{MaxDocuments: 1, MaxBytes: 1})
	// Over both limits already (they were lowered), yet a smaller body is a
	// write the collection can always take.
	if _, err := gs.UpdateDocument(context.Background(), &pb.UpdateDocumentRequest{Collection: "q", Key: "a", Lang: "en",
		UpdateContent: true, ContentMd: "x"}); err != nil {
		t.Fatalf("shrinking update refused: %v", err)
	}
	if after := s.collectionUsage("q"); after.docs != 2 || after.bytes >= used.bytes {
		t.Fatalf("usage %+v, want 2 docs and fewer than %d bytes", after, used.bytes)
	}
}

func TestNoQuotaIsTodaysBehaviour(t *testing.T) {
	gs, s := quotaTestServer(t)
	for i, key := range []string{"c", "d", "e"} {
		if _, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "q", Key: key, Lang: "en", ContentMd: "more"}); err != nil {
			t.Fatalf("write %d refused without a quota: %v", i, err)
		}
	}
	if n := quotaDocCount(t, s, "q"); n != 5 {
		t.Fatalf("%d documents, want 5", n)
	}
}
