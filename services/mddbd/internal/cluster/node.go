package cluster

import (
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

// Node is this server's part in the election.
type Node struct {
	cfg   Config
	tr    Transport
	hooks Hooks
	store *Store
	now   func() time.Time

	mu         sync.Mutex
	term       uint64
	votedFor   string
	led        uint64 // the last term this node led, kept so a manual cluster's leader resumes
	state      State
	leader     string
	leaderHTTP string
	heardAt    time.Time // last heartbeat from the leader; zero after it handed over
	electionAt time.Time // when this node stands if it has not heard from a leader
	holdUntil  time.Time // a node that handed over does not stand before
	leaseUntil time.Time
	readyTerm  uint64               // the term whose Lead hook has completed
	acks       map[string]time.Time // leader: when the heartbeat each peer last answered was sent
	peers      map[string]*PeerStatus

	kick chan struct{} // the role changed; the reconciler applies it
	stop chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

// PeerStatus is what this node knows of a peer.
type PeerStatus struct {
	ID        string    `json:"id"`
	Addr      string    `json:"grpc_addr"`
	HTTP      string    `json:"http_addr,omitempty"`
	State     State     `json:"state,omitempty"`
	Term      uint64    `json:"term"`
	LastLSN   uint64    `json:"last_lsn"`
	LastTerm  uint64    `json:"last_term"`
	LastSeen  time.Time `json:"last_seen"`
	Reachable bool      `json:"reachable"`
	Self      bool      `json:"self,omitempty"`
}

// New is a node in cfg, as a follower in the term store remembers. cfg must
// have been validated.
func New(cfg Config, tr Transport, hooks Hooks, store *Store) (*Node, error) {
	v, err := store.load()
	if err != nil {
		return nil, err
	}
	n := &Node{
		cfg: cfg, tr: tr, hooks: hooks, store: store, now: time.Now,
		term: v.Term, votedFor: v.VotedFor, led: v.Led,
		state: Follower,
		acks:  map[string]time.Time{},
		peers: map[string]*PeerStatus{},
		kick:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
	}
	for _, p := range cfg.Peers {
		n.peers[p.ID] = &PeerStatus{ID: p.ID, Addr: p.Addr, Self: p.ID == cfg.NodeID}
	}
	n.peers[cfg.NodeID].HTTP = cfg.HTTP
	n.electionAt = n.now().Add(n.timeout())
	return n, nil
}

// timeout is an election timeout drawn between the configured one and twice
// it, so that nodes rarely stand at the same moment.
func (n *Node) timeout() time.Duration {
	// #nosec G404 -- jitter for election timing, not a secret
	return n.cfg.ElectionTimeout + time.Duration(rand.Int64N(int64(n.cfg.ElectionTimeout)))
}

// persistLocked saves the term and vote. The caller holds n.mu.
func (n *Node) persistLocked() error {
	return n.store.save(vote{Term: n.term, VotedFor: n.votedFor, Led: n.led})
}

// adoptTermLocked moves to a later term, without a vote in it. The caller
// holds n.mu.
func (n *Node) adoptTermLocked(term uint64) {
	if term <= n.term {
		return
	}
	n.term, n.votedFor = term, ""
	if err := n.persistLocked(); err != nil {
		slog.Warn("cluster: could not save the new term", "term", term, "err", err)
	}
}

// followLocked makes this node a follower of leader in term ("" while no
// leader is known). The caller holds n.mu.
func (n *Node) followLocked(term uint64, leader, leaderHTTP string) {
	n.adoptTermLocked(term)
	changed := n.state != Follower || n.leader != leader
	if n.state == Leader {
		slog.Info("cluster: stepping down", "term", n.term, "leader", leader)
	}
	n.state, n.leader, n.leaderHTTP = Follower, leader, leaderHTTP
	n.readyTerm = 0
	if changed {
		n.signal()
	}
}

// signal wakes the reconciler.
func (n *Node) signal() {
	select {
	case n.kick <- struct{}{}:
	default:
	}
}

// HandleVote answers a candidate.
func (n *Node) HandleVote(req VoteRequest) VoteReply {
	pos := n.hooks.Position()
	n.mu.Lock()
	defer n.mu.Unlock()
	if req.Term < n.term {
		return VoteReply{Term: n.term, Reason: "the candidate's term is behind"}
	}
	if why := n.leaderActiveLocked(req.Candidate); why != "" {
		return VoteReply{Term: n.term, Reason: why}
	}
	if req.Term > n.term {
		n.followLocked(req.Term, "", "")
	}
	if n.votedFor != "" && n.votedFor != req.Candidate {
		return VoteReply{Term: n.term, Reason: "voted for " + n.votedFor + " in this term"}
	}
	if !req.Last.AtLeast(pos) {
		return VoteReply{Term: n.term, Reason: "the candidate's log is behind this node's"}
	}
	n.votedFor = req.Candidate
	if err := n.persistLocked(); err != nil {
		n.votedFor = ""
		return VoteReply{Term: n.term, Reason: "could not record the vote"}
	}
	n.electionAt = n.now().Add(n.timeout())
	return VoteReply{Term: n.term, Granted: true}
}

// leaderActiveLocked says why a vote is refused while a leader is known to
// be alive: this node is it, inside its lease, or heard from it within the
// election timeout. That is what keeps a leader's lease safe, and a node
// cut off from the leader from unseating it when it comes back.
func (n *Node) leaderActiveLocked(candidate string) string {
	if !n.cfg.AutoFailover {
		return ""
	}
	now := n.now()
	if n.state == Leader && now.Before(n.leaseUntil) {
		return "this node leads with a valid lease"
	}
	if n.leader != "" && n.leader != candidate && !n.heardAt.IsZero() && now.Sub(n.heardAt) < n.cfg.ElectionTimeout {
		return "heard from leader " + n.leader + " " + now.Sub(n.heardAt).Round(time.Millisecond).String() + " ago"
	}
	return ""
}

// HandleHeartbeat answers the leader.
func (n *Node) HandleHeartbeat(req HeartbeatRequest) HeartbeatReply {
	pos := n.hooks.Position()
	n.mu.Lock()
	defer n.mu.Unlock()
	reply := HeartbeatReply{Last: pos, HTTP: n.cfg.HTTP}
	if req.Term < n.term {
		reply.Term, reply.State = n.term, n.state
		return reply
	}
	if req.Release {
		// The leader handed over: keep replicating from it until the
		// successor leads, and vote for that successor at once.
		n.heardAt = time.Time{}
		n.electionAt = n.now().Add(n.timeout())
		n.adoptTermLocked(req.Term)
	} else {
		n.followLocked(req.Term, req.Leader, req.LeaderHTTP)
		n.heardAt = n.now()
		n.electionAt = n.heardAt.Add(n.timeout())
	}
	n.notePeerLocked(req.Leader, func(p *PeerStatus) {
		p.HTTP, p.Term, p.State = req.LeaderHTTP, req.Term, Leader
		if req.Release {
			p.State = Follower
		}
	})
	reply.Term, reply.OK, reply.State = n.term, true, n.state
	return reply
}

// notePeerLocked records that peer id was heard from. The caller holds n.mu.
func (n *Node) notePeerLocked(id string, update func(*PeerStatus)) {
	p, ok := n.peers[id]
	if !ok || p.Self {
		return
	}
	p.LastSeen, p.Reachable = n.now(), true
	update(p)
}

// Probe is what this node says about itself to a peer.
func (n *Node) Probe() ProbeReply {
	pos := n.hooks.Position()
	n.mu.Lock()
	defer n.mu.Unlock()
	return ProbeReply{Term: n.term, State: n.state, Leader: n.leader, Last: pos, HTTP: n.cfg.HTTP}
}

// CanWrite says whether this node may take a write now: it leads, has
// finished taking over, and — with automatic failover — its lease holds.
func (n *Node) CanWrite() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case n.state != Leader && n.leader != "":
		return ErrNotLeader
	case n.state != Leader, n.readyTerm != n.term:
		return ErrNoLeader
	case n.cfg.AutoFailover && n.now().After(n.leaseUntil):
		return ErrNoLeader
	}
	return nil
}

// Leader is the leader this node knows and its address for clients, with
// the current term; "" while none is known.
func (n *Node) Leader() (id, http string, term uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leader, n.leaderHTTP, n.term
}

// Term is this node's current term.
func (n *Node) Term() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.term
}

// State is this node's state in its current term.
func (n *Node) State() State {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state
}

// Observe tells the node a peer is in term; a later term makes it a
// follower that knows no leader yet.
func (n *Node) Observe(term uint64) { n.observeTerm(term) }

// Config is the node's configuration.
func (n *Node) Config() Config { return n.cfg }
