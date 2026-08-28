package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
	"mddb/internal/binlog"
	proto "mddb/proto"
	"os"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// authorizeReplication gates the snapshot / binlog streams (SEC-001). Without
// it, RequestSnapshot streams the ENTIRE BoltDB — including auth_users (bcrypt
// hashes) and auth_apikeys — to any host that can reach the gRPC port, and
// StreamBinlog tails every write live, with no credentials. It accepts, in
// order: a matching replication secret, a verified mTLS client certificate, or
// an admin-authenticated context (the SEC-003 stream interceptor injects the
// claims). With none of those configured it refuses, naming the fix.
func (rs *ReplicationServer) authorizeReplication(ctx context.Context) error {
	// 1) Dedicated replication secret (best for cross-host links without full auth).
	if want := os.Getenv("MDDB_REPLICATION_SECRET"); want != "" {
		got := ""
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if v := md.Get("x-mddb-replication-secret"); len(v) > 0 {
				got = v[0]
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
			return nil
		}
		return status.Error(codes.PermissionDenied, "invalid replication secret")
	}

	// 2) A verified mTLS client certificate authenticates the peer.
	if p, ok := peer.FromContext(ctx); ok && p.AuthInfo != nil {
		if tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo); ok && len(tlsInfo.State.VerifiedChains) > 0 {
			return nil
		}
	}

	// 3) Main auth enabled: require admin (claims injected by the stream interceptor).
	if rs.server.AuthManager != nil && rs.server.AuthManager.enabled {
		if err := rs.server.AuthManager.CheckPermission(ctx, "*", PermAdmin); err != nil {
			return status.Error(codes.PermissionDenied, "replication requires admin credentials")
		}
		return nil
	}

	// 4) Nothing configured — refuse rather than expose the whole database.
	return status.Error(codes.PermissionDenied,
		"replication is unauthenticated: set MDDB_REPLICATION_SECRET, enable MDDB_AUTH_ENABLED, or use mTLS")
}

const snapshotChunkSize = 1024 * 1024 // 1MB

// ReplicationServer implements the MDDBReplication gRPC service (leader-side).
type ReplicationServer struct {
	proto.UnimplementedMDDBReplicationServer
	server    *Server
	followers map[string]*FollowerState
	mu        sync.RWMutex
}

// FollowerState tracks a connected follower
type FollowerState struct {
	ID           string
	ConfirmedLSN uint64
	LastSeenAt   int64
	Address      string
}

// NewReplicationServer creates a new replication server
func NewReplicationServer(s *Server) *ReplicationServer {
	return &ReplicationServer{
		server:    s,
		followers: make(map[string]*FollowerState),
	}
}

// RequestSnapshot streams a full BoltDB snapshot to a follower
func (rs *ReplicationServer) RequestSnapshot(req *proto.SnapshotRequest, stream proto.MDDBReplication_RequestSnapshotServer) error {
	if err := rs.authorizeReplication(stream.Context()); err != nil {
		return err
	}
	if rs.server.Binlog == nil {
		return status.Error(codes.FailedPrecondition, "binlog not enabled on this node")
	}

	followerID := req.FollowerId
	if followerID == "" {
		return status.Error(codes.InvalidArgument, "follower_id is required")
	}

	slog.Info("Replication follower requested snapshot", "followerID", followerID)

	// Record the LSN at snapshot time
	snapshotLSN := rs.server.Binlog.CurrentLSN()

	// Use BoltDB's read-only transaction to stream the database
	err := rs.server.DBView(func(tx *bolt.Tx) error {
		pr, pw := io.Pipe()

		// Write snapshot to pipe in background
		go func() {
			_, err := tx.WriteTo(pw)
			pw.CloseWithError(err)
		}()

		buf := make([]byte, snapshotChunkSize)
		var offset uint64
		totalSize := uint64(tx.Size()) // #nosec G115 -- db size always non-negative

		for {
			n, err := pr.Read(buf)
			if n > 0 {
				chunk := &proto.SnapshotChunk{
					Data:        buf[:n],
					Offset:      offset,
					TotalSize:   totalSize,
					IsLast:      err == io.EOF,
					SnapshotLsn: snapshotLSN,
				}
				if sendErr := stream.Send(chunk); sendErr != nil {
					_ = pr.Close()
					return fmt.Errorf("failed to send snapshot chunk: %w", sendErr)
				}
				offset += uint64(n)
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("failed to read snapshot: %w", err)
			}
		}
		return nil
	})

	if err != nil {
		slog.Warn("Replication snapshot failed", "followerID", followerID, "err", err)
		return status.Error(codes.Internal, err.Error())
	}

	slog.Info("replication snapshot completed", "followerID", followerID, "snapshotLSN", snapshotLSN)
	return nil
}

// StreamBinlog streams binlog entries from a given LSN. The stream stays open for continuous tailing.
func (rs *ReplicationServer) StreamBinlog(req *proto.StreamBinlogRequest, stream proto.MDDBReplication_StreamBinlogServer) error {
	if err := rs.authorizeReplication(stream.Context()); err != nil {
		return err
	}
	if rs.server.Binlog == nil {
		return status.Error(codes.FailedPrecondition, "binlog not enabled on this node")
	}

	followerID := req.FollowerId
	if followerID == "" {
		return status.Error(codes.InvalidArgument, "follower_id is required")
	}

	fromLSN := req.FromLsn

	// Track follower
	addr := ""
	if p, ok := peer.FromContext(stream.Context()); ok {
		addr = p.Addr.String()
	}
	rs.mu.Lock()
	rs.followers[followerID] = &FollowerState{
		ID:           followerID,
		ConfirmedLSN: fromLSN,
		LastSeenAt:   time.Now().Unix(),
		Address:      addr,
	}
	rs.mu.Unlock()

	defer func() {
		rs.mu.Lock()
		delete(rs.followers, followerID)
		rs.mu.Unlock()
		slog.Info("Replication follower disconnected from binlog stream", "followerID", followerID)
	}()

	slog.Info("replication follower streaming binlog", "followerID", followerID, "fromLSN", fromLSN)

	// 1. Send historical entries from binlog file
	entries, err := rs.server.Binlog.ReadFrom(fromLSN)
	if err != nil {
		if err == binlog.ErrBinlogLSNTooOld {
			return status.Error(codes.FailedPrecondition, "LSN too old, snapshot required")
		}
		return status.Error(codes.Internal, err.Error())
	}

	for _, entry := range entries {
		if err := stream.Send(entryToProto(entry)); err != nil {
			return err
		}
	}

	// 2. Subscribe to real-time entries and tail the binlog
	ch := rs.server.Binlog.Subscribe(followerID)
	defer rs.server.Binlog.Unsubscribe(followerID)

	for {
		select {
		case entry, ok := <-ch:
			if !ok {
				return nil // channel closed (binlog shutting down)
			}
			if err := stream.Send(entryToProto(entry)); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

// ReplicationStatus returns the current replication state
func (rs *ReplicationServer) ReplicationStatus(_ context.Context, _ *proto.ReplicationStatusRequest) (*proto.ReplicationStatusResponse, error) {
	resp := &proto.ReplicationStatusResponse{
		NodeId: rs.server.NodeID,
		Role:   rs.server.ReplicationRole,
	}

	if rs.server.Binlog != nil {
		stats := rs.server.Binlog.Stats()
		resp.CurrentLsn = stats.CurrentLSN
		resp.BinlogOldestLsn = stats.OldestLSN
		resp.BinlogSizeBytes = stats.FileSize
	}

	// Add follower info
	rs.mu.RLock()
	for _, fs := range rs.followers {
		lagMs := int64(0)
		if rs.server.Binlog != nil {
			currentLSN := rs.server.Binlog.CurrentLSN()
			if currentLSN > fs.ConfirmedLSN {
				lagMs = int64(currentLSN-fs.ConfirmedLSN) * 10 // #nosec G115 -- rough estimate, LSN diff within int64 range
			}
		}
		resp.Followers = append(resp.Followers, &proto.FollowerInfo{
			FollowerId:   fs.ID,
			ConfirmedLsn: fs.ConfirmedLSN,
			LastSeenAt:   fs.LastSeenAt,
			Address:      fs.Address,
			LagMs:        lagMs,
		})
	}
	rs.mu.RUnlock()

	return resp, nil
}

// AcknowledgeLSN processes LSN acknowledgment from a follower
func (rs *ReplicationServer) AcknowledgeLSN(_ context.Context, req *proto.AcknowledgeLSNRequest) (*proto.AcknowledgeLSNResponse, error) {
	rs.mu.Lock()
	if fs, ok := rs.followers[req.FollowerId]; ok {
		fs.ConfirmedLSN = req.ConfirmedLsn
		fs.LastSeenAt = time.Now().Unix()
	}
	rs.mu.Unlock()

	leaderLSN := uint64(0)
	if rs.server.Binlog != nil {
		leaderLSN = rs.server.Binlog.CurrentLSN()
	}

	return &proto.AcknowledgeLSNResponse{
		Ok:        true,
		LeaderLsn: leaderLSN,
	}, nil
}

// entryToProto converts a binlog.BinlogEntry to the protobuf representation
func entryToProto(e *binlog.BinlogEntry) *proto.BinlogEntryProto {
	return &proto.BinlogEntryProto{
		Lsn:        e.LSN,
		Type:       uint32(e.Type),
		Timestamp:  e.Timestamp,
		BucketName: e.BucketName,
		Key:        e.Key,
		Value:      e.Value,
		Checksum:   e.Checksum,
	}
}

// protoToEntry converts a protobuf BinlogEntryProto to internal binlog.BinlogEntry
func protoToEntry(p *proto.BinlogEntryProto) *binlog.BinlogEntry {
	return &binlog.BinlogEntry{
		LSN:        p.Lsn,
		Type:       binlog.BinlogEntryType(p.Type), // #nosec G115 -- entry type always within byte range
		Timestamp:  p.Timestamp,
		BucketName: p.BucketName,
		Key:        p.Key,
		Value:      p.Value,
		Checksum:   p.Checksum,
	}
}
