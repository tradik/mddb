package main

import (
	"context"
	"errors"
	"testing"

	"mddb/internal/encryption"
	"mddb/internal/storage"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// One set-TTL for REST, MCP and gRPC. The copies wrote the document without
// encryption, and only REST wrote the binlog.
func TestSetDocumentTTL_EncryptsAndIsShared(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	t.Setenv("MDDB_ENCRYPTION_KEY", genKey(t))
	e, _ := encryption.NewEncryptor()
	SetGlobalEncryptor(e)
	defer SetGlobalEncryptor(nil)
	e.SetCollectionEnabled("secrets", true)
	doc := addTestDoc(t, s, "secrets", "k", "en", "top-secret", nil)

	stored := func() []byte {
		var v []byte
		_ = s.DBViewC("secrets", func(tx *bolt.Tx) error {
			v = append([]byte(nil), tx.Bucket([]byte("docs")).Get(storage.DocKey("secrets", doc.ID))...)
			return nil
		})
		return v
	}
	got, err := s.setDocumentTTL("secrets", "k", "en", 60)
	if err != nil || got.ExpiresAt == 0 {
		t.Fatalf("set: %v %+v", err, got)
	}
	if !encryption.IsEncrypted(stored()) {
		t.Fatal("a TTL change stored the document unencrypted")
	}
	// MCP and gRPC run the same code: clearing goes through both.
	if _, err := NewDirectClient(s).SetTTL(context.Background(), &MCPSetTTLRequest{Collection: "secrets", Key: "k", Lang: "en"}); err != nil {
		t.Fatalf("mcp clear: %v", err)
	}
	if cur, _ := s.loadDocByRef("secrets", "k", "en"); cur.ExpiresAt != 0 {
		t.Fatalf("mcp clear kept the expiry: %d", cur.ExpiresAt)
	}
	g := &GRPCServer{server: s}
	if pd, err := g.SetTTL(context.Background(), &proto.SetTTLRequest{Collection: "secrets", Key: "k", Lang: "en", Ttl: 30}); err != nil || pd.ExpiresAt == 0 {
		t.Fatalf("grpc: %v %+v", err, pd)
	}
	if !encryption.IsEncrypted(stored()) {
		t.Fatal("gRPC SetTTL stored the document unencrypted")
	}
	if _, err := g.SetTTL(context.Background(), &proto.SetTTLRequest{Collection: "secrets", Key: "nope", Lang: "en", Ttl: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("grpc missing: %v", err)
	}
	if _, err := s.setDocumentTTL("", "k", "en", 1); err == nil {
		t.Fatal("missing collection")
	}
	var bad invalidInput
	if _, err := s.setDocumentTTL("secrets", "", "en", 1); !errors.As(err, &bad) {
		t.Fatalf("missing key is an input error: %v", err)
	}
	// A corrupt record is reported, not overwritten.
	_ = s.DBUpdateC("secrets", func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey("secrets", doc.ID), []byte("{"))
	})
	if _, err := s.setDocumentTTL("secrets", "k", "en", 1); err == nil {
		t.Fatal("corrupt record")
	}
	if _, err := g.SetTTL(context.Background(), &proto.SetTTLRequest{Collection: "secrets", Key: "k", Lang: "en", Ttl: 1}); status.Code(err) != codes.Internal {
		t.Fatalf("grpc corrupt: %v", err)
	}
}

// gRPC Add used to drop AddRequest.ttl.
func TestGRPCAdd_KeepsTTL(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	g := &GRPCServer{server: s}
	d, err := g.Add(context.Background(), &proto.AddRequest{Collection: "c", Key: "k", Lang: "en", ContentMd: "x", Ttl: 120})
	if err != nil || d.ExpiresAt == 0 {
		t.Fatalf("ttl dropped: %v %+v", err, d)
	}
}

// Metadata values may contain '|'; listing them returned the part before it.
func TestMetaKeys_ValuesWithAPipe(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "c", "a", "en", "x", map[string][]string{"ns": {"a|b"}, "t": {"x"}})
	addTestDoc(t, s, "c", "b", "en", "x", map[string][]string{"ns": {"a|b"}})
	m := s.metaKeys("c")
	if len(m["ns"]) != 1 || m["ns"][0] != "a|b" || m["t"][0] != "x" {
		t.Fatalf("meta keys: %+v", m)
	}
	mcp, _ := NewDirectClient(s).GetMetaKeys(context.Background(), "c")
	if mcp.Meta["ns"][0] != "a|b" {
		t.Fatalf("mcp: %+v", mcp.Meta)
	}
	gr, err := (&GRPCServer{server: s}).GetMetaKeys(context.Background(), &proto.GetMetaKeysRequest{Collection: "c"})
	if err != nil || gr.Meta["ns"].Values[0] != "a|b" {
		t.Fatalf("grpc: %v %+v", err, gr)
	}
	// Entries that do not have the index-key shape are skipped.
	_ = s.DBUpdateC("c", func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("idxmeta"))
		_ = b.Put([]byte("meta|c|nokeysep"), []byte("1"))
		return b.Put([]byte("meta|c|k|v|elsewhere"), []byte("1"))
	})
	if m = s.metaKeys("c"); len(m) != 2 {
		t.Fatalf("malformed entries: %+v", m)
	}
	if len(s.metaKeys("empty")) != 0 {
		t.Fatal("empty collection")
	}
}
