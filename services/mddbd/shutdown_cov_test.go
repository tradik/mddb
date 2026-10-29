package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mddb/internal/cache"
	"mddb/internal/indexqueue"
	"mddb/internal/temporal"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// covALogBuffer is a goroutine-safe sink for slog output.
type covALogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *covALogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *covALogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// covACaptureLogs routes the default logger into a buffer for the rest of
// the test and restores the previous logger afterwards.
func covACaptureLogs(t *testing.T) *covALogBuffer {
	t.Helper()
	buf := &covALogBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// covAClosedDB returns a bolt handle that has already been closed and that
// claims its freelist still needs storing.
func covAClosedDB(t *testing.T, name string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), name), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	db.NoFreelistSync = true
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return db
}

// Every subsystem main can start has a step, in dependency order, and running
// the sequence stops all of them and closes the database.
func TestCovAShutdownStopsEverySubsystem(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	h3, err := NewHTTP3Server("127.0.0.1:0", http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	s.HTTP3 = h3
	s.CronScheduler = NewCronScheduler(s)
	s.CronScheduler.Start()
	s.BulkIngest = NewBulkIngestManager(s, 4)
	s.BulkIngest.Start()
	s.IndexQueue = indexqueue.NewIndexQueue(nil, 1)
	s.TemporalManager = temporal.NewTemporalManager(s.DB)
	if err := s.TemporalManager.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	s.TemporalManager.Start()
	s.MVCC = NewMVCC()
	s.AdaptiveIndex = NewAdaptiveIndexManager()
	wal, err := NewWAL(s.Path, SyncPeriodic)
	if err != nil {
		t.Fatal(err)
	}
	s.WAL = wal
	s.LockFreeCache = cache.NewLockFreeCache(10, 60)
	s.EmbeddingWorker = NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, s.VectorStore, s.VectorIndex, 4)
	s.EmbeddingWorker.Start(1)
	s.Gain = NewGainRecorder(func() *bolt.DB { return s.DB })
	s.Gain.Start()

	var names []string
	for _, st := range s.shutdownSteps() {
		names = append(names, st.name)
	}
	want := "http3,cron,bulk-ingest,index-queue,embedding-worker,ttl,temporal,mvcc,adaptive-index,wal,gain,freelist,lockfree-cache,document-cache,database"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("steps = %s\nwant    %s", got, want)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !s.Shutdown(ctx) {
		t.Fatal("every step should finish well inside the deadline")
	}
	if err := s.DB.View(func(*bolt.Tx) error { return nil }); !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Fatalf("database should be closed after shutdown, View returned %v", err)
	}
}

// Gain counters and a freelist that cannot be stored are reported — for the
// main file and for each collection file — and the sequence still completes.
func TestCovAShutdownReportsUnstorableFreelists(t *testing.T) {
	logs := covACaptureLogs(t)
	s := &Server{
		DB:        covAClosedDB(t, "main.db"),
		collFiles: &collectionFiles{dbs: map[string]*bolt.DB{"c": covAClosedDB(t, "c.db")}},
	}
	s.Gain = NewGainRecorder(func() *bolt.DB { return s.DB })
	s.Gain.Search(gainKind{Type: "fts"})
	if !s.Shutdown(context.Background()) {
		t.Fatal("shutdown should complete")
	}
	out := logs.String()
	for _, want := range []string{"gain counters not stored", "the next start will walk the database", "the next start will walk a collection file"} {
		if !strings.Contains(out, want) {
			t.Errorf("log is missing %q:\n%s", want, out)
		}
	}
}
