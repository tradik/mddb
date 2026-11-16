package binlog

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opening logs (GO-050): one written by 2.19.7, one closed cleanly, one
// that was not, and sidecars that cannot be trusted.

// legacyLog puts the 2.19.7 fixture at dir/mddb.binlog and returns its bytes.
// testdata/legacy_binlog_2_19_7.hex was written by the binlog package of
// 2.19.7 (commit 96a2b023): six entries, LSN 1 to 6, one of every type.
func legacyLog(t *testing.T, dir string) []byte {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", "legacy_binlog_2_19_7.hex"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(text)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mddb.binlog"), raw, 0o600); err != nil { // #nosec G703 -- t.TempDir() path
		t.Fatal(err)
	}
	return raw
}

func openIn(t *testing.T, dir string, cfg BinlogConfig) *Binlog {
	t.Helper()
	bl, err := NewBinlog(filepath.Join(dir, "x.db"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return bl
}

func TestA2197LogOpensAsItIsAndStaysReadable(t *testing.T) {
	dir := t.TempDir()
	raw := legacyLog(t, dir)
	bl := openIn(t, dir, BinlogConfig{})
	entries, err := bl.ReadFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []BinlogEntryType{BinlogPut, BinlogPut, BinlogDelete, BinlogDeleteBucket, BinlogPut, BinlogCheckpoint}
	if len(entries) != len(want) || bl.CurrentLSN() != 6 || bl.BaseLSN() != 0 {
		t.Fatalf("read %d entries, at LSN %d from %d", len(entries), bl.CurrentLSN(), bl.BaseLSN())
	}
	for i, e := range entries {
		if e.LSN != uint64(i+1) || e.Type != want[i] {
			t.Errorf("entry %d: LSN %d type %v", i, e.LSN, e.Type)
		}
	}
	if string(entries[0].Value) != `{"key":"hello"}` {
		t.Errorf("value %q", entries[0].Value)
	}
	if err := bl.Close(); err != nil {
		t.Fatal(err)
	}
	// The file is still nothing but entries in the old layout: a release
	// before 2.19.8 reads it as it wrote it.
	got, _ := os.ReadFile(filepath.Join(dir, "mddb.binlog")) // #nosec G304 -- t.TempDir() path
	if !bytes.Equal(got, raw) {
		t.Error("opening and closing the log changed its file")
	}
	// And it continues on the next start: closed cleanly, same ID.
	id := bl.LogID()
	again := openIn(t, dir, BinlogConfig{})
	defer func() { _ = again.Close() }()
	if again.LogID() != id || again.CurrentLSN() != 6 {
		t.Errorf("reopened as log %x at %d; was %x at 6", again.LogID(), again.CurrentLSN(), id)
	}
}

func TestALogNotClosedStartsANewOne(t *testing.T) {
	dir := t.TempDir()
	bl := openIn(t, dir, BinlogConfig{Sync: SyncCommit})
	appendN(t, bl, 5)
	id := bl.LogID()
	// No Close: the process died. The sidecar still says "open".
	_ = bl.file.Close()
	close(bl.done)

	again := openIn(t, dir, BinlogConfig{})
	defer func() { _ = again.Close() }()
	if again.LogID() == id || again.CurrentLSN() != 5+resetJump || again.OldestLSN() != 0 {
		t.Fatalf("after a crash: log %x at %d (oldest %d); want a new log at %d",
			again.LogID(), again.CurrentLSN(), again.OldestLSN(), uint64(5+resetJump))
	}
	if _, err := again.ReadFrom(5); !errors.Is(err, ErrBinlogLSNTooOld) {
		t.Errorf("a follower of the old log was not sent to a snapshot: %v", err)
	}
}

func TestSidecarsThatCannotBeTrusted(t *testing.T) {
	cases := map[string]func(t *testing.T, meta string){
		"unreadable": func(t *testing.T, meta string) { writeFile(t, meta, "{not json") },
		"closed elsewhere": func(t *testing.T, meta string) {
			writeMeta(t, meta, logMeta{Version: 1, EntryFormat: 1, LogID: "00000000000000aa", State: stateClean, CleanLSN: 2})
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bl := openIn(t, dir, BinlogConfig{})
			appendN(t, bl, 3)
			if err := bl.Close(); err != nil {
				t.Fatal(err)
			}
			spoil(t, bl.metaPath())
			again := openIn(t, dir, BinlogConfig{})
			defer func() { _ = again.Close() }()
			if again.CurrentLSN() < resetJump {
				t.Errorf("a log whose sidecar is %s was continued", name)
			}
		})
	}
}

func TestASidecarFromANewerReleaseIsRefused(t *testing.T) {
	for _, m := range []logMeta{{Version: metaVersion + 1}, {Version: 1, EntryFormat: entryFormatVersion + 1}} {
		dir := t.TempDir()
		writeMeta(t, filepath.Join(dir, "mddb.binlog.meta"), m)
		if _, err := NewBinlog(filepath.Join(dir, "x.db"), BinlogConfig{}); err == nil || !strings.Contains(err.Error(), "newer release") {
			t.Errorf("%+v: %v", m, err)
		}
	}
}

func TestAFollowersCopyIsNeverJudgedAndKeepsNoID(t *testing.T) {
	dir := t.TempDir()
	legacyLog(t, dir)
	bl := openIn(t, dir, BinlogConfig{Replica: true})
	if bl.LogID() != 0 || bl.CurrentLSN() != 6 {
		t.Fatalf("follower copy: log %x at %d", bl.LogID(), bl.CurrentLSN())
	}
	// Not closed — and still continued, at the position it has.
	_ = bl.file.Close()
	close(bl.done)
	again := openIn(t, dir, BinlogConfig{Replica: true})
	defer func() { _ = again.Close() }()
	if again.CurrentLSN() != 6 {
		t.Errorf("follower copy reopened at %d", again.CurrentLSN())
	}
}

func TestAnEmptyLogContinuesFromItsSidecar(t *testing.T) {
	dir := t.TempDir()
	bl := openIn(t, dir, BinlogConfig{})
	if err := bl.Rebase(40, 7); err != nil {
		t.Fatal(err)
	}
	if err := bl.Close(); err != nil {
		t.Fatal(err)
	}
	again := openIn(t, dir, BinlogConfig{})
	defer func() { _ = again.Close() }()
	if again.CurrentLSN() != 40 || again.BaseLSN() != 40 || again.LogID() != 7 {
		t.Errorf("reopened at %d from %d as %x", again.CurrentLSN(), again.BaseLSN(), again.LogID())
	}
	appendN(t, again, 1)
	if e, _ := again.ReadFrom(40); len(e) != 1 || e[0].LSN != 41 {
		t.Errorf("the next entry: %+v", e)
	}
}

func TestStatsDescribeTheLog(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: 1 << 20, Sync: SyncCommit})
	defer func() { _ = bl.Close() }()
	appendN(t, bl, 2)
	st := bl.Stats()
	if st.CurrentLSN != 2 || st.OldestLSN != 1 || st.MaxSize != 1<<20 || st.Sync != "commit" ||
		st.LogID != FormatLogID(bl.LogID()) || st.MaxAgeSeconds != 86400 || st.FileSize == 0 {
		t.Errorf("stats: %+v", st)
	}
	if parseLogID(st.LogID) != bl.LogID() || parseLogID("zz") != 0 {
		t.Error("log IDs do not round-trip")
	}
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMeta(t *testing.T, path string, m logMeta) {
	t.Helper()
	raw, _ := json.Marshal(m)
	writeFile(t, path, string(raw))
}
