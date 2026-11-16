package main

import (
	"log/slog"
	"mddb/internal/binlog"
)

// The follower's start and its copy of the leader's log (GO-050).
//
// A follower resumes from the position its database holds, and keeps the
// entries it applies in a binlog of its own, under the leader's LSNs and log
// ID, so every node carries the same history. The copy is written after the
// entry is applied: a follower that stops between the two finds its copy
// behind its data, and the copy is started again at the applied position —
// it never claims an entry the database does not hold, nor misses one inside
// what it holds.

// resume loads the position from the database and opens the follower's log.
func (rc *ReplicationClient) resume() {
	if rc.cluster != nil {
		rc.resumeShared() // replication_cluster_follower.go
		return
	}
	pos, err := rc.server.loadReplicationPosition()
	if err != nil {
		slog.Warn("replication: could not read this follower's position; starting from the leader's first entry", "err", err)
	}
	rc.applier.SetPosition(pos)
	if rc.logConfig == nil {
		return
	}
	cfg := *rc.logConfig
	cfg.Replica = true
	log, err := binlog.NewBinlog(rc.server.Path, cfg)
	if err != nil {
		slog.Warn("replication: this follower's copy of the leader's log could not be opened; replicating without it", "err", err)
		return
	}
	rc.log.Store(log)
	if log.LogID() != pos.LogID || log.CurrentLSN() != pos.LSN {
		rc.rebaseLog(pos)
	}
}

// appendLog adds applied entries to the follower's log. Entries that do
// not continue the log start it again at the applied position. A cluster
// node's log is the one it would lead with, and is not started again: the
// error ends replication and the node takes a snapshot.
func (rc *ReplicationClient) appendLog(entries []*binlog.BinlogEntry) error {
	log := rc.log.Load()
	if log == nil {
		return nil
	}
	err := log.AppendReplicated(entries)
	if err == nil || rc.cluster != nil {
		return err
	}
	slog.Warn("replication: this follower's log does not continue; starting it again at the applied position", "err", err)
	rc.rebaseLog(rc.applier.Position())
	return nil
}

// rebaseLog makes the follower's log an empty one continuing after pos.
func (rc *ReplicationClient) rebaseLog(pos replPosition) {
	log := rc.log.Load()
	if log == nil {
		return
	}
	if err := log.RebaseAt(pos.LSN, pos.LogID, pos.Term); err != nil {
		slog.Warn("replication: could not restart this follower's log", "err", err)
	}
}
