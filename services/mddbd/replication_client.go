package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mddb/internal/binlog"
	proto "mddb/proto"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ReplicationClient manages the follower's connection to the leader.
//
// GO-050. The follower resumes from the position in its database, not from
// the start of the leader's log. An entry it cannot apply — or one that does
// not follow the last — stops it: the node reports itself unhealthy and
// replaces its data with a snapshot before it applies anything else, where it
// used to log the entry and carry on without it. What it applies it also
// writes to a log of its own under the leader's LSNs.
type ReplicationClient struct {
	server        *Server
	leaderAddr    string
	followerID    string
	applier       *ReplicationApplier
	retryInterval time.Duration
	ackInterval   time.Duration
	maxLag        time.Duration
	tlsConfig     *tls.Config
	logConfig     *binlog.BinlogConfig

	conn   *grpc.ClientConn
	client proto.MDDBReplicationClient
	// log is this follower's copy of the leader's log; nil when it could not
	// be opened, which costs the copy and nothing else.
	log atomic.Pointer[binlog.Binlog]

	lastAppliedAt atomic.Int64
	lagMs         atomic.Int64
	lagEntries    atomic.Uint64
	connected     atomic.Bool
	needSnapshot  atomic.Bool
	lastError     atomic.Pointer[string]
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
	// TLS is how the leader is dialled; nil is plaintext.
	TLS *tls.Config
	// Log configures the follower's copy of the leader's log; nil keeps none.
	Log *binlog.BinlogConfig
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
		tlsConfig:     cfg.TLS,
		logConfig:     cfg.Log,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the replication loop in a background goroutine
func (rc *ReplicationClient) Start() {
	if rc.running.Swap(true) {
		return // already running
	}
	rc.resume()
	rc.wg.Add(1)
	go rc.loop()
	slog.Info("Replication client started", "leaderAddr", rc.leaderAddr, "followerID", rc.followerID,
		"fromLSN", rc.applier.LastAppliedLSN())
}

// Stop gracefully stops the replication client
func (rc *ReplicationClient) Stop() {
	if !rc.running.Swap(false) {
		return
	}
	close(rc.stopCh)
	rc.wg.Wait()
	rc.disconnect()
	if log := rc.log.Swap(nil); log != nil {
		_ = log.Close()
	}
	slog.Info("Replication client stopped")
}

// LagMs returns the replication lag in milliseconds, as the leader measured
// it at the last acknowledgement.
func (rc *ReplicationClient) LagMs() int64 {
	return rc.lagMs.Load()
}

// LastAppliedAt returns the unix timestamp of the last applied entry
func (rc *ReplicationClient) LastAppliedAt() int64 {
	return rc.lastAppliedAt.Load()
}

// IsHealthy reports whether this follower is a current copy of its leader:
// the stream is open, nothing has failed, and the lag is within bounds.
func (rc *ReplicationClient) IsHealthy() bool {
	return rc.unhealthyReason() == ""
}

// unhealthyReason says why the follower is not healthy, or "".
func (rc *ReplicationClient) unhealthyReason() string {
	if e := rc.lastError.Load(); e != nil {
		return *e
	}
	switch {
	case rc.needSnapshot.Load():
		return "waiting for a snapshot from the leader"
	case !rc.connected.Load():
		return "not connected to the leader"
	case rc.lagMs.Load() > rc.maxLag.Milliseconds():
		return fmt.Sprintf("replication lag %d ms is over the limit of %d ms", rc.lagMs.Load(), rc.maxLag.Milliseconds())
	}
	return ""
}

// setError records why replication is failing; nil clears it.
func (rc *ReplicationClient) setError(err error) {
	if err == nil {
		rc.lastError.Store(nil)
		return
	}
	msg := err.Error()
	rc.lastError.Store(&msg)
}

// fillStatus adds the follower's side to a status response.
func (rc *ReplicationClient) fillStatus(resp *ReplicationStatusResponse) {
	pos := rc.applier.Position()
	resp.CurrentLSN = pos.LSN
	if pos.LogID != 0 {
		resp.LogID = binlog.FormatLogID(pos.LogID)
	}
	if log := rc.log.Load(); log != nil {
		stats := log.Stats()
		resp.BinlogOldest = stats.OldestLSN
		resp.BinlogSize = stats.FileSize
		resp.BinlogMaxSize = stats.MaxSize
		resp.BinlogSync = stats.Sync
	}
	resp.LeaderAddr = rc.leaderAddr
	resp.Connected = rc.connected.Load()
	resp.ReplicationLag = rc.lagMs.Load()
	resp.LagEntries = rc.lagEntries.Load()
	resp.LastAppliedAt = rc.lastAppliedAt.Load()
	resp.LastError = rc.unhealthyReason()
	resp.Healthy = resp.LastError == ""
}

// loop is the main replication loop with reconnection logic
func (rc *ReplicationClient) loop() {
	defer rc.wg.Done()

	for rc.running.Load() {
		err := rc.session()
		rc.connected.Store(false)
		rc.disconnect()
		if err != nil {
			rc.setError(err)
			slog.Warn("Replication stream error", "leaderAddr", rc.leaderAddr, "err", err)
		}
		// A follower that has just learned it needs a snapshot asks for it
		// at once; every other end of a session waits before the next.
		if err == nil || !rc.needSnapshot.Load() || errors.Is(err, errSnapshotFailed) {
			rc.waitRetry()
		}
	}
}

// errSnapshotFailed marks a session that ended because the snapshot it needed
// could not be taken.
var errSnapshotFailed = errors.New("snapshot failed")

// session is one connection to the leader: a snapshot if one is due, then
// the binlog stream until it ends.
func (rc *ReplicationClient) session() error {
	if err := rc.connect(); err != nil {
		return fmt.Errorf("failed to connect to leader: %w", err)
	}
	ctx, cancel := context.WithCancel(withReplicationSecret(context.Background()))
	defer cancel()
	// Stop ends a stream that is waiting for the next entry.
	go func() {
		select {
		case <-rc.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	if rc.needSnapshot.Load() {
		if err := rc.requestSnapshot(ctx); err != nil {
			return fmt.Errorf("%w: %w", errSnapshotFailed, err)
		}
	}
	return rc.replicate(ctx)
}

// connect establishes a gRPC connection to the leader
func (rc *ReplicationClient) connect() error {
	rc.disconnect()

	creds := insecure.NewCredentials()
	if rc.tlsConfig != nil {
		creds = credentials.NewTLS(rc.tlsConfig)
	}
	conn, err := grpc.NewClient(rc.leaderAddr,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64*1024*1024)), // 64MB for snapshots
	)
	if err != nil {
		return fmt.Errorf("failed to dial leader: %w", err)
	}

	rc.conn = conn
	rc.client = proto.NewMDDBReplicationClient(conn)
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

// replicate starts the binlog stream and applies entries until it ends.
func (rc *ReplicationClient) replicate(ctx context.Context) error {
	pos := rc.applier.Position()
	stream, err := rc.client.StreamBinlog(ctx, &proto.StreamBinlogRequest{
		FollowerId:      rc.followerID,
		FromLsn:         pos.LSN,
		ProtocolVersion: replicationProtocol,
		LogId:           pos.LogID,
	})
	if err != nil {
		return rc.streamEnded(err)
	}
	go rc.ackLoop(ctx)

	// The leader answers with its headers before the first entry, or with
	// its refusal. One from before 2.19.8 sends headers with its first
	// entry, so this waits for that entry and the follower shows as
	// connected from its first acknowledgement instead. A refusal has no
	// headers; the Recv below returns it.
	if header, err := stream.Header(); err == nil {
		rc.adoptLeaderLog(header)
	}

	for {
		entry, err := stream.Recv()
		if err != nil {
			return rc.streamEnded(err)
		}
		if err := rc.applyEntry(protoToEntry(entry)); err != nil {
			return err
		}
	}
}

// adoptLeaderLog takes the log ID from a leader's stream headers. Reaching
// here with headers means the leader accepted this follower's position, so
// a follower that knew no ID — a new one, or one upgraded from a release
// without them — is in that log.
func (rc *ReplicationClient) adoptLeaderLog(header metadata.MD) {
	if len(header.Get(replProtocolHeader)) == 0 {
		return // a leader before 2.19.8: no IDs
	}
	rc.connected.Store(true)
	rc.setError(nil)
	ids := header.Get(replLogIDHeader)
	if len(ids) == 0 {
		return
	}
	var id uint64
	if _, err := fmt.Sscanf(ids[0], "%x", &id); err != nil || id == rc.applier.Position().LogID {
		return
	}
	pos := replPosition{LSN: rc.applier.LastAppliedLSN(), LogID: id}
	rc.applier.SetPosition(pos)
	if log := rc.log.Load(); log != nil && log.LogID() != id {
		rc.rebaseLog(pos)
	}
}

// streamEnded turns the end of a stream into the session's result. A leader
// that says the follower's position cannot be continued (FailedPrecondition:
// trimmed away, another log, ahead of the leader) is answered with a
// snapshot. The refusal arrives here, with the first read, and not from the
// call that opened the stream — which is where this used to look for it, so
// a follower that had fallen behind the log retried the stream for ever.
func (rc *ReplicationClient) streamEnded(err error) error {
	if err == io.EOF {
		return nil // the leader closed the stream: it is shutting down
	}
	if status.Code(err) == codes.FailedPrecondition {
		rc.needSnapshot.Store(true)
		slog.Info("replication: the leader cannot continue from this follower's position; taking a snapshot", "reason", status.Convert(err).Message())
	}
	if rc.running.Load() {
		return fmt.Errorf("stream recv error: %w", err)
	}
	return nil // stopped
}

// applyEntry applies one entry from the stream and adds it to the follower's
// log. An entry that does not follow the last one applied, or cannot be
// applied, ends replication from the log: the follower's data is no longer
// known to be the leader's, and only a snapshot makes it so again.
func (rc *ReplicationClient) applyEntry(entry *binlog.BinlogEntry) error {
	err := error(nil)
	if want := rc.applier.LastAppliedLSN() + 1; entry.LSN != want {
		err = fmt.Errorf("the leader sent LSN %d where %d was due", entry.LSN, want)
	} else {
		err = rc.applier.Apply(entry)
	}
	if err != nil {
		rc.needSnapshot.Store(true)
		return fmt.Errorf("replication stopped, resynchronising from a snapshot: %w", err)
	}
	rc.appendLog(entry)
	rc.connected.Store(true)
	rc.lastAppliedAt.Store(time.Now().Unix())
	return nil
}

// ackLoop periodically sends LSN acknowledgments to the leader
func (rc *ReplicationClient) ackLoop(ctx context.Context) {
	ticker := time.NewTicker(rc.ackInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rc.acknowledge(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// acknowledge tells the leader what has been applied — which is what lets it
// trim its log — and takes the lag the leader measured in return.
func (rc *ReplicationClient) acknowledge(ctx context.Context) {
	client := rc.client
	if client == nil {
		return
	}
	lsn := rc.applier.LastAppliedLSN()
	resp, err := client.AcknowledgeLSN(ctx, &proto.AcknowledgeLSNRequest{
		FollowerId:   rc.followerID,
		ConfirmedLsn: lsn,
	})
	if err != nil {
		return
	}
	rc.connected.Store(true)
	rc.lagMs.Store(resp.LagMs)
	entries := uint64(0)
	if resp.LeaderLsn > lsn {
		entries = resp.LeaderLsn - lsn
	}
	rc.lagEntries.Store(entries)
}

// waitRetry waits for the retry interval or until stopped
func (rc *ReplicationClient) waitRetry() {
	select {
	case <-time.After(rc.retryInterval):
	case <-rc.stopCh:
	}
}
