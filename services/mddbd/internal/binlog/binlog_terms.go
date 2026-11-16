package binlog

import "slices"

// Election terms in the log (GO-051).
//
// Every entry is written in a term: the term of the leader that took the
// write. The file does not carry it — its layout is the one every release
// has written — so the log keeps where each term begins, a short list in the
// sidecar that grows by one entry per election that led to writes. Two logs
// that have the same term at the same LSN hold the same entries up to it:
// one leader per term wrote them. That is what a returning node is compared
// by, and where its history and the leader's part.
//
// Term 0 is a log without elections: a node outside a cluster.

// TermStart is the first LSN written in a term. A list of them, in LSN
// order, gives the term of every entry: that of the last start at or before
// it.
type TermStart struct {
	Term  uint64 `json:"term"`
	Start uint64 `json:"start"`
}

// TermAtLSN is the term of entry lsn under starts, 0 before the first.
func TermAtLSN(starts []TermStart, lsn uint64) uint64 {
	term := uint64(0)
	for _, s := range starts {
		if s.Start > lsn {
			break
		}
		term = s.Term
	}
	return term
}

// withStart adds s, dropping the starts at or after its LSN: a term that
// begins at LSN n replaces whatever this log said about n and later.
func withStart(starts []TermStart, s TermStart) []TermStart {
	keep := starts[:0:0]
	for _, old := range starts {
		if old.Start < s.Start {
			keep = append(keep, old)
		}
	}
	return append(keep, s)
}

// SetTerm makes term the one the node's own appends carry from now on — a
// node that has become leader. The term begins after the last entry; the
// sidecar records it before the first such entry can be written.
func (b *Binlog) SetTerm(term uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if term == b.term && TermAtLSN(b.terms, b.lsn.Load()+1) == term && b.logID != 0 {
		return nil
	}
	if b.logID == 0 {
		// A cluster node's log gets its ID from the leader it follows; a
		// node that leads before it ever followed names the log itself.
		b.logID = newLogID()
	}
	b.term = term
	b.terms = withStart(b.terms, TermStart{Term: term, Start: b.lsn.Load() + 1})
	return b.saveMeta(stateOpen)
}

// Term is the term the node's own appends carry.
func (b *Binlog) Term() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.term
}

// TermAt is the term entry lsn was written in; 0 when the log does not know.
func (b *Binlog) TermAt(lsn uint64) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return TermAtLSN(b.terms, lsn)
}

// LastPosition is the last entry's LSN and term, which is what an election
// compares logs by.
func (b *Binlog) LastPosition() (lsn, term uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lsn = b.lsn.Load()
	return lsn, TermAtLSN(b.terms, lsn)
}

// Terms is a copy of where each term begins.
func (b *Binlog) Terms() []TermStart {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.terms)
}

// noteTermsLocked records the terms that replicated entries begin, before
// they are written. The caller holds b.mu.
func (b *Binlog) noteTermsLocked(entries []*BinlogEntry) error {
	changed := false
	for _, e := range entries {
		if e.Term != 0 && e.Term != TermAtLSN(b.terms, e.LSN) {
			b.terms = withStart(b.terms, TermStart{Term: e.Term, Start: e.LSN})
			changed = true
		}
		b.term = max(b.term, e.Term)
	}
	if !changed {
		return nil
	}
	return b.saveMeta(stateOpen)
}

// RebaseAt is Rebase for a log whose entry lsn — the last one before the
// empty file — was written in term: a follower's copy after a snapshot
// taken at lsn.
func (b *Binlog) RebaseAt(lsn, id, term uint64) error {
	b.scanMu.Lock()
	defer b.scanMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.restartAt(lsn, id); err != nil {
		return err
	}
	b.terms = nil
	if term != 0 {
		b.terms = []TermStart{{Term: term, Start: lsn}}
		b.term = max(b.term, term)
	}
	return b.saveMeta(stateOpen)
}

// CommonPrefix is the last LSN up to which two logs hold the same entries:
// one with term starts a ending at aLast, the other with b ending at bLast.
// One leader wrote each term, so two logs with the same term at an LSN agree
// on every entry up to it; the answer is the highest LSN, no later than
// either log's end, where their terms are the same. Entries written before
// any election have term 0 in both and count as shared — the log ID says
// whether they are the same log.
func CommonPrefix(a []TermStart, aLast uint64, b []TermStart, bLast uint64) uint64 {
	x := min(aLast, bLast)
	for x > 0 && TermAtLSN(a, x) != TermAtLSN(b, x) {
		// Neither term changes between the later of the two starts and x,
		// so they differ all the way down to it.
		x = max(segmentStart(a, x), segmentStart(b, x)) - 1
	}
	return x
}

// segmentStart is the first LSN of the term entry x is in; 1 before any.
func segmentStart(starts []TermStart, x uint64) uint64 {
	start := uint64(1)
	for _, s := range starts {
		if s.Start > x {
			break
		}
		start = max(s.Start, 1)
	}
	return start
}

// Jump empties the log and continues it far ahead of its last LSN, under
// the same ID and with its term history. Every follower's position then
// falls before the new base, and it takes a snapshot. A cluster node whose
// log was not closed cleanly does this before it takes writes as leader:
// its database may hold writes the log lost, which no follower would
// otherwise receive. The history stays, so a node whose log parted from
// this one's is still told where.
func (b *Binlog) Jump() error {
	b.scanMu.Lock()
	defer b.scanMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.restartAt(b.lsn.Load()+resetJump, b.logID); err != nil {
		return err
	}
	return b.saveMeta(stateOpen)
}

// Dirty reports that the database may hold writes this log does not: the
// log was found not closed cleanly when it was opened (the process died),
// or its gate refused a write that had already been committed (a write made
// outside Logged on a node that had stopped leading). A follower's copy is
// opened without being judged for it, and says so here.
func (b *Binlog) Dirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dirty || b.unlogged
}
