package binlog

import (
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// Writing to the log in the order the database commits (GO-051).
//
// A write used to commit its transaction and append its entries afterwards,
// outside any lock. Two writers of one key could commit A then B and append
// B then A: the leader kept B, a follower replaying the log kept A, and an
// election, which trusts the order of the log, would have built on that.
//
// Logged appends inside the transaction instead, as its last step. bbolt
// lets one write transaction run at a time per file, so the order entries
// take their LSNs in is the order the transactions commit in. The append
// goes to the write buffer; with MDDB_BINLOG_SYNC=commit the sync waits until
// the transaction has committed and no longer holds the file, as it did
// before. Of the two kinds of failure, only one can leave the log and the
// database apart: a commit that fails after its entries were appended. bbolt
// fails a commit only when it cannot write its file, and the log then holds
// entries the database does not; it is stopped, as after its own write
// errors, and the next start begins a new log (followers resynchronise).

// ErrNotWritable is what a WriteGate returns when the node may not write.
var ErrNotWritable = errors.New("this node does not take writes")

// WriteGate says whether the node may append to its log now: nil when it
// may. A cluster follower's gate refuses (GO-051).
type WriteGate func() error

// AckWaiter holds a logged write until it is acknowledged as configured
// (MDDB_REPLICATION_ACK=quorum), given the write's last LSN.
type AckWaiter func(lsn uint64) error

// SetWriteGate installs gate; nil removes it.
func (b *Binlog) SetWriteGate(gate WriteGate) {
	if gate == nil {
		b.gate.Store(nil)
		return
	}
	b.gate.Store(&gate)
}

// EntryGate says whether the node may log a write of these entries: nil
// when it may. A node of a placement refuses writes to collections another
// group holds, or that a move is switching over (GO-052).
type EntryGate func(entries []*BinlogEntry) error

// SetEntryGate installs gate; nil removes it.
func (b *Binlog) SetEntryGate(gate EntryGate) {
	if gate == nil {
		b.entryGate.Store(nil)
		return
	}
	b.entryGate.Store(&gate)
}

// SetAckWaiter installs wait; nil removes it.
func (b *Binlog) SetAckWaiter(wait AckWaiter) {
	if wait == nil {
		b.ackWait.Store(nil)
		return
	}
	b.ackWait.Store(&wait)
}

func (b *Binlog) checkGate() error {
	if g := b.gate.Load(); g != nil {
		return (*g)()
	}
	return nil
}

// Logged runs a write transaction through update — db.Update, or a server's
// routing to the right file — with fn collecting the transaction's entries
// in bo, and writes them to bl as the transaction's last step, in commit
// order. With a nil bl it is update(fn) and nothing is logged.
func Logged(update func(func(*bolt.Tx) error) error, bl *Binlog, fn func(*bolt.Tx, *BinlogOps) error) error {
	var bo BinlogOps
	err := update(bo.InTx(bl, func(tx *bolt.Tx) error { return fn(tx, &bo) }))
	return bo.Settle(bl, err)
}

// InTx is fn, collecting into bo, followed by writing what bo collected to
// bl: the transaction function of a logged write whose caller holds bo
// itself. The caller passes the transaction's result to Settle.
func (bo *BinlogOps) InTx(bl *Binlog, fn func(*bolt.Tx) error) func(*bolt.Tx) error {
	return func(tx *bolt.Tx) error {
		bo.entries, bo.staged = bo.entries[:0], false
		if err := fn(tx); err != nil {
			return err
		}
		return bo.stage(bl)
	}
}

// stage appends the collected entries, unsynced. Only the gate refuses the
// transaction: a log that is closed or has stopped on a write error takes
// the write's entries no more than it took them after the commit — the log
// has already marked itself, and the database keeps serving.
func (bo *BinlogOps) stage(bl *Binlog) error {
	if bl == nil || len(bo.entries) == 0 {
		return nil
	}
	// The gate is asked under the log's lock: once a node has stepped down,
	// its last position is final.
	bl.mu.Lock()
	defer bl.mu.Unlock()
	if err := bl.checkGate(); err != nil {
		return err
	}
	if g := bl.entryGate.Load(); g != nil {
		if err := (*g)(bo.entries); err != nil {
			return err
		}
	}
	bl.number(bo.entries)
	bo.staged = bl.writeLocked(bo.entries, false) == nil
	if bo.staged {
		bl.pending[bo.entries[0].LSN] = struct{}{}
	}
	return nil
}

// Settle finishes a logged write once its transaction has returned with
// txErr: a failed commit stops the log, a good one is synced (commit mode)
// and waited for (quorum). It returns the write's result.
func (bo *BinlogOps) Settle(bl *Binlog, txErr error) error {
	if !bo.staged {
		return txErr
	}
	last := bo.entries[len(bo.entries)-1].LSN
	bl.mu.Lock()
	delete(bl.pending, bo.entries[0].LSN)
	if txErr != nil {
		_ = bl.fail(fmt.Errorf("the log holds entries through LSN %d of a transaction that did not commit: %w", last, txErr))
	}
	bl.mu.Unlock()
	if txErr != nil {
		return txErr
	}
	if err := bl.syncThrough(last); err != nil {
		return err
	}
	if w := bl.ackWait.Load(); w != nil {
		return (*w)(last)
	}
	return nil
}

// syncThrough makes sure the entries through lsn are on disk when the log
// syncs on commit. One sync covers every write buffered before it.
func (b *Binlog) syncThrough(lsn uint64) error {
	if b.cfg.Sync != SyncCommit {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.synced >= lsn {
		return nil
	}
	if b.closed {
		return ErrBinlogClosed
	}
	return b.flush()
}

// StableLSN is the last LSN through which every entry's transaction has
// returned: the database files hold all of them. Entries are appended
// before their transaction commits, so CurrentLSN can run ahead of the
// files; a snapshot takes its position from here.
func (b *Binlog) StableLSN() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	stable := b.lsn.Load()
	for first := range b.pending {
		stable = min(stable, first-1)
	}
	return stable
}
