package main

import (
	"mddb/internal/binlog"
	proto "mddb/proto"
	"net/http"
	"time"
)

// Replication status, the same over HTTP (/v1/replication/status) and gRPC
// (MDDBReplication.ReplicationStatus).
//
// GO-050: what it reports is measured. healthy was the constant true,
// leader_addr and replication_lag_ms were declared and never set, a
// follower's current_lsn was 0 because it read a binlog a follower did not
// have, and a follower's lag was the LSN difference times ten.

// ReplicationStatusResponse is the JSON response for /v1/replication/status
type ReplicationStatusResponse struct {
	NodeID string `json:"node_id"`
	Role   string `json:"role"` // leader, follower, standalone
	// CurrentLSN is the last LSN written on a leader and the last LSN
	// applied on a follower.
	CurrentLSN    uint64 `json:"current_lsn"`
	LogID         string `json:"log_id,omitempty"`
	BinlogOldest  uint64 `json:"binlog_oldest_lsn,omitempty"`
	BinlogSize    int64  `json:"binlog_size_bytes,omitempty"`
	BinlogMaxSize int64  `json:"binlog_max_size_bytes,omitempty"`
	BinlogSync    string `json:"binlog_sync,omitempty"`
	// Follower: where the leader is, whether the stream from it is open, and
	// how far behind the leader last measured this node to be.
	LeaderAddr     string `json:"leader_addr,omitempty"`
	Connected      bool   `json:"connected"`
	ReplicationLag int64  `json:"replication_lag_ms"`
	LagEntries     uint64 `json:"lag_entries"`
	LastAppliedAt  int64  `json:"last_applied_at,omitempty"`
	// Healthy is false on a follower that is not connected, is waiting for a
	// snapshot or is further behind than it may be, and on a leader whose
	// binlog stopped taking entries. LastError says which.
	Healthy   bool               `json:"healthy"`
	LastError string             `json:"last_error,omitempty"`
	Followers []FollowerInfoJSON `json:"followers"`
	Uptime    int64              `json:"uptime_seconds"`
}

// FollowerInfoJSON is the JSON representation of a follower a leader knows
type FollowerInfoJSON struct {
	FollowerID   string `json:"follower_id"`
	Address      string `json:"address"`
	ConfirmedLSN uint64 `json:"confirmed_lsn"`
	LagMs        int64  `json:"lag_ms"`
	LagEntries   uint64 `json:"lag_entries"`
	LagExact     bool   `json:"lag_exact"`
	LastSeenAt   int64  `json:"last_seen_at"`
	Connected    bool   `json:"connected"`
	Status       string `json:"status"` // healthy, warning, unhealthy, disconnected
}

var startTime = time.Now()

func (s *Server) handleReplicationStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, s.replicationStatus())
}

// replicationStatus gathers the node's replication state.
func (s *Server) replicationStatus() ReplicationStatusResponse {
	role := s.ReplicationRole
	if role == "" {
		role = "standalone"
	}
	resp := ReplicationStatusResponse{
		NodeID:    s.NodeID,
		Role:      role,
		Healthy:   true,
		Followers: []FollowerInfoJSON{},
		Uptime:    int64(time.Since(startTime).Seconds()),
	}

	if s.Binlog != nil {
		resp.setBinlog(s.Binlog)
		if s.replServer != nil {
			resp.Followers = s.replServer.followerViews()
		}
	}
	if s.replClient != nil {
		s.replClient.fillStatus(&resp)
	}
	return resp
}

// setBinlog fills in the log's position and size, and its failure if it has
// stopped.
func (r *ReplicationStatusResponse) setBinlog(bl *binlog.Binlog) {
	stats := bl.Stats()
	r.CurrentLSN = stats.CurrentLSN
	r.LogID = stats.LogID
	r.BinlogOldest = stats.OldestLSN
	r.BinlogSize = stats.FileSize
	r.BinlogMaxSize = stats.MaxSize
	r.BinlogSync = stats.Sync
	r.noteBinlogError(bl.Err())
}

// noteBinlogError marks the node unhealthy when its binlog has stopped
// taking entries: followers no longer receive what is written.
func (r *ReplicationStatusResponse) noteBinlogError(err error) {
	if err != nil {
		r.Healthy = false
		r.LastError = "binlog stopped: " + err.Error()
	}
}

// toProto is the status as the gRPC service returns it.
func (r ReplicationStatusResponse) toProto() *proto.ReplicationStatusResponse {
	out := &proto.ReplicationStatusResponse{
		NodeId:           r.NodeID,
		Role:             r.Role,
		CurrentLsn:       r.CurrentLSN,
		LeaderAddr:       r.LeaderAddr,
		LastAppliedAt:    r.LastAppliedAt,
		ReplicationLagMs: r.ReplicationLag,
		BinlogOldestLsn:  r.BinlogOldest,
		BinlogSizeBytes:  r.BinlogSize,
		Healthy:          r.Healthy,
		LagEntries:       r.LagEntries,
		LastError:        r.LastError,
		LogId:            r.LogID,
		Connected:        r.Connected,
		ProtocolVersion:  replicationProtocol,
	}
	for _, f := range r.Followers {
		out.Followers = append(out.Followers, &proto.FollowerInfo{
			FollowerId:   f.FollowerID,
			ConfirmedLsn: f.ConfirmedLSN,
			LastSeenAt:   f.LastSeenAt,
			Address:      f.Address,
			LagMs:        f.LagMs,
			Connected:    f.Connected,
			LagEntries:   f.LagEntries,
			Status:       f.Status,
			LagExact:     f.LagExact,
		})
	}
	return out
}
