package main

// Coverage for grpc_server.go (covE batch): the transport builder, the core
// document RPCs on their permission, read-only, extreme-mode, corrupt-data
// and storage-failure paths.

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"mddb/internal/cache"
	"mddb/internal/storage"
	pb "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

// covEExportStream is an Export stream that only carries a context.
type covEExportStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (c covEExportStream) Context() context.Context   { return c.ctx }
func (c covEExportStream) Send(*pb.ExportChunk) error { return nil }

func covEServerGuarded(gs *GRPCServer) []covERPC {
	return []covERPC{
		{"Add", func(ctx context.Context) error {
			_, err := gs.Add(ctx, &pb.AddRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"AddBatch", func(ctx context.Context) error {
			_, err := gs.AddBatch(ctx, &pb.AddBatchRequest{Collection: "c"})
			return err
		}},
		{"Ingest", func(ctx context.Context) error {
			_, err := gs.Ingest(ctx, &pb.IngestRequest{Collection: "c"})
			return err
		}},
		{"Get", func(ctx context.Context) error {
			_, err := gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "k", Lang: "en"})
			return err
		}},
		{"Search", func(ctx context.Context) error {
			_, err := gs.Search(ctx, &pb.SearchRequest{Collection: "c"})
			return err
		}},
		{"Export", func(ctx context.Context) error {
			return gs.Export(&pb.ExportRequest{Collection: "c"}, covEExportStream{ctx: ctx})
		}},
		{"Backup", func(ctx context.Context) error {
			_, err := gs.Backup(ctx, &pb.BackupRequest{})
			return err
		}},
		{"Restore", func(ctx context.Context) error {
			_, err := gs.Restore(ctx, &pb.RestoreRequest{From: "x.db"})
			return err
		}},
		{"Truncate", func(ctx context.Context) error {
			_, err := gs.Truncate(ctx, &pb.TruncateRequest{Collection: "c"})
			return err
		}},
		{"Stats", func(ctx context.Context) error {
			_, err := gs.Stats(ctx, &pb.StatsRequest{})
			return err
		}},
	}
}

func TestCovEServerPermissionDenied(t *testing.T) {
	gs, s := covEServer(t)
	covEDenyAll(t, s)
	covERun(t, context.Background(), covEServerGuarded(gs), codes.PermissionDenied, "")
}

func TestCovEIngestReadOnly(t *testing.T) {
	gs, s := covEServer(t)
	s.Mode = ModeRead
	_, err := gs.Ingest(context.Background(), &pb.IngestRequest{Collection: "c"})
	covEWant(t, "ingest", err, codes.PermissionDenied, "read-only mode")
}

func TestCovEIngest(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()

	_, err := gs.Ingest(ctx, &pb.IngestRequest{})
	covEWant(t, "no collection", err, codes.InvalidArgument, "missing collection")

	empty, err := gs.Ingest(ctx, &pb.IngestRequest{Collection: "c"})
	if err != nil || empty.Collection != "c" || empty.Added != 0 {
		t.Fatalf("empty ingest: %v %+v", err, empty)
	}

	resp, err := gs.Ingest(ctx, &pb.IngestRequest{Collection: "c", Documents: []*pb.IngestDocument{
		{Key: "one", Lang: "en", ContentMd: "first"},
		{Url: "https://example.com/docs/two.md", Lang: "en", ContentMd: "second"},
	}, Options: &pb.IngestOptions{SkipEmbeddings: true}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Added != 2 || resp.Collection != "c" {
		t.Fatalf("ingest: %+v", resp)
	}
	got, err := gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "one", Lang: "en"})
	if err != nil || got.ContentMd != "first" {
		t.Fatalf("ingested doc: %v %+v", err, got)
	}

	covECloseDB(t, s)
	_, err = gs.Ingest(ctx, &pb.IngestRequest{Collection: "c", Documents: []*pb.IngestDocument{{Key: "x", Lang: "en", ContentMd: "x"}}})
	covEWant(t, "closed db", err, codes.Internal, "")
}

func TestCovEExtremeModeAddBatchAndGet(t *testing.T) {
	_, s := covEServer(t)
	s.UseExtreme = true
	gs := NewGRPCServer(s)
	ctx := context.Background()

	resp, err := gs.AddBatch(ctx, &pb.AddBatchRequest{Collection: "c", Documents: []*pb.BatchDocument{
		{Key: "batch", Lang: "en", ContentMd: "from the batch"},
	}})
	if err != nil || resp.Added != 1 {
		t.Fatalf("extreme batch: %v %+v", err, resp)
	}
	got, err := gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "batch", Lang: "en"})
	if err != nil || got.ContentMd != "from the batch" {
		t.Fatalf("batch doc: %v %+v", err, got)
	}

	// A document written behind the cache's back is read from the database.
	raw := storage.Doc{ID: genID("c", "k", "en"), Key: "k", Lang: "en", ContentMD: "Hello %%WHO%%"}
	buf, err := marshalDoc(&raw)
	if err != nil {
		t.Fatal(err)
	}
	covEPutRaw(t, s, "c", "k", "en", buf)
	key := cache.BuildCacheKey("c", "k", "en")
	if _, found := s.LockFreeCache.Get(key); found {
		t.Fatal("raw write filled the lock-free cache")
	}
	// The first read fills the lock-free cache, the second is served by it.
	first, err := gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "k", Lang: "en", Env: map[string]string{"WHO": "db"}})
	if err != nil || first.ContentMd != "Hello db" {
		t.Fatalf("first get: %v %+v", err, first)
	}
	if _, found := s.LockFreeCache.Get(key); !found {
		t.Fatal("read did not fill the lock-free cache")
	}
	second, err := gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "k", Lang: "en"})
	if err != nil || second.ContentMd != "Hello %%WHO%%" || second.Revision != first.Revision {
		t.Fatalf("cached get: %v %+v", err, second)
	}
}

func TestCovEGetCorruptAndDangling(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	covEPutRaw(t, s, "c", "dangling", "en", nil)
	_, err := gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "dangling", Lang: "en"})
	covEWant(t, "dangling", err, codes.NotFound, "document not found")

	covEPutRaw(t, s, "c", "corrupt", "en", []byte{0xff, 0x00, 0x01})
	_, err = gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "corrupt", Lang: "en"})
	covEWant(t, "corrupt", err, codes.Internal, "")
}

func TestCovEStorageFailures(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	covECloseDB(t, s)
	covERun(t, ctx, []covERPC{
		{"Add", func(ctx context.Context) error {
			_, err := gs.Add(ctx, &pb.AddRequest{Collection: "c", Key: "k", Lang: "en", ContentMd: "x"})
			return err
		}},
		{"Search", func(ctx context.Context) error {
			_, err := gs.Search(ctx, &pb.SearchRequest{Collection: "c"})
			return err
		}},
		{"Truncate", func(ctx context.Context) error {
			_, err := gs.Truncate(ctx, &pb.TruncateRequest{Collection: "c"})
			return err
		}},
	}, codes.Internal, "database not open")

	// No statistics have ever been computed, and none can be now.
	t.Setenv("MDDB_STATS_WAIT", "0")
	_, err := gs.Stats(ctx, &pb.StatsRequest{})
	covEWant(t, "stats", err, codes.Unavailable, "being computed")
}

func TestCovEBackupRestoreEscapingPaths(t *testing.T) {
	gs, _ := covEServer(t)
	ctx := context.Background()
	_, err := gs.Backup(ctx, &pb.BackupRequest{To: "../escape.db"})
	covEWant(t, "backup", err, codes.InvalidArgument, "")
	_, err = gs.Restore(ctx, &pb.RestoreRequest{From: "../escape.db"})
	covEWant(t, "restore", err, codes.InvalidArgument, "")
}

func TestCovEAddBatchAndExportAllowed(t *testing.T) {
	gs, s := covEServer(t)
	covEDenyAll(t, s)
	ctx := covEAdminCtx()
	resp, err := gs.AddBatch(ctx, &pb.AddBatchRequest{Collection: "c", Documents: []*pb.BatchDocument{{Key: "k", Lang: "en", ContentMd: "x"}}})
	if err != nil || resp.Added != 1 {
		t.Fatalf("batch as admin: %v %+v", err, resp)
	}
	err = gs.Export(&pb.ExportRequest{Collection: "c"}, covEExportStream{ctx: ctx})
	covEWant(t, "export", err, codes.Unimplemented, "")
}

func TestCovEStartGRPCServerBadAddress(t *testing.T) {
	_, s := covEServer(t)
	addr := "unix:" + filepath.Join(t.TempDir(), "missing", "dir", "mddb.sock")
	if err := startGRPCServer(s, addr); err == nil {
		t.Fatal("listening on a socket in a missing directory succeeded")
	}
}

func TestCovEGRPCTransportServes(t *testing.T) {
	_, s := covEServer(t)
	s.ReplicationRole = "leader"
	t.Setenv("MDDB_REPLICATION_SECRET", "")
	t.Setenv("MDDB_AUTH_ENABLED", "")
	srv := newGRPCTransport(s)

	info := srv.GetServiceInfo()
	for _, name := range []string{"mddb.MDDB", "mddb.MDDBReplication", "grpc.reflection.v1.ServerReflection"} {
		if _, ok := info[name]; !ok {
			t.Fatalf("service %s not registered; have %v", name, info)
		}
	}
	if s.replServer == nil {
		t.Fatal("leader has no replication server")
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(lis) }()
	defer func() {
		srv.GracefulStop()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewMDDBClient(conn)
	if _, err := client.Add(context.Background(), &pb.AddRequest{Collection: "c", Key: "k", Lang: "en", ContentMd: "over the wire"}); err != nil {
		t.Fatal(err)
	}
	got, err := client.Get(context.Background(), &pb.GetRequest{Collection: "c", Key: "k", Lang: "en"})
	if err != nil || got.ContentMd != "over the wire" {
		t.Fatalf("get over the wire: %v %+v", err, got)
	}
}

func TestCovEBackupRestoreFailures(t *testing.T) {
	gs, s := covEServer(t)
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)
	addTestDoc(t, s, "c", "k", "en", "kept", nil)

	// A file that is not a database is refused, and the live data stays.
	if err := os.WriteFile(filepath.Join(dir, "junk.db"), []byte("not a bolt file"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := gs.Restore(ctx, &pb.RestoreRequest{From: "junk.db"})
	covEWant(t, "restore junk", err, codes.Internal, "")
	got, err := gs.Get(ctx, &pb.GetRequest{Collection: "c", Key: "k", Lang: "en"})
	if err != nil || got.ContentMd != "kept" {
		t.Fatalf("data after refused restore: %v %+v", err, got)
	}

	covECloseDB(t, s)
	_, err = gs.Backup(ctx, &pb.BackupRequest{To: "b.db"})
	covEWant(t, "backup closed db", err, codes.Internal, "")
}
