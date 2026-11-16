package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GO-054: the edges of the tenant code — failures, stores not loaded, the
// streaming gate and a request whose client gives up while it waits.

func TestTenantGateEdges(t *testing.T) {
	t.Setenv("MDDB_TENANT_QUEUE_TIMEOUT_MS", "2000")
	s := limitServer(t)
	setLimits(t, s, "t1", TenantLimits{ReadPerSecond: 1, MaxConcurrentWrites: 1})

	// A gRPC stream is admitted once, when it starts.
	client := grpcClient(t, s)
	drain := func() error {
		stream, err := client.Export(grpcAs(t, tina), &proto.ExportRequest{Collection: "t1/notes", Format: "ndjson"})
		if err != nil {
			return err
		}
		for {
			if _, err := stream.Recv(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
		}
	}
	if err := drain(); status.Code(err) == codes.ResourceExhausted {
		t.Fatalf("first export refused: %v", err)
	}
	if err := drain(); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second export: %v", err)
	}

	// A REST write whose client gives up while waiting for a write slot.
	_, hold, err := s.admitRequest(context.WithValue(context.Background(), authContextKey, tina), PermWrite)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/add", strings.NewReader(`{"collection":"t1/n","key":"k","lang":"en","contentMd":"x"}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	asUser(tina, mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestTimeout {
		t.Fatalf("a client that gave up: %d %s", rec.Code, rec.Body.String())
	}
	hold()

	// The list is a global admin's even when the gate is bypassed.
	rec = httptest.NewRecorder()
	s.handleTenants(rec, httptest.NewRequest(http.MethodGet, "/v1/tenants", nil).WithContext(context.WithValue(context.Background(), authContextKey, tina)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant list as a tenant user: %d", rec.Code)
	}
	if got := restRec(t, s, root, http.MethodGet, "/v1/tenants/t1/usage", ""); got.Code != http.StatusOK {
		t.Fatalf("admin reads a tenant's usage: %d", got.Code)
	}
}

func TestTenantStoreFailures(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	tm := NewTenantManager(s.DB)
	if err := tm.Set("acme", TenantConfig{Limits: &TenantLimits{ReadPerSecond: 1}}); !errors.Is(err, errTenantsNotLoaded) {
		t.Fatalf("Set before LoadAll: %v", err)
	}
	if err := tm.Delete("acme"); err != nil {
		t.Fatalf("Delete before LoadAll: %v", err)
	}
	dir := t.TempDir()
	db, err := bolt.Open(dir+"/t.db", 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	closed := NewTenantManager(db)
	if closed.LoadAll() == nil {
		t.Fatal("LoadAll on a closed database")
	}
	if closed.Delete("x") == nil {
		t.Fatal("Delete on a closed database")
	}
}

func TestTenantQuotaEdges(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	t.Setenv("MDDB_VECTOR_RAM_LIMIT_BYTES", "1000000000")
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
	s.Tenants = NewTenantManager(s.DB)
	_ = s.Tenants.LoadAll()
	old := tenantUsageRefresh
	tenantUsageRefresh = 0
	t.Cleanup(func() { tenantUsageRefresh = old })
	addTestDoc(t, s, "acme/a", "k", "en", "text", nil)
	addTestDoc(t, s, "acme/b", "k", "en", "text", nil)
	// A transaction of the main file counts collections living in their own.
	_ = s.DB.View(func(tx *bolt.Tx) error {
		if o := s.tenantOthers(tx, "acme", "acme/a"); o.docs != 1 {
			t.Errorf("others from another file: %+v", o)
		}
		return nil
	})
	// A share with room left lets the write through.
	s.Embedding = &mockEmbedding{dims: 3, model: "m"}
	if qe := s.vectorRAMExceeded("acme", &TenantQuota{VectorRAMShare: 0.9}, 1_000_000_000); qe != nil {
		t.Fatalf("share with room: %v", qe)
	}
	s.CollectionManager = nil
	if s.collectionConfigured("acme/a") {
		t.Fatal("configured without a manager")
	}
}

func TestGRPCAPIKeyTokenFailure(t *testing.T) {
	s := limitServer(t)
	key, err := s.AuthManager.CreateAPIKey("tina", "k", 0)
	if err != nil {
		t.Fatal(err)
	}
	orig := apiKeySessionToken
	apiKeySessionToken = func(string, string, bool, string, time.Duration) (string, error) { return "", errors.New("no") }
	t.Cleanup(func() { apiKeySessionToken = orig })
	md := metadata.New(map[string]string{"x-api-key": key})
	if _, err := s.AuthManager.authenticateContext(metadata.NewIncomingContext(context.Background(), md)); status.Code(err) != codes.Internal {
		t.Fatalf("token failure: %v", err)
	}
}

func TestTenantStoreRecordsChangesInTheBinlog(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	bl, err := binlog.NewBinlog(filepath.Join(t.TempDir(), "bl.db"), binlog.BinlogConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	tm := NewTenantManager(s.DB)
	tm.SetBinlog(bl)
	_ = tm.LoadAll()
	before := bl.CurrentLSN()
	if err := tm.Set("acme", TenantConfig{Quota: &TenantQuota{MaxDocuments: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := tm.Delete("acme"); err != nil {
		t.Fatal(err)
	}
	if got := bl.CurrentLSN() - before; got != 2 {
		t.Fatalf("binlog entries = %d, want 2", got)
	}
	// A method that names no collection is not admitted (nothing to count).
	if _, release, err := s.admitRPC(context.Background(), "/mddb.MDDB/Health"); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}
