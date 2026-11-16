package main

import (
	"fmt"
	"strings"
	"time"

	"mddb/internal/cluster"
)

// Cluster settings read from the environment (GO-051). See docs/config.md
// and docs/REPLICATION.md. Without MDDB_CLUSTER_PEERS none of this applies
// and MDDB_REPLICATION_ROLE works as before.

// ackMode is when a write on the leader returns.
type ackMode string

const (
	ackAsync  ackMode = "async"  // once this node has committed it
	ackQuorum ackMode = "quorum" // once a majority of the cluster holds it
)

// clusterSettings is a node's cluster configuration.
type clusterSettings struct {
	node        cluster.Config
	ack         ackMode
	ackTimeout  time.Duration
	bootstrap   bool   // lead a cluster that has never had a term
	rejectedDir string // where cut writes are saved; "" is next to the binlog
}

// clusterSettingsFromEnv reads the cluster's settings; ok is false when the
// node is not in a cluster.
func clusterSettingsFromEnv(nodeID string) (cs clusterSettings, ok bool, err error) {
	raw := env("MDDB_CLUSTER_PEERS", "")
	if raw == "" {
		return cs, false, checkAckWithoutCluster()
	}
	peers, err := cluster.ParsePeers(raw)
	if err != nil {
		return cs, true, fmt.Errorf("MDDB_CLUSTER_PEERS: %w", err)
	}
	cs.node = cluster.Config{
		NodeID:       nodeID,
		Peers:        peers,
		AutoFailover: env("MDDB_CLUSTER_AUTO_FAILOVER", "true") == "true",
		HTTP:         strings.TrimRight(env("MDDB_CLUSTER_ADVERTISE_HTTP", ""), "/"),
	}
	durations := []struct {
		name string
		dst  *time.Duration
	}{
		{"MDDB_CLUSTER_HEARTBEAT", &cs.node.Heartbeat},
		{"MDDB_CLUSTER_ELECTION_TIMEOUT", &cs.node.ElectionTimeout},
		{"MDDB_REPLICATION_ACK_TIMEOUT", &cs.ackTimeout},
	}
	for _, d := range durations {
		if *d.dst, err = envDuration(d.name); err != nil {
			return cs, true, err
		}
	}
	if cs.ackTimeout == 0 {
		cs.ackTimeout = 5 * time.Second
	}
	if cs.ack, err = ackModeFromEnv(); err != nil {
		return cs, true, err
	}
	cs.bootstrap = env("MDDB_CLUSTER_BOOTSTRAP", "false") == "true" || env("MDDB_REPLICATION_ROLE", "") == "leader"
	cs.rejectedDir = env("MDDB_CLUSTER_REJECTED_DIR", "")
	return cs, true, nil
}

// checkAckWithoutCluster refuses quorum acknowledgements on a node that has
// no cluster to count a majority of.
func checkAckWithoutCluster() error {
	mode, err := ackModeFromEnv()
	if err == nil && mode == ackQuorum {
		err = fmt.Errorf("MDDB_REPLICATION_ACK=quorum needs MDDB_CLUSTER_PEERS: a majority is counted over the cluster's nodes")
	}
	return err
}

func ackModeFromEnv() (ackMode, error) {
	switch m := ackMode(strings.ToLower(env("MDDB_REPLICATION_ACK", string(ackAsync)))); m {
	case ackAsync, ackQuorum:
		return m, nil
	default:
		return "", fmt.Errorf("MDDB_REPLICATION_ACK=%q: use async or quorum", m)
	}
}

// envDuration is a positive duration from name; 0 when it is unset.
func envDuration(name string) (time.Duration, error) {
	v := env(name, "")
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s=%q: want a positive duration such as 500ms", name, v)
	}
	return d, nil
}
