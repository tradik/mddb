package binlog

import (
	"errors"
	"testing"
	"time"
)

// Retention (GO-050): size and age limits that are enforced, a floor for
// what followers still need, and a hard limit above it.

func entrySize() int64 {
	return int64(len(MarshalBinlogEntry(&BinlogEntry{Type: BinlogPut, BucketName: "b", Key: []byte{0}, Value: []byte("v")})))
}

func TestRetentionKeepsTheLogUnderItsSize(t *testing.T) {
	size := entrySize()
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: 10 * size, RetentionInterval: -1})
	defer func() { _ = bl.Close() }()
	appendN(t, bl, 30)
	res, err := bl.EnforceRetention()
	if err != nil || res.DroppedThrough != 25 || res.Forced {
		t.Fatalf("retention: %+v, %v", res, err)
	}
	if bl.Stats().FileSize != 5*size || bl.OldestLSN() != 26 {
		t.Errorf("left %d bytes from LSN %d", bl.Stats().FileSize, bl.OldestLSN())
	}
	if e, err := bl.ReadFrom(25); err != nil || len(e) != 5 || e[0].LSN != 26 {
		t.Errorf("what is left: %d entries, %v", len(e), err)
	}
	// Under the limit, a pass does nothing.
	if res, _ := bl.EnforceRetention(); res.DroppedThrough != 0 {
		t.Errorf("second pass dropped through %d", res.DroppedThrough)
	}
}

func TestRetentionKeepsWhatAFollowerNeedsUpToTheHardLimit(t *testing.T) {
	size := entrySize()
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: 10 * size, HardMaxSize: 20 * size, RetentionInterval: -1})
	defer func() { _ = bl.Close() }()
	bl.SetRetentionFloor(func() (uint64, bool) { return 12, true })
	appendN(t, bl, 18)
	if res, _ := bl.EnforceRetention(); res.DroppedThrough != 12 || res.Forced {
		t.Fatalf("with a follower at 12: %+v", res)
	}
	appendN(t, bl, 20) // 26 entries after the floor: past the hard limit
	res, err := bl.EnforceRetention()
	if err != nil || !res.Forced || bl.Stats().FileSize > 20*size {
		t.Fatalf("past the hard limit: %+v, %v, %d bytes", res, err, bl.Stats().FileSize)
	}
	if _, err := bl.ReadFrom(12); !errors.Is(err, ErrBinlogLSNTooOld) {
		t.Errorf("the follower behind the cut was not sent to a snapshot: %v", err)
	}
}

func TestRetentionDropsEntriesPastTheirAge(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxAge: time.Hour, RetentionInterval: -1})
	defer func() { _ = bl.Close() }()
	appendN(t, bl, 4)
	if res, _ := bl.enforce(time.Now()); res.DroppedThrough != 0 {
		t.Fatal("fresh entries dropped")
	}
	res, err := bl.enforce(time.Now().Add(2 * time.Hour))
	if err != nil || res.DroppedThrough != 4 || bl.OldestLSN() != 0 || bl.CurrentLSN() != 4 {
		t.Fatalf("aged out: %+v, %v; oldest %d current %d", res, err, bl.OldestLSN(), bl.CurrentLSN())
	}
	appendN(t, bl, 1)
	if bl.firstTS == 0 || bl.OldestLSN() != 5 {
		t.Errorf("the next entry starts the file again: oldest %d", bl.OldestLSN())
	}
}

func TestRetentionWaitsForReadersAndClosedLogs(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: entrySize(), RetentionInterval: -1})
	appendN(t, bl, 5)
	bl.scanMu.RLock()
	if res, _ := bl.EnforceRetention(); res.DroppedThrough != 0 {
		t.Error("retention rewrote the file under a reader")
	}
	bl.scanMu.RUnlock()
	_ = bl.Close()
	if res, err := bl.EnforceRetention(); res.DroppedThrough != 0 || err != nil {
		t.Errorf("retention on a closed log: %+v, %v", res, err)
	}
}

func TestRetentionRunsOnItsOwn(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{MaxSize: 2 * entrySize(), RetentionInterval: 5 * time.Millisecond})
	defer func() { _ = bl.Close() }()
	appendN(t, bl, 10)
	deadline := time.Now().Add(5 * time.Second)
	for bl.OldestLSN() == 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if bl.OldestLSN() == 1 {
		t.Error("the retention loop never ran")
	}
}

func TestAFollowersCopyTakesTheLeadersLSNs(t *testing.T) {
	bl := openIn(t, t.TempDir(), BinlogConfig{Replica: true})
	defer func() { _ = bl.Close() }()
	if err := bl.Rebase(100, 9); err != nil {
		t.Fatal(err)
	}
	if err := bl.AppendReplicated(nil); err != nil {
		t.Fatal(err)
	}
	ch := bl.Subscribe("s")
	in := []*BinlogEntry{
		{LSN: 101, Timestamp: 1111, Type: BinlogPut, BucketName: "b"},
		{LSN: 102, Timestamp: 2222, Type: BinlogDelete, BucketName: "b"},
	}
	if err := bl.AppendReplicated(in); err != nil {
		t.Fatal(err)
	}
	if (<-ch).LSN != 101 {
		t.Error("subscribers do not see replicated entries")
	}
	got, _ := bl.ReadFrom(100)
	if len(got) != 2 || got[1].LSN != 102 || got[1].Timestamp != 2222 || bl.LogID() != 9 {
		t.Fatalf("copy holds %+v as log %x", got, bl.LogID())
	}
	if err := bl.AppendReplicated([]*BinlogEntry{{LSN: 104}}); !errors.Is(err, ErrLSNGap) {
		t.Errorf("a gap was accepted: %v", err)
	}
	if ts, exact := bl.EntryTime(101); ts != 1111 || !exact {
		t.Errorf("EntryTime(101) = %d, %v", ts, exact)
	}
}

func TestTheTimeRing(t *testing.T) {
	var r timeRing
	if ts, _ := r.at(1); ts != 0 {
		t.Error("an empty ring knows a time")
	}
	for lsn := uint64(1); lsn <= timeRingSize+10; lsn++ {
		r.add(lsn, int64(lsn))
	}
	if ts, exact := r.at(5); ts != 11 || exact {
		t.Errorf("an entry older than the ring: %d, %v; want the oldest held, inexact", ts, exact)
	}
	if ts, exact := r.at(timeRingSize + 10); ts != timeRingSize+10 || !exact {
		t.Errorf("the newest: %d, %v", ts, exact)
	}
	if ts, _ := r.at(timeRingSize + 11); ts != 0 {
		t.Error("an entry not yet written has a time")
	}
	r.add(1<<40, 7) // a new log far ahead
	if ts, exact := r.at(1 << 40); ts != 7 || !exact || r.low != 1<<40 {
		t.Errorf("after a jump: %d, %v, low %d", ts, exact, r.low)
	}
}
