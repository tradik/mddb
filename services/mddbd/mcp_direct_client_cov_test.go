package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// covC: the DirectClient paths an agent reaches through MCP and gRPC that no
// test exercised — damaged records, expired documents, missing subsystems and
// a database that refuses every transaction.

// covCServer is a fully wired server and its direct client, cleaned up with t.
func covCServer(t *testing.T) (*DirectClient, *Server) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	return NewDirectClient(s), s
}

// covCClosedClient is a client whose database is closed, so every transaction
// it opens fails.
func covCClosedClient(t *testing.T) (*DirectClient, *Server) {
	t.Helper()
	c, s := covCServer(t)
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	return c, s
}

// covCBareClient is a client over a database that has no buckets at all.
func covCBareClient(t *testing.T) *DirectClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bare.db")
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewDirectClient(&Server{DB: db, Path: path, Mode: ModeRW})
}

// covCPut writes raw bytes under key in bucket, bypassing every write path.
func covCPut(t *testing.T, s *Server, bucket string, key, val []byte) {
	t.Helper()
	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(bucket)).Put(key, val) }); err != nil {
		t.Fatal(err)
	}
}

// covCDel removes key from bucket, leaving every index that points at it.
func covCDel(t *testing.T, s *Server, bucket string, key []byte) {
	t.Helper()
	if err := s.DB.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(bucket)).Delete(key) }); err != nil {
		t.Fatal(err)
	}
}

// covCRewrite changes a stored document in place.
func covCRewrite(t *testing.T, s *Server, coll string, doc storage.Doc, change func(*storage.Doc)) {
	t.Helper()
	change(&doc)
	buf, err := marshalDoc(&doc)
	if err != nil {
		t.Fatal(err)
	}
	covCPut(t, s, "docs", storage.DocKey(coll, doc.ID), buf)
}

// covCGarbage is a stored document that cannot be decoded.
var covCGarbage = []byte("{not json")

func covCExpire(d *storage.Doc) { d.ExpiresAt = time.Now().Add(-time.Hour).Unix() }

func TestCovCAddBatchUsesTheFinalProcessorWhenConfigured(t *testing.T) {
	c, s := covCServer(t)
	s.finalBatchProcessor = NewFinalBatchProcessor(s, 2)
	resp, err := c.AddBatch(context.Background(), &MCPAddBatchRequest{Collection: "b", Documents: []MCPBatchDocument{
		{Key: "one", Lang: "en", ContentMD: "first"}, {Key: "two", Lang: "en", ContentMD: "second"},
	}})
	if err != nil || resp.Added != 2 || resp.Failed != 0 {
		t.Fatalf("AddBatch = %+v, %v", resp, err)
	}
	got, err := c.Get(context.Background(), &MCPGetRequest{Collection: "b", Key: "two", Lang: "en"})
	if err != nil || got.ContentMD != "second" {
		t.Fatalf("Get after batch = %+v, %v", got, err)
	}
}

func TestCovCGetRefusesMissingDamagedAndExpiredDocuments(t *testing.T) {
	c, s := covCServer(t)
	ctx := context.Background()
	gone := addTestDoc(t, s, "g", "gone", "en", "x", nil)
	covCDel(t, s, "docs", storage.DocKey("g", gone.ID))
	bad := addTestDoc(t, s, "g", "bad", "en", "x", nil)
	covCPut(t, s, "docs", storage.DocKey("g", bad.ID), covCGarbage)
	old := addTestDoc(t, s, "g", "old", "en", "x", nil)
	covCRewrite(t, s, "g", old, covCExpire)

	for _, key := range []string{"never", "gone", "old"} {
		if _, err := c.Get(ctx, &MCPGetRequest{Collection: "g", Key: key, Lang: "en"}); err == nil || err.Error() != "not found" {
			t.Errorf("Get(%s) err = %v, want not found", key, err)
		}
	}
	if _, err := c.Get(ctx, &MCPGetRequest{Collection: "g", Key: "bad", Lang: "en"}); err == nil {
		t.Error("a damaged document was returned without an error")
	}
}

func TestCovCGetSubstitutesEnvironment(t *testing.T) {
	c, s := covCServer(t)
	addTestDoc(t, s, "g", "tpl", "en", "Hello %%NAME%%", nil)
	got, err := c.Get(context.Background(), &MCPGetRequest{Collection: "g", Key: "tpl", Lang: "en",
		Env: map[string]string{"NAME": "world"}})
	if err != nil || got.ContentMD != "Hello world" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

// covCSearchKeys is the keys a search returned, in order.
func covCSearchKeys(t *testing.T, c *DirectClient, req MCPSearchRequest) []string {
	t.Helper()
	resp, err := c.Search(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(resp.Documents))
	for _, d := range resp.Documents {
		keys = append(keys, d.Key)
	}
	return keys
}

func TestCovCSearchSortsByEveryFieldInBothDirections(t *testing.T) {
	c, s := covCServer(t)
	// addedAt rises a→c, updatedAt rises c→a, so the two orders differ.
	for i, key := range []string{"a", "b", "c"} {
		d := addTestDoc(t, s, "sort", key, "en", "x", nil)
		covCRewrite(t, s, "sort", d, func(d *storage.Doc) { d.AddedAt = int64(100 + i); d.UpdatedAt = int64(300 - i) })
	}
	cases := []struct {
		sort string
		asc  bool
		want string
	}{
		{"addedAt", true, "a,b,c"}, {"addedAt", false, "c,b,a"},
		{"updatedAt", true, "c,b,a"}, {"updatedAt", false, "a,b,c"},
		{"key", true, "a,b,c"}, {"key", false, "c,b,a"},
	}
	for _, tc := range cases {
		got := strings.Join(covCSearchKeys(t, c, MCPSearchRequest{Collection: "sort", Sort: tc.sort, Asc: tc.asc}), ",")
		if got != tc.want {
			t.Errorf("sort %s asc=%v = %s, want %s", tc.sort, tc.asc, got, tc.want)
		}
	}
	resp, err := c.Search(context.Background(), &MCPSearchRequest{Collection: "sort", Offset: 10})
	if err != nil || len(resp.Documents) != 0 || resp.Total != 3 {
		t.Fatalf("offset past the end = %+v, %v", resp, err)
	}
}

func TestCovCSearchSkipsExpiredAndDanglingDocuments(t *testing.T) {
	c, s := covCServer(t)
	tag := map[string][]string{"tag": {"t"}}
	addTestDoc(t, s, "s", "live", "en", "body", tag)
	old := addTestDoc(t, s, "s", "old", "en", "body", tag)
	covCRewrite(t, s, "s", old, covCExpire)
	gone := addTestDoc(t, s, "s", "gone", "en", "body", tag)
	covCDel(t, s, "docs", storage.DocKey("s", gone.ID))

	for _, filter := range []map[string][]string{nil, tag} {
		resp, err := c.Search(context.Background(), &MCPSearchRequest{Collection: "s", FilterMeta: filter})
		if err != nil || len(resp.Documents) != 1 || resp.Documents[0].Key != "live" {
			t.Fatalf("filter %v: %+v, %v", filter, resp, err)
		}
		if resp.Documents[0].ContentMD != "" {
			t.Errorf("filter %v: content returned without includeContent", filter)
		}
	}
}

func TestCovCSearchFailsOnADamagedDocument(t *testing.T) {
	for _, filter := range []map[string][]string{nil, {"tag": {"t"}}} {
		c, s := covCServer(t)
		bad := addTestDoc(t, s, "s", "bad", "en", "body", map[string][]string{"tag": {"t"}})
		covCPut(t, s, "docs", storage.DocKey("s", bad.ID), covCGarbage)
		if _, err := c.Search(context.Background(), &MCPSearchRequest{Collection: "s", FilterMeta: filter}); err == nil {
			t.Errorf("filter %v: a damaged document did not fail the search", filter)
		}
	}
}

func TestCovCClosedDatabaseFailsEveryOperation(t *testing.T) {
	c, _ := covCClosedClient(t)
	ctx := context.Background()
	if _, err := c.DeleteCollection(ctx, &MCPDeleteCollectionRequest{Collection: "x"}); err == nil {
		t.Error("DeleteCollection succeeded on a closed database")
	}
	if _, err := c.Export(ctx, &MCPExportRequest{Collection: "x"}); err == nil {
		t.Error("Export succeeded on a closed database")
	}
	if _, err := c.Truncate(ctx, &MCPTruncateRequest{Collection: "x"}); err == nil {
		t.Error("Truncate succeeded on a closed database")
	}
	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())
	if _, err := c.Backup(ctx, &MCPBackupRequest{To: "closed.db"}); err == nil {
		t.Error("Backup succeeded on a closed database")
	}
}

func TestCovCExportSkipsDamagedAndDanglingDocuments(t *testing.T) {
	c, s := covCServer(t)
	tag := map[string][]string{"tag": {"t"}}
	addTestDoc(t, s, "e", "good", "en", "body", tag)
	bad := addTestDoc(t, s, "e", "bad", "en", "body", tag)
	covCPut(t, s, "docs", storage.DocKey("e", bad.ID), covCGarbage)
	gone := addTestDoc(t, s, "e", "gone", "en", "body", tag)
	covCDel(t, s, "docs", storage.DocKey("e", gone.ID))

	for _, filter := range []map[string][]string{nil, tag} {
		rc, err := c.Export(context.Background(), &MCPExportRequest{Collection: "e", FilterMeta: filter})
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if lines := strings.Count(string(out), "\n"); lines != 1 || !strings.Contains(string(out), `"good"`) {
			t.Errorf("filter %v: export = %q, want only the good document", filter, out)
		}
	}
}

// A reader that walks away mid-export must stop the writer with an error,
// not leave it blocked on a pipe nobody reads.
func TestCovCExportStopsWhenTheReaderCloses(t *testing.T) {
	c, s := covCServer(t)
	addTestDoc(t, s, "big", "one", "en", strings.Repeat("x", 2*exportBufferSize), nil)
	rc, err := c.Export(context.Background(), &MCPExportRequest{Collection: "big"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rc, make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if _, err := rc.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("read after close = %v, want ErrClosedPipe", err)
	}
}

func TestCovCBackupNamesTheFileWhenNoneIsGiven(t *testing.T) {
	c, _ := covCServer(t)
	dir := t.TempDir()
	t.Setenv("MDDB_BACKUP_DIR", dir)
	resp, err := c.Backup(context.Background(), &MCPBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(resp.Backup), "backup-") {
		t.Errorf("backup name = %s", resp.Backup)
	}
	if _, err := os.Stat(resp.Backup); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
}

func TestCovCRestoreRefusesAPathOutsideTheBackupDirectory(t *testing.T) {
	c, _ := covCServer(t)
	t.Setenv("MDDB_BACKUP_DIR", t.TempDir())
	if _, err := c.Restore(context.Background(), &MCPRestoreRequest{From: "../../outside.db"}); err == nil {
		t.Fatal("restore from outside the backup directory was accepted")
	}
	if _, err := c.Restore(context.Background(), &MCPRestoreRequest{}); err == nil || err.Error() != "missing from" {
		t.Fatalf("restore without a source: %v", err)
	}
}

func TestCovCTruncateFailsOnADamagedDocument(t *testing.T) {
	c, s := covCServer(t)
	bad := addTestDoc(t, s, "t", "bad", "en", "body", nil)
	covCPut(t, s, "docs", storage.DocKey("t", bad.ID), covCGarbage)
	if _, err := c.Truncate(context.Background(), &MCPTruncateRequest{Collection: "t"}); err == nil {
		t.Fatal("truncate over a damaged document reported success")
	}
}
