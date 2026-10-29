package main

import (
	"context"
	"testing"

	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// One metadata search behind REST, MCP and gRPC: the same documents, the
// same order, the same page — and no expired documents on any of them.
func TestSearchDocuments_OneImplementation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	for _, k := range []string{"c", "a", "b"} {
		addTestDoc(t, s, "col", k, "en", k, map[string][]string{"tag": {"x"}})
	}
	old := addTestDoc(t, s, "col", "old", "en", "o", map[string][]string{"tag": {"x"}})
	old.ExpiresAt = 1
	buf, _ := marshalDoc(&old)
	_ = s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("docs")).Put(storage.DocKey("col", old.ID), buf) })

	for _, f := range []MetaFilter{nil, {"tag": {In: []string{"x"}, hasIn: true}}} {
		page, total, err := s.searchDocuments("col", f, "key", true, 1, 1)
		if err != nil || total != 3 || len(page) != 1 || page[0].Key != "b" {
			t.Fatalf("filter %v: %v %d %+v", f, err, total, page)
		}
	}
	if page, total, _ := s.searchDocuments("col", nil, "", false, 5, 10); len(page) != 0 || total != 3 {
		t.Fatal("offset past the end")
	}
	// gRPC used to return the expired document.
	resp, err := (&GRPCServer{server: s}).Search(context.Background(), &proto.SearchRequest{Collection: "col", Sort: "key", Asc: true})
	if err != nil || resp.Total != 3 || resp.Documents[0].Key != "a" {
		t.Fatalf("grpc: %v %+v", err, resp)
	}
	// A record that does not decode is an error, not a silent gap.
	_ = s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("docs")).Put(storage.DocKey("col", "zz"), []byte("{")) })
	if _, _, err := s.searchDocuments("col", nil, "", false, 0, 10); err == nil {
		t.Fatal("corrupt record")
	}
	if _, _, err := s.searchDocuments("col", MetaFilter{"tag": {In: []string{"x"}, hasIn: true}}, "", false, 0, 10); err != nil {
		t.Fatal("the filter does not reach the corrupt record")
	}
}
