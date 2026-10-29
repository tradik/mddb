package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// writeDBWithFreePages leaves a database whose persisted freelist is not
// empty: keys written in one transaction, most deleted in the next.
func writeDBWithFreePages(t *testing.T, path string) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	put := func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("docs"))
		if err != nil {
			return err
		}
		for i := 0; i < 2000; i++ {
			if err := b.Put([]byte(fmt.Sprintf("key-%06d", i)), []byte(strings.Repeat("v", 100))); err != nil {
				return err
			}
		}
		return nil
	}
	drop := func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		for i := 0; i < 1900; i++ {
			if err := b.Delete([]byte(fmt.Sprintf("key-%06d", i))); err != nil {
				return err
			}
		}
		return nil
	}
	for _, step := range []func(*bolt.Tx) error{put, drop} {
		if err := db.Update(step); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// editFreelistPage rewrites the page the current meta names as the freelist.
func editFreelistPage(t *testing.T, path string, edit func(page []byte)) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: a file this test created
	if err != nil {
		t.Fatal(err)
	}
	// Meta pages 0 and 1: page header (16 bytes), magic, version, pageSize,
	// flags, root bucket (16), freelist pgid at 48, pgid, txid at 64.
	pageSize := int(binary.LittleEndian.Uint32(data[24:]))
	meta := 0
	if binary.LittleEndian.Uint64(data[pageSize+64:]) > binary.LittleEndian.Uint64(data[64:]) {
		meta = pageSize
	}
	freelist := int(binary.LittleEndian.Uint64(data[meta+48:])) * pageSize // #nosec G115 -- a page id in a small file this test wrote
	edit(data[freelist : freelist+pageSize])
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // G703: a file this test created
		t.Fatal(err)
	}
}

// A freelist that forgets a free page is damage bbolt's check reports, not a
// panic: the child prints bbolt's words and exits 1.
func TestVerifyChildReportsAPageTheFreelistLost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lost.db")
	writeDBWithFreePages(t, path)
	editFreelistPage(t, path, func(page []byte) {
		count := binary.LittleEndian.Uint16(page[10:])
		if count == 0 || count == 0xFFFF {
			t.Fatalf("freelist count %d: the test needs a short, non-empty freelist", count)
		}
		binary.LittleEndian.PutUint16(page[10:], count-1)
	})

	if code := runVerifyChild(path); code != 1 {
		t.Fatalf("checking a database with a lost free page exited %d, want 1", code)
	}
	if err := verifyDatabase(path); err == nil || !strings.Contains(err.Error(), "unreachable unfreed") {
		t.Fatalf("err = %v, want bbolt's unreachable-unfreed report", err)
	}
}

// A page named as the freelist that is not one is refused on open with
// bbolt's words (patched in third_party/bbolt: it used to panic).
func TestOpenBoltRefusesAFreelistPageThatIsNotOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notfree.db")
	writeDBWithFreePages(t, path)
	editFreelistPage(t, path, func(page []byte) {
		const leafFlag = 0x02
		binary.LittleEndian.PutUint16(page[8:], leafFlag)
	})

	_, err := openBolt(path, &bolt.Options{ReadOnly: true, PreLoadFreelist: true, FreelistType: bolt.FreelistMapType})
	if err == nil || !strings.Contains(err.Error(), "invalid freelist page") {
		t.Fatalf("err = %v, want the freelist page refused", err)
	}
}

// A binary that cannot find itself cannot start the check's child process.
func TestVerifyDatabaseWithoutItsOwnExecutable(t *testing.T) {
	saved := currentExecutable
	t.Cleanup(func() { currentExecutable = saved })
	currentExecutable = func() (string, error) { return "", errors.New("no /proc") }

	if err := verifyDatabase(filepath.Join(t.TempDir(), "any.db")); err == nil || !strings.Contains(err.Error(), "cannot run the integrity check: no /proc") {
		t.Fatalf("err = %v", err)
	}
}

// A check that outlives MDDB_VERIFY_TIMEOUT is reported as such, not as damage.
func TestVerifyDatabaseThatTimesOut(t *testing.T) {
	t.Setenv("MDDB_VERIFY_TIMEOUT", "1ns")
	path := filepath.Join(t.TempDir(), "slow.db")
	writeTestDB(t, path, 10)
	if err := verifyDatabase(path); err == nil || !strings.Contains(err.Error(), "did not finish within") {
		t.Fatalf("err = %v, want the timeout", err)
	}
}
