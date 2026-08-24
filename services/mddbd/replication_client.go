package main

import (
	"context"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"io"
	"log/slog"
	proto "mddb/proto"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ReplicationClient manages the follower's connection to the leader.
type ReplicationClient struct {
	server        *Server
	leaderAddr    string
	followerID    string
	applier       *ReplicationApplier
	retryInterval time.Duration
	ackInterval   time.Duration
	maxLag        time.Duration

	conn   *grpc.ClientConn
	client proto.MDDBReplicationClient

	lastAppliedAt atomic.Int64
	lagMs         atomic.Int64
	running       atomic.Bool
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// ReplicationClientConfig holds configuration for the replication client
type ReplicationClientConfig struct {
	LeaderAddr    string
	FollowerID    string
	RetryInterval time.Duration
	AckInterval   time.Duration
	MaxLag        time.Duration
}

// NewReplicationClient creates a new replication client
func NewReplicationClient(s *Server, cfg ReplicationClientConfig) *ReplicationClient {
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = 5 * time.Second
	}
	if cfg.AckInterval <= 0 {
		cfg.AckInterval = 10 * time.Second
	}
	if cfg.MaxLag <= 0 {
		cfg.MaxLag = 30 * time.Second
	}

	return &ReplicationClient{
		server:        s,
		leaderAddr:    cfg.LeaderAddr,
		followerID:    cfg.FollowerID,
		applier:       NewReplicationApplier(s),
		retryInterval: cfg.RetryInterval,
		ackInterval:   cfg.AckInterval,
		maxLag:        cfg.MaxLag,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the replication loop in a background goroutine
func (rc *ReplicationClient) Start() {
	if rc.running.Swap(true) {
		return // already running
	}
	rc.wg.Add(1)
	go rc.loop()
	slog.Info("Replication client started", "leaderAddr", rc.leaderAddr, "followerID", rc.followerID)
}

// Stop gracefully stops the replication client
func (rc *ReplicationClient) Stop() {
	if !rc.running.Swap(false) {
		return
	}
	close(rc.stopCh)
	rc.wg.Wait()
	if rc.conn != nil {
		_ = rc.conn.Close()
	}
	slog.Info("Replication client stopped")
}

// LagMs returns the current replication lag in milliseconds
func (rc *ReplicationClient) LagMs() int64 {
	return rc.lagMs.Load()
}

// LastAppliedAt returns the unix timestamp of the last applied entry
func (rc *ReplicationClient) LastAppliedAt() int64 {
	return rc.lastAppliedAt.Load()
}

// IsHealthy returns true if the replication lag is within acceptable bounds
func (rc *ReplicationClient) IsHealthy() bool {
	lastApplied := rc.lastAppliedAt.Load()
	if lastApplied == 0 {
		return false // never applied anything
	}
	return time.Since(time.Unix(lastApplied, 0)) < rc.maxLag
}

// loop is the main replication loop with reconnection logic
func (rc *ReplicationClient) loop() {
	defer rc.wg.Done()

	for rc.running.Load() {
		if err := rc.connect(); err != nil {
			slog.Warn("Replication failed to connect to leader", "leaderAddr", rc.leaderAddr, "err", err)
			rc.waitRetry()
			continue
		}

		if err := rc.replicate(); err != nil {
			slog.Warn("Replication stream error", "err", err)
			rc.disconnect()
			rc.waitRetry()
			continue
		}
	}
}

// connect establishes a gRPC connection to the leader
func (rc *ReplicationClient) connect() error {
	if rc.conn != nil {
		_ = rc.conn.Close()
	}

	conn, err := grpc.NewClient(rc.leaderAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64*1024*1024)), // 64MB for snapshots
	)
	if err != nil {
		return fmt.Errorf("failed to dial leader: %w", err)
	}

	rc.conn = conn
	rc.client = proto.NewMDDBReplicationClient(conn)
	slog.Info("Replication connected to leader", "leaderAddr", rc.leaderAddr)
	return nil
}

// disconnect closes the gRPC connection
func (rc *ReplicationClient) disconnect() {
	if rc.conn != nil {
		_ = rc.conn.Close()
		rc.conn = nil
		rc.client = nil
	}
}

// withReplicationSecret attaches MDDB_REPLICATION_SECRET to the outgoing gRPC
// metadata so the leader's authorizeReplication accepts this follower (SEC-001).
// No-op when unset (mTLS / main-auth deployments don't need it).
func withReplicationSecret(ctx context.Context) context.Context {
	if secret := os.Getenv("MDDB_REPLICATION_SECRET"); secret != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-mddb-replication-secret", secret)
	}
	return ctx
}

// replicate starts the binlog stream and applies entries
func (rc *ReplicationClient) replicate() error {
	fromLSN := rc.applier.LastAppliedLSN()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = withReplicationSecret(ctx)

	// Start binlog stream
	stream, err := rc.client.StreamBinlog(ctx, &proto.StreamBinlogRequest{
		FollowerId: rc.followerID,
		FromLsn:    fromLSN,
	})
	if err != nil {
		st, ok := status.FromError(err)
		if ok && st.Code() == codes.FailedPrecondition {
			// LSN too old, need full snapshot
			slog.Info("replication LSN too old, requesting full snapshot")
			if err := rc.requestSnapshot(ctx); err != nil {
				return fmt.Errorf("snapshot failed: %w", err)
			}
			// Retry binlog stream after snapshot
			return nil
		}
		return err
	}

	// Start ack goroutine
	go rc.ackLoop(ctx)

	// Receive and apply entries
	for {
		select {
		case <-rc.stopCh:
			return nil
		default:
		}

		entry, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream recv error: %w", err)
		}

		// Apply entry
		binlogEntry := protoToEntry(entry)
		if err := rc.applier.Apply(binlogEntry); err != nil {
			slog.Warn("replication failed to apply entry", "lsn", entry.Lsn, "err", err)
			continue // skip and continue (eventual consistency)
		}

		rc.lastAppliedAt.Store(time.Now().Unix())

		// Update lag estimate
		entryTime := time.Unix(0, entry.Timestamp)
		rc.lagMs.Store(time.Since(entryTime).Milliseconds())
	}
}

// requestSnapshot downloads a full database snapshot from the leader
func (rc *ReplicationClient) requestSnapshot(ctx context.Context) error {
	ctx = withReplicationSecret(ctx)
	stream, err := rc.client.RequestSnapshot(ctx, &proto.SnapshotRequest{
		FollowerId: rc.followerID,
		CurrentLsn: rc.applier.LastAppliedLSN(),
	})
	if err != nil {
		return fmt.Errorf("failed to request snapshot: %w", err)
	}

	// Write to temp file
	tmpPath := rc.server.Path + ".snapshot.tmp"
	// #nosec G304 -- Extension is hardcoded and safe
	tmpFile, err := os.Create(filepath.Clean(tmpPath))
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	var snapshotLSN uint64
	var totalReceived uint64

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to receive snapshot chunk: %w", err)
		}

		if _, err := tmpFile.Write(chunk.Data); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("failed to write snapshot chunk: %w", err)
		}

		totalReceived += uint64(len(chunk.Data))
		snapshotLSN = chunk.SnapshotLsn

		if chunk.IsLast {
			break
		}
	}

	_ = tmpFile.Close()
	slog.Info("replication snapshot received", "totalReceived", totalReceived, "snapshotLSN", snapshotLSN)

	// Replace the database and rebuild in-memory state atomically with respect
	// to live readers (GO-004). The restore write lock drains in-flight
	// DBView/DBUpdate calls and blocks new ones, so no handler observes a
	// closed or half-swapped *bolt.DB.
	// swapDatabase rebuilds the derived in-memory state as part of reopening,
	// so there is no separate rebuild here.
	if err := rc.server.withRestoreLock(func() error {
		return rc.replaceDatabase(tmpPath)
	}); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to replace database: %w", err)
	}

	// Set the applier's LSN to the snapshot LSN (atomic, lock-free).
	rc.applier.SetLastAppliedLSN(snapshotLSN)
	rc.lastAppliedAt.Store(time.Now().Unix())

	slog.Info("replication snapshot applied", "snapshotLSN", snapshotLSN)
	return nil
}

// replaceDatabase swaps the live database for a received snapshot.
//
// SEC-018: this used to be a third hand-rolled close-rename-reopen. It did not
// check that the snapshot was a database, so a truncated stream was
// indistinguishable from a good one until the reopen — by which point the live
// database had already been renamed over. Any failure left the follower holding
// a closed handle with no way back, and it reopened with hand-written bolt
// options rather than the tuned ones, so a follower that had ever taken a
// snapshot ran with a different mmap and lock configuration than everything
// else in the process.
//
// The snapshot is renamed rather than copied: it is a temp file this client
// created and is about to delete, and copying a multi-gigabyte file to throw
// the original away is waste.
func (rc *ReplicationClient) replaceDatabase(snapshotPath string) error {
	return rc.server.swapDatabase(snapshotPath, func(dst string) error {
		return os.Rename(snapshotPath, dst)
	})
}

// ackLoop periodically sends LSN acknowledgments to the leader
func (rc *ReplicationClient) ackLoop(ctx context.Context) {
	ticker := time.NewTicker(rc.ackInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if rc.client == nil {
				continue
			}
			lsn := rc.applier.LastAppliedLSN()
			resp, err := rc.client.AcknowledgeLSN(ctx, &proto.AcknowledgeLSNRequest{
				FollowerId:   rc.followerID,
				ConfirmedLsn: lsn,
			})
			if err != nil {
				continue
			}
			// Update lag based on leader's current LSN
			if resp.LeaderLsn > lsn {
				rc.lagMs.Store(int64(resp.LeaderLsn-lsn) * 10) // #nosec G115 -- LSN diff within int64 range
			} else {
				rc.lagMs.Store(0)
			}
		case <-ctx.Done():
			return
		case <-rc.stopCh:
			return
		}
	}
}

// waitRetry waits for the retry interval or until stopped
func (rc *ReplicationClient) waitRetry() {
	select {
	case <-time.After(rc.retryInterval):
	case <-rc.stopCh:
	}
}
