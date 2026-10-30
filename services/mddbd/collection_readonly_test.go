package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	json "mddb/internal/jsonx"
	pb "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AGT-005: a read-only collection refuses writes on every surface, through
// one gate, and nothing is stored.

// readOnlyTestServer is quotaTestServer's two documents in "q", with "q"
// then marked read-only as a corpus pack install marks it.
func readOnlyTestServer(t *testing.T) (*GRPCServer, *Server) {
	t.Helper()
	gs, s := quotaTestServer(t)
	s.wireRouters() // as main does: the index, vector, geo and TTL writes go through the router too
	if err := s.CollectionManager.put("q", &CollectionConfig{ReadOnly: true, Corpus: &CorpusInfo{Name: "q"}}); err != nil {
		t.Fatal(err)
	}
	return gs, s
}

func wantHTTPReadOnly(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != "READ_ONLY" || body["collection"] != "q" {
		t.Fatalf("body = %s, want a READ_ONLY refusal for q", rec.Body.String())
	}
}

func wantGRPCReadOnly(t *testing.T, err error) {
	t.Helper()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v (%v), want PermissionDenied", status.Code(err), err)
	}
}

func wantReadOnly(t *testing.T, err error) {
	t.Helper()
	if re := asReadOnlyError(err); re == nil || re.Collection != "q" {
		t.Fatalf("err = %v, want a read-only refusal for q", err)
	}
}

func TestReadOnlyCollectionRefusesWritesOnEverySurface(t *testing.T) {
	ctx := context.Background()
	add := map[string]any{"collection": "q", "key": "n", "lang": "en", "contentMd": "new"}
	docs := []map[string]any{{"key": "n", "lang": "en", "contentMd": "new"}}
	cases := []struct {
		name  string
		write func(t *testing.T, gs *GRPCServer, s *Server)
	}{
		{"REST add", func(t *testing.T, _ *GRPCServer, s *Server) { wantHTTPReadOnly(t, doRequest(t, s.handleAdd, add)) }},
		{"REST add-batch", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantHTTPReadOnly(t, doRequest(t, s.handleAddBatch, map[string]any{"collection": "q", "documents": docs}))
		}},
		{"REST ingest", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantHTTPReadOnly(t, doRequest(t, s.handleIngest, map[string]any{"collection": "q", "documents": docs}))
		}},
		{"REST update", func(t *testing.T, _ *GRPCServer, s *Server) {
			body, _ := json.Marshal(map[string]any{"collection": "q", "key": "a", "lang": "en", "contentMd": "changed"})
			rec := httptest.NewRecorder()
			s.handleUpdate(rec, httptest.NewRequest(http.MethodPatch, "/v1/update", bytes.NewReader(body)))
			wantHTTPReadOnly(t, rec)
		}},
		{"REST delete-collection", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantHTTPReadOnly(t, doRequest(t, s.handleDeleteCollection, map[string]any{"collection": "q"}))
		}},
		{"gRPC Add", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.Add(ctx, &pb.AddRequest{Collection: "q", Key: "n", Lang: "en", ContentMd: "new"})
			wantGRPCReadOnly(t, err)
		}},
		{"gRPC AddBatch", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.AddBatch(ctx, &pb.AddBatchRequest{Collection: "q", Documents: []*pb.BatchDocument{{Key: "n", Lang: "en", ContentMd: "new"}}})
			wantGRPCReadOnly(t, err)
		}},
		{"gRPC AddBatch extreme", func(t *testing.T, gs *GRPCServer, s *Server) {
			s.UseExtreme, s.finalBatchProcessor = true, NewFinalBatchProcessor(s, 2)
			_, err := gs.AddBatch(ctx, &pb.AddBatchRequest{Collection: "q", Documents: []*pb.BatchDocument{{Key: "n", Lang: "en", ContentMd: "new"}}})
			wantGRPCReadOnly(t, err)
		}},
		{"gRPC UpdateBatch", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.UpdateBatch(ctx, &pb.UpdateBatchRequest{Collection: "q",
				Documents: []*pb.UpdateDocument{{Key: "a", Lang: "en", ContentMd: "changed"}}})
			wantGRPCReadOnly(t, err)
		}},
		{"gRPC UpdateDocument", func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.UpdateDocument(ctx, &pb.UpdateDocumentRequest{Collection: "q", Key: "a", Lang: "en", UpdateContent: true, ContentMd: "x"})
			if err == nil {
				t.Fatal("gRPC UpdateDocument wrote to a read-only collection")
			}
		}},
		{"gRPC DeleteCollection", func(t *testing.T, gs *GRPCServer, _ *Server) {
			if _, err := gs.DeleteCollection(ctx, &pb.DeleteCollectionRequest{Collection: "q"}); err == nil {
				t.Fatal("gRPC DeleteCollection deleted a read-only collection")
			}
		}},
		{"MCP add_document", func(t *testing.T, _ *GRPCServer, s *Server) {
			res := mcpCall(s, "add_document", map[string]any{"collection": "q", "key": "n", "lang": "en", "content_md": "new"})
			detail, _ := res["structuredContent"].(map[string]any)
			if res["isError"] != true || detail["code"] != "READ_ONLY" || detail["status"] != http.StatusForbidden {
				t.Fatalf("MCP result = %v, want a READ_ONLY error", res)
			}
		}},
		{"GraphQL addDocument", func(t *testing.T, _ *GRPCServer, s *Server) {
			out := graphQLPost(t, s, `mutation { addDocument(input:{collection:"q", key:"n", lang:"en", contentMd:"new"}) { key } }`)
			errs, _ := out["errors"].([]any)
			if len(errs) == 0 {
				t.Fatalf("no GraphQL error: %v", out)
			}
			if ext, _ := errs[0].(map[string]any)["extensions"].(map[string]any); ext["code"] != "READ_ONLY" {
				t.Fatalf("extensions = %v", ext)
			}
		}},
		{"full-text index", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantReadOnly(t, s.FTSIndex.Index("q", "x", "new words"))
		}},
		{"vectors", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantReadOnly(t, s.VectorStore.Put("q", "x", []float32{1, 0, 0}, "m", "h"))
		}},
		{"TTL", func(t *testing.T, _ *GRPCServer, s *Server) {
			wantReadOnly(t, s.TTLManager.Set("q", "x", 1))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs, s := readOnlyTestServer(t)
			tc.write(t, gs, s)
			if n := quotaDocCount(t, s, "q"); n != 2 {
				t.Fatalf("%d documents stored, want 2", n)
			}
		})
	}
}

func TestReadOnlyLeavesReadsAndOtherCollectionsAlone(t *testing.T) {
	gs, _ := readOnlyTestServer(t)
	if _, err := gs.Get(context.Background(), &pb.GetRequest{Collection: "q", Key: "a", Lang: "en"}); err != nil {
		t.Fatalf("a read of the read-only collection failed: %v", err)
	}
	if _, err := gs.Add(context.Background(), &pb.AddRequest{Collection: "other", Key: "n", Lang: "en", ContentMd: "new"}); err != nil {
		t.Fatalf("a write to another collection failed: %v", err)
	}
}

func TestAConfigWriteKeepsReadOnly(t *testing.T) {
	_, s := readOnlyTestServer(t)
	rec := covHConfigRequest(s, http.MethodPut, "/v1/collection-config", `{"collection":"q","description":"changed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := s.CollectionManager.Get("q")
	if !cfg.ReadOnly || cfg.Corpus == nil || cfg.Description != "changed" {
		t.Fatalf("config = %+v, want the description changed and the pack still read-only", cfg)
	}
}

func TestAPacksConfigCannotBeDeleted(t *testing.T) {
	_, s := readOnlyTestServer(t)
	wantHTTPReadOnly(t, covHConfigRequest(s, http.MethodDelete, "/v1/collection-config?collection=q", ""))
	if !s.collectionReadOnly("q") {
		t.Fatal("deleting the config opened the pack to writes")
	}
}

func TestReadOnlyErrorMapping(t *testing.T) {
	err := errors.Join(errors.New("context"), &ReadOnlyError{Collection: "q"})
	if c := status.Code(grpcError(codes.Internal, err)); c != codes.PermissionDenied {
		t.Fatalf("gRPC code %v", c)
	}
	if detail := mcpQuotaContent(err); detail["code"] != "READ_ONLY" {
		t.Fatalf("MCP detail %v", detail)
	}
	rec := httptest.NewRecorder()
	bad(rec, err)
	wantHTTPReadOnly(t, rec)
	if writeReadOnlyError(httptest.NewRecorder(), errors.New("other")) {
		t.Fatal("another error was answered as read-only")
	}
	if (&Server{}).collectionReadOnly("q") {
		t.Fatal("a server without configs has a read-only collection")
	}
}
