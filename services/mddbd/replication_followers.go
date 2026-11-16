package main

import (
	"log/slog"
	"mddb/internal/binlog"
	"sort"
	"time"
)

// The followers a leader knows, what each has confirmed, and how far behind
// that puts it (GO-050).
//
// A follower used to be forgotten the moment its stream ended, and its
// acknowledgements changed a status line and nothing else. Both now matter to
// retention: the lowest LSN confirmed by the followers on record is the floor
// the binlog is not trimmed below, so a follower that is behind, or away for
// a while, finds the entries it needs when it comes back. A follower that
// stays away longer than the log's age limit is dropped from the record; what
// it needed goes with the next trim, and it takes a snapshot.
//
// The record is in memory. A leader that restarts keeps entries only for the
// followers that have reconnected by the time retention runs.

// FollowerState tracks a follower: connected, or gone and still remembered.
type FollowerState struct {
	ID             string
	ConfirmedLSN   uint64
	LastSeenAt     int64
	Address        string
	Connected      bool
	DisconnectedAt time.Time
}

// trackFollower records the stream of a follower that starts at fromLSN.
func (rs *ReplicationServer) trackFollower(id, addr string, fromLSN uint64) *FollowerState {
	state := &FollowerState{
		ID:           id,
		ConfirmedLSN: fromLSN,
		LastSeenAt:   time.Now().Unix(),
		Address:      addr,
		Connected:    true,
	}
	rs.mu.Lock()
	rs.followers[id] = state
	rs.mu.Unlock()
	return state
}

// releaseFollower marks the follower of a finished stream as gone. A follower
// that reconnected has a newer record under the same ID, which is left alone:
// removing by ID let the old stream's cleanup take the new stream's record,
// and status showed no follower while data flowed (#267).
func (rs *ReplicationServer) releaseFollower(state *FollowerState) {
	rs.mu.Lock()
	if rs.followers[state.ID] == state {
		state.Connected = false
		state.DisconnectedAt = time.Now()
	}
	rs.mu.Unlock()
	slog.Info("Replication follower disconnected from binlog stream", "followerID", state.ID)
}

// retentionFloor is the lowest LSN confirmed by the followers on record, for
// the binlog's retention. It is also where followers gone for longer than
// the log keeps entries are taken off the record.
func (rs *ReplicationServer) retentionFloor() (uint64, bool) {
	hold := time.Duration(rs.server.Binlog.Stats().MaxAgeSeconds) * time.Second
	rs.mu.Lock()
	defer rs.mu.Unlock()
	floor, held := uint64(0), false
	for id, fs := range rs.followers {
		if !fs.Connected && time.Since(fs.DisconnectedAt) > hold {
			slog.Warn("replication follower has been gone longer than the binlog keeps entries; "+
				"it resynchronises from a snapshot if it returns after the next trim",
				"followerID", id, "confirmedLSN", fs.ConfirmedLSN, "goneFor", time.Since(fs.DisconnectedAt).Round(time.Second))
			delete(rs.followers, id)
			continue
		}
		if !held || fs.ConfirmedLSN < floor {
			floor, held = fs.ConfirmedLSN, true
		}
	}
	return floor, held
}

// followerViews is the record as the status endpoints show it, by ID.
func (rs *ReplicationServer) followerViews() []FollowerInfoJSON {
	bl := rs.server.Binlog
	now := time.Now()
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make([]FollowerInfoJSON, 0, len(rs.followers))
	for _, fs := range rs.followers {
		lag := measureLag(bl, fs.ConfirmedLSN, now)
		out = append(out, FollowerInfoJSON{
			FollowerID:   fs.ID,
			Address:      fs.Address,
			ConfirmedLSN: fs.ConfirmedLSN,
			LagMs:        lag.Ms,
			LagEntries:   lag.Entries,
			LagExact:     lag.Exact,
			LastSeenAt:   fs.LastSeenAt,
			Connected:    fs.Connected,
			Status:       followerStatus(fs.Connected, lag.Ms),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FollowerID < out[j].FollowerID })
	return out
}

// Lag at which a follower is shown as behind, and as far behind.
const (
	lagWarningMs   = 1000
	lagUnhealthyMs = 30000
)

func followerStatus(connected bool, lagMs int64) string {
	switch {
	case !connected:
		return "disconnected"
	case lagMs > lagUnhealthyMs:
		return "unhealthy"
	case lagMs > lagWarningMs:
		return "warning"
	}
	return "healthy"
}

// replicationLag is how far a follower is behind its leader.
type replicationLag struct {
	Entries uint64 // entries written that it has not confirmed
	Ms      int64  // age of the oldest of them
	Exact   bool   // false: Ms is a lower bound
}

// measureLag compares what a follower confirmed with the leader's log.
//
// The time is the age of the oldest entry the follower has not confirmed,
// read from that entry's own timestamp — not the LSN difference times ten,
// which is what this used to report. The log remembers the timestamps of its
// newest 65,536 entries; for a follower further behind than that the oldest
// of them is used, which makes the figure a lower bound, and Exact says so.
func measureLag(bl *binlog.Binlog, confirmed uint64, now time.Time) replicationLag {
	if bl == nil {
		return replicationLag{Exact: true}
	}
	current := bl.CurrentLSN()
	if confirmed >= current {
		return replicationLag{Exact: true}
	}
	lag := replicationLag{Entries: current - confirmed}
	ts, exact := bl.EntryTime(confirmed + 1)
	if ts == 0 {
		// Written before this process started and beyond what was read
		// back: there is nothing to measure the age from.
		return lag
	}
	lag.Exact = exact
	if age := now.UnixNano() - ts; age > 0 {
		lag.Ms = age / int64(time.Millisecond)
	}
	return lag
}
