package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	pb "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// GO-054: a tenant quota caps the sum over the tenant's collections, with the
// same refusal as a collection quota on every surface.

// tenantQuotaServer is quotaTestServer with a tenant store and two
// documents in each of two collections of tenant "acme".
func tenantQuotaServer(t *testing.T) (*GRPCServer, *Server) {
	t.Helper()
	gs, s, cleanup := newTestGRPCServerFull(t)
	t.Cleanup(cleanup)
	s.Tenants = NewTenantManager(s.DB)
	if err := s.Tenants.LoadAll(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"acme/a", "acme/b"} {
		addTestDoc(t, s, c, "one", "en", "first document", nil)
		addTestDoc(t, s, c, "two", "en", "second document", nil)
	}
	return gs, s
}

// setTenantQuota stores a tenant's quota.
func setTenantQuota(t *testing.T, s *Server, tenant string, q TenantQuota) {
	t.Helper()
	if err := s.Tenants.Set(tenant, TenantConfig{Quota: &q}); err != nil {
		t.Fatal(err)
	}
}

func TestTenantQuotaRefusesOnEverySurface(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(t *testing.T, gs *GRPCServer, s *Server){
		"REST": func(t *testing.T, _ *GRPCServer, s *Server) {
			rec := doRequest(t, s.handleAdd, map[string]any{"collection": "acme/a", "key": "n", "lang": "en", "contentMd": "new"})
			wantHTTPQuota(t, rec, http.StatusInsufficientStorage, quotaDocuments)
			if !strings.Contains(rec.Body.String(), `"tenant":"acme"`) {
				t.Fatalf("body does not name the tenant: %s", rec.Body.String())
			}
		},
		"gRPC": func(t *testing.T, gs *GRPCServer, _ *Server) {
			_, err := gs.Add(ctx, &pb.AddRequest{Collection: "acme/b", Key: "n", Lang: "en", ContentMd: "new"})
			wantGRPCQuota(t, err)
		},
		"MCP": func(t *testing.T, _ *GRPCServer, s *Server) {
			wantMCPQuota(t, mcpCall(s, "add_document", map[string]any{"collection": "acme/a", "key": "n", "lang": "en", "content_md": "new"}),
				http.StatusInsufficientStorage)
		},
		"GraphQL": func(t *testing.T, _ *GRPCServer, s *Server) {
			wantGraphQLQuota(t, graphQLPost(t, s,
				`mutation { addDocument(input:{collection:"acme/b", key:"n", lang:"en", contentMd:"new"}) { key } }`),
				http.StatusInsufficientStorage)
		},
	}
	for name, write := range cases {
		t.Run(name, func(t *testing.T) {
			gs, s := tenantQuotaServer(t)
			setTenantQuota(t, s, "acme", TenantQuota{MaxDocuments: 4})
			write(t, gs, s)
			// An update that does not grow the tenant still goes through.
			addTestDoc(t, s, "acme/a", "one", "en", "first", nil)
			// Another tenant and a global collection are not limited by acme's quota.
			addTestDoc(t, s, "other/x", "n", "en", "fine", nil)
			addTestDoc(t, s, "plain", "n", "en", "fine", nil)
			if got := s.tenantRefusals("acme")["quota.documents"]; got != 1 {
				t.Fatalf("refusals = %v", s.tenantRefusals("acme"))
			}
		})
	}
}

func TestTenantQuotaBytesCollectionsAndVectors(t *testing.T) {
	_, s := tenantQuotaServer(t)
	tenantUsageRefresh = 0 // count the other collections afresh on every write
	t.Cleanup(func() { tenantUsageRefresh = 5e9 })

	setTenantQuota(t, s, "acme", TenantQuota{MaxCollections: 2})
	err := s.addOneForTest("acme/c", "n", "third collection")
	if qe := asQuotaError(err); qe == nil || qe.Resource != quotaCollections || qe.Used != 2 || qe.Tenant != "acme" {
		t.Fatalf("a third collection: %v", err)
	}
	if !strings.Contains(err.Error(), `collections of tenant "acme"`) {
		t.Fatalf("message = %v", err)
	}
	if err := s.addOneForTest("acme/a", "n", "more in an existing one"); err != nil {
		t.Fatalf("a write to an existing collection: %v", err)
	}

	setTenantQuota(t, s, "acme", TenantQuota{MaxBytes: 1})
	if qe := asQuotaError(s.addOneForTest("acme/b", "big", strings.Repeat("x", 100))); qe == nil || qe.Resource != quotaBytes {
		t.Fatalf("bytes: %v", qe)
	}

	setTenantQuota(t, s, "acme", TenantQuota{MaxVectors: 1})
	if err := s.VectorIndex.Add("acme/a", "one#0", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if qe := asQuotaError(s.addOneForTest("acme/b", "v", "vectors")); qe == nil || qe.Resource != quotaVectors {
		t.Fatalf("vectors: %v", qe)
	}
	setTenantQuota(t, s, "acme", TenantQuota{MaxVectors: 10})
	if err := s.addOneForTest("acme/b", "v", "vectors"); err != nil {
		t.Fatalf("room for vectors: %v", err)
	}
}

// addOneForTest writes one document through the shared write path.
func (s *Server) addOneForTest(collection, key, content string) error {
	_, _, err := s.addDocument(collection, key, "en", nil, content, 0, true)
	return err
}

func TestTenantVectorRAMShare(t *testing.T) {
	t.Setenv("MDDB_VECTOR_RAM_LIMIT_BYTES", "1000000000")
	_, s := tenantQuotaServer(t)
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	for i, c := range []string{"acme/a", "other/x"} {
		if err := s.VectorIndex.Add(c, "d#0", []float32{float32(i), 1, 0}); err != nil {
			t.Fatal(err)
		}
	}
	// A share so small any vector passes it.
	setTenantQuota(t, s, "acme", TenantQuota{VectorRAMShare: 0.000000001})
	qe := asQuotaError(s.addOneForTest("acme/a", "n", "new"))
	if qe == nil || qe.Resource != quotaVectorRAM || qe.Tenant != "acme" || qe.Limit != 1 {
		t.Fatalf("share: %v", qe)
	}
	// Other tenants are under the server limit only.
	if err := s.addOneForTest("other/x", "n", "new"); err != nil {
		t.Fatalf("other tenant: %v", err)
	}
	// MDDB_TENANT_VECTOR_RAM_SHARE is the default for a tenant without one.
	if got := (tenantDefaults{ramShare: 0.5}).ramShareFor(nil); got != 0.5 {
		t.Fatalf("default share = %v", got)
	}
	if got := s.tenantVectorRAM("nobody", 100); got != 0 {
		t.Fatalf("a tenant without vectors takes %d bytes", got)
	}
}

func TestTenantCollectionsAcrossLayouts(t *testing.T) {
	for _, layout := range []string{"single", "per-collection"} {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			s, cleanup := newHandlerTestServer(t)
			t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
			s.Tenants = NewTenantManager(s.DB)
			_ = s.Tenants.LoadAll()
			for _, c := range []string{"acme/a", "acme/b", "acmex/c", "other/d"} {
				addTestDoc(t, s, c, "k", "en", "text", nil)
			}
			if err := s.CollectionManager.Set("acme/empty", &CollectionConfig{}); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(s.tenantCollections(nil, "acme"), ",")
			if got != "acme/a,acme/b,acme/empty" {
				t.Fatalf("collections = %s", got)
			}
			u := s.tenantUsageReport("acme").Usage
			if u.Documents != 2 || u.Collections != 3 || u.Bytes <= 0 {
				t.Fatalf("usage = %+v", u)
			}
			// Inside a write transaction of the main file.
			_ = s.DB.Update(func(tx *bolt.Tx) error {
				if n := len(s.tenantCollections(tx, "acme")); n != 3 {
					t.Errorf("in a transaction: %d collections", n)
				}
				o := s.tenantOthers(tx, "acme", "acme/a")
				if o.docs != 1 || o.collections != 2 {
					t.Errorf("others = %+v", o)
				}
				return nil
			})
		})
	}
}

func TestNamespaceCollectionsSkipsOddKeys(t *testing.T) {
	_, s := tenantQuotaServer(t)
	_ = s.DB.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("docs"))
		return b.Put([]byte("doc|acme/odd"), []byte("x")) // no second separator
	})
	_ = s.DB.View(func(tx *bolt.Tx) error {
		got := namespaceCollections(tx, "acme")
		for _, c := range got {
			if c == "acme/odd" {
				t.Errorf("an odd key was taken for a collection: %v", got)
			}
		}
		return nil
	})
	_ = s.DB.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("docs")) })
	_ = s.DB.View(func(tx *bolt.Tx) error {
		if namespaceCollections(tx, "acme") != nil {
			t.Error("collections without a docs bucket")
		}
		return nil
	})
}

func TestTenantQuotaValidation(t *testing.T) {
	for _, q := range []TenantQuota{{MaxDocuments: -1}, {MaxCollections: -1}, {VectorRAMShare: 1.5}, {VectorRAMShare: -0.1}} {
		if (&TenantConfig{Quota: &q}).Validate() == nil {
			t.Errorf("accepted %+v", q)
		}
	}
	if (&TenantConfig{Limits: &TenantLimits{ReadPerSecond: -2}}).Validate() == nil {
		t.Error("accepted a limit below -1")
	}
	var nilQ *TenantQuota
	var nilL *TenantLimits
	if nilQ.Validate() != nil || nilL.Validate() != nil || nilQ.limitsStorage() || nilQ.limitsEmbedding() {
		t.Error("nil parts")
	}
	if (&TenantConfig{}).Validate() != nil {
		t.Error("an empty config is valid")
	}
}

func TestTenantDefaultsFromEnvironment(t *testing.T) {
	t.Setenv("MDDB_TENANT_READ_RPS", "10")
	t.Setenv("MDDB_TENANT_VECTOR_RAM_SHARE", "0.25")
	d := readTenantDefaults()
	if d.limits.ReadPerSecond != 10 || d.ramShare != 0.25 {
		t.Fatalf("defaults = %+v", d)
	}
	eff := d.effective(&TenantLimits{ReadPerSecond: -1, WritePerSecond: 3})
	if eff.ReadPerSecond != 0 || eff.WritePerSecond != 3 {
		t.Fatalf("effective = %+v", eff)
	}
	if d.effective(nil).ReadPerSecond != 10 {
		t.Fatal("no override takes the default")
	}
	t.Setenv("MDDB_TENANT_VECTOR_RAM_SHARE", "2")
	if readTenantDefaults().ramShare != 0 {
		t.Fatal("a share above 1 was kept")
	}
	if (tenantDefaults{}).ramShareFor(&TenantQuota{VectorRAMShare: 0.3}) != 0.3 {
		t.Fatal("the tenant's own share")
	}
}
