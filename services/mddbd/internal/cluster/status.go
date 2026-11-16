package cluster

import (
	"sort"
	"time"
)

// Status is the node's view of its cluster, for the status endpoints and
// the panel.
type Status struct {
	NodeID       string       `json:"node_id"`
	State        State        `json:"state"`
	Term         uint64       `json:"term"`
	VotedFor     string       `json:"voted_for,omitempty"`
	LeaderID     string       `json:"leader_id,omitempty"`
	LeaderHTTP   string       `json:"leader_addr,omitempty"`
	LeaseValid   bool         `json:"lease_valid"`
	Writable     bool         `json:"writable"`
	AutoFailover bool         `json:"auto_failover"`
	Majority     int          `json:"majority"`
	Nodes        []PeerStatus `json:"nodes"`
}

// Status reports what this node knows. Its own entry carries its log
// position; the others carry what they last said, which on a follower is
// little beyond the leader.
func (n *Node) Status() Status {
	pos := n.hooks.Position()
	writable := n.CanWrite() == nil
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.now()
	st := Status{
		NodeID: n.cfg.NodeID, State: n.state, Term: n.term, VotedFor: n.votedFor,
		LeaderID: n.leader, LeaderHTTP: n.leaderHTTP,
		LeaseValid:   n.state == Leader && (!n.cfg.AutoFailover || now.Before(n.leaseUntil)),
		Writable:     writable,
		AutoFailover: n.cfg.AutoFailover,
		Majority:     n.cfg.Majority(),
	}
	for _, p := range n.peers {
		view := *p
		if view.Self {
			view.State, view.Term, view.LastLSN, view.LastTerm = n.state, n.term, pos.LSN, pos.Term
			view.LastSeen, view.Reachable = now, true
		} else if view.Reachable && now.Sub(view.LastSeen) > 2*n.cfg.ElectionTimeout {
			view.Reachable = false // not heard from for a while
		}
		st.Nodes = append(st.Nodes, view)
	}
	sort.Slice(st.Nodes, func(i, j int) bool { return st.Nodes[i].ID < st.Nodes[j].ID })
	return st
}

// LeaseRemaining is how long the leader's lease still runs; 0 when this node
// does not lead or the lease has run out.
func (n *Node) LeaseRemaining() time.Duration {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.state != Leader {
		return 0
	}
	return max(n.leaseUntil.Sub(n.now()), 0)
}
