package migrate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func openTemp(t *testing.T) (*bolt.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

// put writes key/value pairs into bucket.
func put(t *testing.T, db *bolt.DB, bucket string, kv ...string) {
	t.Helper()
	err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucket))
		if err != nil {
			return err
		}
		for i := 0; i < len(kv); i += 2 {
			if err := b.Put([]byte(kv[i]), []byte(kv[i+1])); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// dump returns every key/value of bucket.
func dump(t *testing.T, db *bolt.DB, bucket string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			out[string(k)] = string(v)
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustVersion(t *testing.T, db *bolt.DB) uint64 {
	t.Helper()
	v, err := Version(db)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFreshFileIsStampedWithTheLatestVersion(t *testing.T) {
	db, _ := openTemp(t)
	if got := mustVersion(t, db); got != 0 {
		t.Fatalf("a file never migrated reads as version %d, want 0", got)
	}
	if err := Run(db); err != nil {
		t.Fatal(err)
	}
	if got := mustVersion(t, db); got != Latest() {
		t.Fatalf("version after Run = %d, want %d", got, Latest())
	}
}

func TestStepsRunInOrderOnceEach(t *testing.T) {
	db, _ := openTemp(t)
	var ran []string
	steps := []Step{
		{Version: 1, Name: "one", Run: func(*bolt.DB) error { ran = append(ran, "one"); return nil }},
		{Version: 2, Name: "two", Run: func(*bolt.DB) error { ran = append(ran, "two"); return nil }},
	}
	if err := setVersion(db, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := run(db, steps); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(ran, ",") != "two" {
		t.Fatalf("steps run = %v, want only the one newer than the file, once", ran)
	}
	if got := mustVersion(t, db); got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}
}

func TestFailedStepLeavesTheVersionSoItRunsAgain(t *testing.T) {
	db, _ := openTemp(t)
	boom := errors.New("boom")
	fail := true
	steps := []Step{{Version: 1, Name: "flaky", Run: func(*bolt.DB) error {
		if fail {
			return boom
		}
		return nil
	}}}
	if err := run(db, steps); !errors.Is(err, boom) {
		t.Fatalf("run = %v, want the step's error", err)
	}
	if got := mustVersion(t, db); got != 0 {
		t.Fatalf("a failed step recorded version %d", got)
	}
	fail = false
	if err := run(db, steps); err != nil {
		t.Fatal(err)
	}
	if got := mustVersion(t, db); got != 1 {
		t.Fatalf("version after the retry = %d, want 1", got)
	}
}

func TestNewerFileIsLeftAlone(t *testing.T) {
	db, _ := openTemp(t)
	if err := setVersion(db, Latest()+5); err != nil {
		t.Fatal(err)
	}
	put(t, db, "bykey", "bykey|c|README.md|en", "c|readme.md|en")
	if err := Run(db); err != nil {
		t.Fatal(err)
	}
	if _, ok := dump(t, db, "bykey")["bykey|c|README.md|en"]; !ok {
		t.Fatal("a file from a newer release was rewritten")
	}
	if got := mustVersion(t, db); got != Latest()+5 {
		t.Fatalf("the newer version was lowered to %d", got)
	}
}

func TestVersionBucketWithoutAVersionReadsAsZero(t *testing.T) {
	db, _ := openTemp(t)
	put(t, db, string(Bucket), "other", "x")
	if got := mustVersion(t, db); got != 0 {
		t.Fatalf("version = %d, want 0", got)
	}
}

func TestMalformedVersionIsAnError(t *testing.T) {
	db, _ := openTemp(t)
	put(t, db, string(Bucket), string(versionKey), "abc")
	if err := Run(db); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("Run on a malformed version = %v, want an error naming it", err)
	}
}

// readOnlyCopy returns a read-only handle on a file holding one old-format
// key-index entry: its version reads, nothing can be written to it.
func readOnlyCopy(t *testing.T) *bolt.DB {
	t.Helper()
	db, path := openTemp(t)
	put(t, db, "bykey", "bykey|c|README.md|en", "c|readme.md|en")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return ro
}

func TestUnwritableFileReportsWhichPartFailed(t *testing.T) {
	if err := Run(readOnlyCopy(t)); err == nil || !strings.Contains(err.Error(), "schema step 1") {
		t.Fatalf("a step that cannot write = %v, want an error naming the step", err)
	}
	noop := []Step{{Version: 1, Name: "noop", Run: func(*bolt.DB) error { return nil }}}
	if err := run(readOnlyCopy(t), noop); err == nil || !strings.Contains(err.Error(), "recording schema version") {
		t.Fatalf("a version that cannot be recorded = %v, want an error saying so", err)
	}
}
