package binlog

import "fmt"

// A follower's copy of the log, and when an entry was written.

// AppendReplicated writes entries a follower received from its leader, with
// the leader's LSNs and timestamps, so the follower's log is the leader's
// history and not a renumbering of it. Each entry must continue the log:
// anything else is ErrLSNGap, and the caller rebases the log on what it has
// applied.
func (b *Binlog) AppendReplicated(entries []*BinlogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	b.mu.Lock()
	next := b.lsn.Load()
	for _, entry := range entries {
		next++
		if entry.LSN != next {
			b.mu.Unlock()
			return fmt.Errorf("%w: got %d, want %d", ErrLSNGap, entry.LSN, next)
		}
	}
	err := b.writeLocked(entries)
	b.mu.Unlock()
	if err != nil {
		return err
	}
	b.notifySubscribers(entries)
	return nil
}

// timeRingSize is how many of the newest entries the log remembers the
// timestamps of.
const timeRingSize = 1 << 16

// timeRing holds the timestamps of the newest entries, by LSN. LSNs are
// consecutive, so the slot is the LSN modulo the size; low is the oldest LSN
// still held.
type timeRing struct {
	ts        []int64
	low, high uint64
}

func (r *timeRing) add(lsn uint64, ts int64) {
	if r.ts == nil {
		r.ts = make([]int64, timeRingSize)
	}
	if lsn != r.high+1 || r.low == 0 {
		r.low = lsn // a new log, or the first entry
	}
	r.high = lsn
	r.ts[lsn%timeRingSize] = ts
	if lsn-r.low >= timeRingSize {
		r.low = lsn - timeRingSize + 1
	}
}

// at returns when entry lsn was written. For an entry older than the ring
// holds it returns the oldest timestamp it has, with exact false; for one
// that does not exist, 0.
func (r *timeRing) at(lsn uint64) (ts int64, exact bool) {
	if r.low == 0 || lsn > r.high {
		return 0, false
	}
	if lsn < r.low {
		return r.ts[r.low%timeRingSize], false
	}
	return r.ts[lsn%timeRingSize], true
}

// EntryTime reports when entry lsn was written, in Unix nanoseconds, from the
// timestamps of the newest 65,536 entries. An older entry gets the oldest
// timestamp held and exact false — it was written no later than that. An LSN
// past the end of the log gets 0.
func (b *Binlog) EntryTime(lsn uint64) (ts int64, exact bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.recent.at(lsn)
}
