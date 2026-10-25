package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// #333: a clean shutdown stores the freelist, and the next open reads it.

func newWalkingDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := bolt.Open(path, 0o600, getOptimizedBoltOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists([]byte("b"))
		for i := 0; i < 500; i++ {
			_ = b.Put([]byte(strings.Repeat("k", i%40+1)+string(rune('a'+i%26))), make([]byte, 900))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return path
}

func TestCleanShutdownStoresTheFreelist(t *testing.T) {
	path := newWalkingDB(t)
	m, err := readBoltMeta(path)
	if err != nil || !m.needsWalk() || m.bytesInUse() <= 0 || m.pageSize == 0 {
		t.Fatalf("before: %+v %v", m, err)
	}

	db, err := bolt.Open(path, 0o600, getOptimizedBoltOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := persistFreelist(db); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if m, _ := readBoltMeta(path); m.needsWalk() {
		t.Fatal("the freelist was not stored")
	}

	// A start after that, followed by a write, clears it again: a crash in
	// that session must cost a walk, not trust a stale list.
	db, _ = bolt.Open(path, 0o600, getOptimizedBoltOptions())
	_ = db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte("b")).Put([]byte("x"), []byte("y")) })
	_ = db.Close()
	if m, _ := readBoltMeta(path); !m.needsWalk() {
		t.Error("a later commit left a stored freelist behind")
	}

	if persistFreelist(nil) != nil {
		t.Error("nil db")
	}
	ro, _ := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	defer func() { _ = ro.Close() }()
	if persistFreelist(ro) != nil {
		t.Error("read-only db should be left alone")
	}
}

func TestReadBoltMetaRejectsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := readBoltMeta(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file")
	}
	junk := filepath.Join(dir, "junk")
	_ = os.WriteFile(junk, make([]byte, 9000), 0o600)
	if _, err := readBoltMeta(junk); err == nil {
		t.Error("a file of zeros has no meta page")
	}
	short := filepath.Join(dir, "short")
	_ = os.WriteFile(short, []byte("x"), 0o600)
	if _, err := readBoltMeta(short); err == nil {
		t.Error("a one-byte file")
	}
}

func TestShouldPrewarm(t *testing.T) {
	walk, stored := boltMeta{freelist: noFreelist}, boltMeta{freelist: 7}
	cases := []struct {
		env    string
		meta   boltMeta
		size   int64
		mem    uint64
		expect bool
	}{
		{"", walk, 100, 1000, true},
		{"", walk, 600, 1000, false}, // more than half the memory
		{"", stored, 100, 1000, false},
		{"", walk, 100, 0, false}, // memory unknown
		{"on", stored, 100, 0, true},
		{"off", walk, 100, 1000, false},
	}
	for _, c := range cases {
		t.Setenv("MDDB_PREWARM", c.env)
		if got := shouldPrewarm(c.meta, c.size, c.mem); got != c.expect {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func TestOpenDatabaseReportsTheWalk(t *testing.T) {
	path := newWalkingDB(t)
	t.Setenv("MDDB_PREWARM", "on")
	clock := newStartupClock()
	var seen []string
	clock.begin("opening database")
	db, walked, err := openDatabase(clock, path, getOptimizedBoltOptions())
	if err != nil || !walked {
		t.Fatalf("walked=%v err=%v", walked, err)
	}
	phase, _ := clock.current()
	seen = append(seen, phase)
	_ = persistFreelist(db)
	_ = db.Close()

	t.Setenv("MDDB_PREWARM", "off")
	db, walked, err = openDatabase(clock, path, getOptimizedBoltOptions())
	if err != nil || walked {
		t.Fatalf("after a clean shutdown: walked=%v err=%v", walked, err)
	}
	_ = db.Close()
	if seen[0] != "opening database" {
		t.Errorf("phase after prewarm = %q", seen[0])
	}
	if err := prewarmFile(filepath.Join(t.TempDir(), "none"), nil); err == nil {
		t.Error("prewarming a missing file")
	}
}

func TestStartupProgressOnHealth(t *testing.T) {
	clock := newStartupClock()
	clock.begin("reading database into cache")
	if clock.progress() != nil {
		t.Error("progress before any was reported")
	}
	clock.setProgress(250, 1000)
	clock.progressSince = time.Now().Add(-10 * time.Second)
	clock.progressLogged = time.Now().Add(-2 * time.Minute) // the next update logs
	clock.setProgress(500, 1000)

	rec := httptest.NewRecorder()
	clock.healthHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body struct {
		Phase    string           `json:"phase"`
		Progress *startupProgress `json:"progress"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body.Progress == nil || body.Progress.Percent != 50 ||
		body.Progress.EtaSeconds < 5 || body.Progress.EtaSeconds > 15 {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}
	clock.begin("next")
	if clock.progress() != nil {
		t.Error("a new phase kept the old progress")
	}
	stop := make(chan struct{})
	close(stop)
	sampleWalkProgress(clock, 1000, stop) // returns at once
	sampleWalkProgress(clock, 0, nil)     // nothing to measure
}

func TestBytesInUseDoesNotOverflow(t *testing.T) {
	if (boltMeta{pageSize: 4096, pgid: ^uint64(0) / 2}).bytesInUse() <= 0 {
		t.Error("overflowed")
	}
	if (boltMeta{}).bytesInUse() <= 0 {
		t.Error("a zero page size should not divide by zero")
	}
}
