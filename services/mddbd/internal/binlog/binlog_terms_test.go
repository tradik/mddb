package binlog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// GO-051: election terms in the log.

func entries(lsn, term uint64, n int) []*BinlogEntry {
	out := make([]*BinlogEntry, n)
	for i := range out {
		out[i] = &BinlogEntry{LSN: lsn + uint64(i), Term: term, Type: BinlogPut, BucketName: "b", Key: []byte("k")} // #nosec G115 -- test
	}
	return out
}

func TestTermsOfALeaderAndAFollower(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	cfg := BinlogConfig{Replica: true, RetentionInterval: -1}
	bl, err := NewBinlog(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bl.LogID() != 0 {
		t.Fatal("a fresh replica log has an ID")
	}
	// A follower takes terms from the entries.
	if err := bl.AppendReplicated(entries(1, 1, 3)); err != nil {
		t.Fatal(err)
	}
	if err := bl.AppendReplicated(entries(4, 2, 2)); err != nil {
		t.Fatal(err)
	}
	if err := bl.AppendReplicated(entries(6, 0, 1)); err != nil { // no term: as before
		t.Fatal(err)
	}
	if bl.TermAt(3) != 1 || bl.TermAt(4) != 2 || bl.TermAt(0) != 0 || bl.Term() != 2 {
		t.Fatalf("terms %v", bl.Terms())
	}
	// Elected: its own appends carry the new term, and the log gets an ID.
	if err := bl.SetTerm(3); err != nil {
		t.Fatal(err)
	}
	if err := bl.SetTerm(3); err != nil { // again: nothing changes
		t.Fatal(err)
	}
	if bl.LogID() == 0 {
		t.Fatal("a leading log has no ID")
	}
	if err := bl.Append(&BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte("k")}); err != nil {
		t.Fatal(err)
	}
	if lsn, term := bl.LastPosition(); lsn != 7 || term != 3 {
		t.Fatalf("last %d/%d", lsn, term)
	}
	_ = bl.Close()

	// The terms are in the sidecar; a dirty open keeps them and says so.
	markOpen(t, path)
	bl, err = NewBinlog(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	if !bl.Dirty() || bl.Term() != 3 || len(bl.Terms()) != 3 {
		t.Fatalf("reopened: dirty %v term %d %v", bl.Dirty(), bl.Term(), bl.Terms())
	}
	// Jump: far ahead, same ID and history, no longer dirty.
	id := bl.LogID()
	if err := bl.Jump(); err != nil {
		t.Fatal(err)
	}
	if bl.Dirty() || bl.LogID() != id || bl.CurrentLSN() != 7+resetJump || bl.TermAt(7) != 3 {
		t.Fatalf("after the jump: %d %v", bl.CurrentLSN(), bl.Terms())
	}
	// A snapshot rebases with the term of its LSN, or none.
	if err := bl.RebaseAt(100, 9, 5); err != nil || bl.TermAt(100) != 5 || bl.LogID() != 9 {
		t.Fatalf("rebase: %v %v", err, bl.Terms())
	}
	if err := bl.RebaseAt(200, 9, 0); err != nil || len(bl.Terms()) != 0 {
		t.Fatalf("rebase without a term: %v %v", err, bl.Terms())
	}
}

// markOpen makes the sidecar say the log was not closed.
func markOpen(t *testing.T, dbPath string) {
	t.Helper()
	meta := filepath.Join(filepath.Dir(dbPath), "mddb.binlog.meta")
	m, status, err := loadMeta(meta)
	if err != nil || status != metaLoaded {
		t.Fatal(err)
	}
	m.State = stateOpen
	b := &Binlog{path: filepath.Join(filepath.Dir(dbPath), "mddb.binlog"), logID: parseLogID(m.LogID), baseLSN: m.BaseLSN, terms: m.Terms}
	b.lsn.Store(m.CleanLSN)
	if err := b.saveMeta(stateOpen); err != nil {
		t.Fatal(err)
	}
}

func TestALeaderLogNotClosedStartsANewLogInItsTerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	bl, err := NewBinlog(path, BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	if err := bl.SetTerm(4); err != nil {
		t.Fatal(err)
	}
	_ = bl.Close()
	markOpen(t, path)
	bl, err = NewBinlog(path, BinlogConfig{RetentionInterval: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bl.Close() }()
	if ts := bl.Terms(); len(ts) != 1 || ts[0].Term != 4 || ts[0].Start != bl.CurrentLSN()+1 {
		t.Fatalf("new log terms %v at %d", ts, bl.CurrentLSN())
	}
}

func TestTermWriteFailures(t *testing.T) {
	bl, cleanup := newTestBinlog(t)
	defer cleanup()
	inject(t, func(o *fileOps) { o.openFile = failAfter(0) })
	if err := bl.AppendReplicated(entries(1, 7, 1)); !errors.Is(err, errInjected) {
		t.Fatalf("a term that cannot be recorded: %v", err)
	}
	inject(t, func(o *fileOps) {
		o.openFile = os.OpenFile
		o.truncate = func(string, int64) error { return errInjected }
	})
	if err := bl.Jump(); !errors.Is(err, errInjected) {
		t.Fatalf("jump: %v", err)
	}
	if err := bl.RebaseAt(5, 1, 1); !errors.Is(err, errInjected) {
		t.Fatalf("rebase: %v", err)
	}
}

func TestCommonPrefix(t *testing.T) {
	leader := []TermStart{{Term: 1, Start: 1}, {Term: 2, Start: 11}, {Term: 4, Start: 21}}
	cases := []struct {
		name     string
		follower []TermStart
		last     uint64
		want     uint64
	}{
		{"a prefix", []TermStart{{Term: 1, Start: 1}, {Term: 2, Start: 11}}, 15, 15},
		{"behind in an old term", []TermStart{{Term: 1, Start: 1}}, 5, 5},
		{"a term the leader never had", []TermStart{{Term: 1, Start: 1}, {Term: 2, Start: 11}, {Term: 3, Start: 18}}, 25, 17},
		{"parted in the first term", []TermStart{{Term: 5, Start: 1}}, 9, 0},
		{"before any election", nil, 0, 0},
	}
	for _, c := range cases {
		if got := CommonPrefix(leader, 40, c.follower, c.last); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	if TermAtLSN(nil, 5) != 0 || segmentStart([]TermStart{{Term: 1, Start: 0}}, 3) != 1 {
		t.Fatal("helpers")
	}
}
