package main

import (
	"errors"
	"log/slog"
)

// A cluster node's replication client (GO-051).
//
// It differs from a plain follower's in three ways. It appends to the
// node's own log — the one the node leads with if it is elected — so its
// position is that log's end, not a record in the database. A node whose
// log was not closed cleanly cannot vouch that its database matches it, and
// takes a snapshot before it applies anything. And when the leader says the
// node's log parted from its own (a former leader with writes nobody else
// received), the node saves the entries after the shared point to the
// rejected-writes file before the snapshot replaces its data.

// errDirtyResync ends a session to take a snapshot of the leader's data.
var errDirtyResync = errors.New("this node's log was not closed cleanly; taking a snapshot from the leader")

// resumeShared starts from the end of the node's log.
func (rc *ReplicationClient) resumeShared() {
	log := rc.cluster.s.Binlog
	rc.log.Store(log)
	rc.applier.SetPosition(replPosition{LSN: log.CurrentLSN(), LogID: log.LogID()})
	rc.dirty = log.Dirty()
}

// resyncIfDirty asks for a snapshot when the node's log cannot be trusted
// to match its database. The leader has accepted the position by now, so
// the log holds nothing the leader lacks: no entry is set aside.
func (rc *ReplicationClient) resyncIfDirty() error {
	if rc.cluster == nil || !rc.dirty {
		return nil
	}
	rc.needSnapshot.Store(true)
	return errDirtyResync
}

// setAsideDiverged saves the entries after the point where the leader's log
// and this node's part, when the leader refused the stream for it.
func (rc *ReplicationClient) setAsideDiverged(err error) {
	match, ok := divergedAt(err)
	if !ok {
		return
	}
	path, n, serr := rc.cluster.saveRejected(match)
	switch {
	case serr != nil:
		slog.Error("cluster: could not save the writes this node's log has beyond the leader's; "+
			"they are lost with the snapshot", "afterLSN", match, "err", serr)
	case n > 0:
		slog.Warn("cluster: this node's log parted from the leader's; its later writes are saved for review "+
			"and its data is replaced by a snapshot", "afterLSN", match, "entries", n, "file", path)
	}
}

// ackSoon has a cluster node acknowledge what it applied at once, which is
// what a leader waiting for a quorum counts.
func (rc *ReplicationClient) ackSoon() {
	if rc.cluster == nil {
		return
	}
	select {
	case rc.ackNow <- struct{}{}:
	default:
	}
}
