// Package cluster elects the leader of a group of mddbd nodes (GO-051).
//
// It is a small, self-contained election: every node keeps a term and the
// vote it gave in it on disk; a node that has not heard from a leader for an
// election timeout becomes a candidate in the next term and asks the others
// for their votes; a majority makes it leader for that term. A node gives
// one vote per term, and only to a candidate whose log is at least as far
// as its own — compared by the term of the last entry, then its LSN — so a
// leader holds every write a majority had. Two leaders in one term would
// need two majorities, which do not exist.
//
// The leader sends heartbeats. A heartbeat answered by a majority extends
// its lease, and only a leader inside its lease takes writes: a leader cut
// off from the majority stops taking them before the others may elect a
// successor, because a node that heard from a leader within the election
// timeout gives no vote. The lease is shorter than the timeout by a margin
// that covers clocks running at slightly different rates; it does not
// depend on the clocks agreeing.
//
// With fewer than three nodes there is no majority to fail over to, and
// automatic failover is off: a leader is made by promotion, which can be
// forced when the other node is known to be down.
//
// The package knows nothing of databases or the network. The server gives it
// a Transport to reach its peers and Hooks that make a node a leader or a
// follower, and asks it whether this node may take a write now.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// State is what a node is in its current term.
type State string

// The states of a node.
const (
	Follower  State = "follower"
	Candidate State = "candidate"
	Leader    State = "leader"
)

// Peer is a node of the cluster, this one included.
type Peer struct {
	ID   string // MDDB_NODE_ID of the node
	Addr string // its gRPC address, where replication and elections go
}

// Position is the last entry of a log: what an election compares.
type Position struct {
	LSN  uint64
	Term uint64
}

// AtLeast reports whether a log ending at p holds at least what one ending
// at o does: a later last term, or the same term and an LSN no lower.
func (p Position) AtLeast(o Position) bool {
	return p.Term > o.Term || (p.Term == o.Term && p.LSN >= o.LSN)
}

// Config is a node's view of its cluster.
type Config struct {
	NodeID string
	Peers  []Peer // every node, this one included
	// Heartbeat is how often the leader reaches every node.
	Heartbeat time.Duration
	// ElectionTimeout is how long a node waits without hearing from a
	// leader before it stands; each wait is drawn between it and twice it.
	ElectionTimeout time.Duration
	// AutoFailover lets nodes stand on their own. Off, a leader is only
	// made by promotion; it is forced off below three nodes.
	AutoFailover bool
	// HTTP is this node's address for clients, given to them when they
	// send a write to a node that is not the leader.
	HTTP string
}

// Defaults for the timings.
const (
	DefaultHeartbeat       = 500 * time.Millisecond
	DefaultElectionTimeout = 3 * time.Second
	minAutoFailoverNodes   = 3
)

// Majority is how many nodes, this one included, make a quorum.
func (c Config) Majority() int { return len(c.Peers)/2 + 1 }

// Lease is how long a heartbeat a majority answered lets the leader take
// writes: three quarters of the election timeout, so it has run out before
// any node that answered may vote for another.
func (c Config) Lease() time.Duration { return c.ElectionTimeout * 3 / 4 }

// others are the peers other than this node.
func (c Config) others() []Peer {
	out := make([]Peer, 0, len(c.Peers))
	for _, p := range c.Peers {
		if p.ID != c.NodeID {
			out = append(out, p)
		}
	}
	return out
}

// peer is the peer called id.
func (c Config) peer(id string) (Peer, bool) {
	for _, p := range c.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return Peer{}, false
}

// Validate fills in the default timings and checks the configuration. It
// turns automatic failover off below three nodes and says so in its second
// result.
func (c *Config) Validate() (string, error) {
	if c.Heartbeat <= 0 {
		c.Heartbeat = DefaultHeartbeat
	}
	if c.ElectionTimeout <= 0 {
		c.ElectionTimeout = DefaultElectionTimeout
	}
	if c.ElectionTimeout < 2*c.Heartbeat {
		return "", fmt.Errorf("the election timeout (%s) must be at least twice the heartbeat (%s)", c.ElectionTimeout, c.Heartbeat)
	}
	if _, ok := c.peer(c.NodeID); !ok {
		return "", fmt.Errorf("this node (%q) is not among the cluster peers", c.NodeID)
	}
	if c.AutoFailover && len(c.Peers) < minAutoFailoverNodes {
		c.AutoFailover = false
		return fmt.Sprintf("automatic failover needs at least %d nodes (a majority must survive a failure); "+
			"with %d, a leader is made by promotion", minAutoFailoverNodes, len(c.Peers)), nil
	}
	return "", nil
}

// ParsePeers reads MDDB_CLUSTER_PEERS: "id=host:port" items separated by
// commas, every node of the cluster once, this one included.
func ParsePeers(s string) ([]Peer, error) {
	var peers []Peer
	seen := map[string]bool{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		id, addr, ok := strings.Cut(item, "=")
		id, addr = strings.TrimSpace(id), strings.TrimSpace(addr)
		if !ok || id == "" || addr == "" {
			return nil, fmt.Errorf("cluster peer %q: want id=host:port", item)
		}
		if seen[id] {
			return nil, fmt.Errorf("cluster peer %q is listed twice", id)
		}
		seen[id] = true
		peers = append(peers, Peer{ID: id, Addr: addr})
	}
	if len(peers) == 0 {
		return nil, errors.New("no cluster peers")
	}
	return peers, nil
}

// VoteRequest asks for a node's vote in Term.
type VoteRequest struct {
	Term      uint64
	Candidate string
	Last      Position
}

// VoteReply is a node's answer to a VoteRequest.
type VoteReply struct {
	Term    uint64
	Granted bool
	Reason  string
}

// HeartbeatRequest is the leader of Term reaching a node.
type HeartbeatRequest struct {
	Term       uint64
	Leader     string
	LeaderHTTP string
	// Release: the leader has stepped down to hand over; the node may vote
	// for a successor at once.
	Release bool
}

// HeartbeatReply is a node's answer to a heartbeat.
type HeartbeatReply struct {
	Term  uint64
	OK    bool
	Last  Position
	HTTP  string
	State State
}

// ProbeReply is what a node says about itself.
type ProbeReply struct {
	Term   uint64
	State  State
	Leader string
	Last   Position
	HTTP   string
}

// StepDownReply is a former leader's last position.
type StepDownReply struct {
	Term uint64
	Last Position
}

// Transport reaches the other nodes.
type Transport interface {
	RequestVote(ctx context.Context, to Peer, req VoteRequest) (VoteReply, error)
	Heartbeat(ctx context.Context, to Peer, req HeartbeatRequest) (HeartbeatReply, error)
	Probe(ctx context.Context, to Peer) (ProbeReply, error)
	StepDown(ctx context.Context, to Peer, term uint64, successor string) (StepDownReply, error)
}

// Hooks make the server what the election decided. They are called one at
// a time, in order, never under the node's lock.
type Hooks struct {
	// Position is this node's log position.
	Position func() Position
	// Lead makes this node the leader of term. Writes are taken only once
	// it has returned without an error; an error makes the node step down.
	Lead func(term uint64) error
	// Follow makes this node a follower in term of leader ("" while no
	// leader is known): it stops taking writes and replicates from it.
	Follow func(term uint64, leader Peer)
}

// Errors of the node.
var (
	ErrNotLeader     = errors.New("this node is not the leader")
	ErrNoLeader      = errors.New("no leader: an election is in progress")
	ErrElectionLost  = errors.New("the election did not reach a majority")
	ErrBehind        = errors.New("another node has a later log")
	ErrNotCaughtUp   = errors.New("this node did not catch up with the leader in time")
	ErrLeaderPresent = errors.New("a leader is active")
)
