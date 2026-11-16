package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"

	"mddb/internal/binlog"
	"mddb/internal/cluster"
)

// A node in a cluster (GO-051, MDDB_CLUSTER_PEERS).
//
// Every node keeps one binlog: as leader it appends its own writes to it,
// as follower the entries it applies from the leader, under the leader's
// LSNs and terms. So a follower that becomes leader continues the same log,
// and the others continue following from where they are. The election
// (internal/cluster) decides the role; this file makes the server play it:
//
//   - leader: the replication client stops, the log takes the new term, and
//     writes open. A node whose log was not closed cleanly first moves the
//     log far ahead (binlog.Jump), so every follower takes a snapshot of
//     what its database holds.
//   - follower: writes close — the log's gate refuses them inside their
//     transaction, and the write surfaces answer with the leader's address —
//     and a replication client follows the leader.
type clusterRuntime struct {
	s        *Server
	node     *cluster.Node
	settings clusterSettings
	tr       *peerTransport
	tls      *tls.Config

	clientMu sync.Mutex // serialises client changes
	client   atomic.Pointer[ReplicationClient]
	rejected atomic.Pointer[string] // the last rejected-writes file
	acks     ackBoard
}

// initClusterFromEnv joins the cluster MDDB_CLUSTER_PEERS describes, and
// reports false when it describes none. A configuration that cannot work
// stops the start.
func (s *Server) initClusterFromEnv() bool {
	cs, ok, err := clusterSettingsFromEnv(s.NodeID)
	if err == nil && ok && !s.Config.GRPC.Enabled {
		err = errors.New("a cluster node needs gRPC: elections and replication run on it")
	}
	if err == nil && ok {
		var logCfg binlog.BinlogConfig
		if logCfg, err = binlogConfigFromEnv(); err == nil {
			err = s.initCluster(cs, logCfg)
		}
	}
	fatalOnErr(err, "Invalid cluster configuration")
	if ok {
		s.setBinlogEverywhere()
	}
	return ok
}

// initCluster opens the node's log and builds its election. It does not
// start it: startCluster does, once the replication service listens.
func (s *Server) initCluster(cs clusterSettings, logCfg binlog.BinlogConfig) error {
	if s.NodeID == "" {
		return fmt.Errorf("MDDB_CLUSTER_PEERS needs MDDB_NODE_ID, this node's id among the peers")
	}
	cs.node.NodeID = s.NodeID
	warning, err := cs.node.Validate()
	if err != nil {
		return err
	}
	if warning != "" {
		slog.Warn("cluster: " + warning)
	}
	tlsCfg, err := replicationTLSFromEnv()
	if err != nil {
		return err
	}
	logCfg.Replica = true // LSNs and IDs are the cluster's, kept across roles
	bl, err := binlog.NewBinlog(s.Path, logCfg)
	if err != nil {
		return err
	}
	c := &clusterRuntime{s: s, settings: cs, tr: newPeerTransport(tlsCfg), tls: tlsCfg}
	c.acks.init()
	hooks := cluster.Hooks{Position: c.position, Lead: c.lead, Follow: c.follow}
	store := cluster.NewStore(filepath.Join(filepath.Dir(bl.Stats().Path), "mddb.cluster.json"))
	if c.node, err = cluster.New(cs.node, c.tr, hooks, store); err != nil {
		_ = bl.Close()
		return err
	}
	bl.SetWriteGate(c.writeGate)
	if cs.ack == ackQuorum {
		bl.SetAckWaiter(c.waitQuorum)
	}
	s.Binlog, s.cluster = bl, c
	slog.Info("cluster node configured", "nodeID", s.NodeID, "peers", len(cs.node.Peers),
		"autoFailover", cs.node.AutoFailover, "ack", cs.ack, "dirtyLog", bl.Dirty())
	return nil
}

// start runs the election.
func (c *clusterRuntime) start() { c.node.Start(c.settings.bootstrap) }

// stop ends the election and the replication client. It is a shutdown
// step: the log closes after it.
func (c *clusterRuntime) stop() {
	c.node.Stop()
	c.stopClient()
	c.tr.close()
}

// position is the log's last entry.
func (c *clusterRuntime) position() cluster.Position {
	lsn, term := c.s.Binlog.LastPosition()
	return cluster.Position{LSN: lsn, Term: term}
}

// lead makes the server the leader of term.
func (c *clusterRuntime) lead(term uint64) error {
	c.stopClient()
	bl := c.s.Binlog
	if bl.Dirty() {
		slog.Warn("cluster: this node's log may lack writes its database holds; "+
			"moving it ahead so every follower takes a snapshot", "term", term)
		if err := bl.Jump(); err != nil {
			return err
		}
	}
	if err := bl.SetTerm(term); err != nil {
		return err
	}
	slog.Info("cluster: this node leads", "term", term, "lsn", bl.CurrentLSN())
	return nil
}

// follow makes the server a follower of leader in term.
func (c *clusterRuntime) follow(term uint64, leader cluster.Peer) {
	c.stopClient()
	if leader.ID == "" || leader.ID == c.s.NodeID {
		slog.Info("cluster: waiting for a leader", "term", term)
		return
	}
	slog.Info("cluster: following", "term", term, "leader", leader.ID, "addr", leader.Addr)
	c.startClient(leader.Addr)
}

// startClient follows the leader at addr.
func (c *clusterRuntime) startClient(addr string) {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()
	rc := NewReplicationClient(c.s, ReplicationClientConfig{
		LeaderAddr: addr,
		FollowerID: c.s.NodeID,
		TLS:        c.tls,
		Cluster:    c,
		// Reconnect and acknowledge on the election's clock, not the
		// seconds a plain follower waits.
		RetryInterval: 2 * c.settings.node.Heartbeat,
		AckInterval:   c.settings.node.Heartbeat,
	})
	c.client.Store(rc)
	rc.Start()
}

// stopClient stops following, if this node was.
func (c *clusterRuntime) stopClient() {
	c.clientMu.Lock()
	defer c.clientMu.Unlock()
	if rc := c.client.Swap(nil); rc != nil {
		rc.Stop()
	}
}

// writeGate is the log's gate: only a leader that may write appends.
func (c *clusterRuntime) writeGate() error {
	if err := c.node.CanWrite(); err != nil {
		return c.notLeader()
	}
	return nil
}
