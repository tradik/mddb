package cluster

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Start runs the node: its timers, and the reconciler that applies role
// changes through the hooks. A node bootstraps — leads at once when it has
// never seen a term — when asked to: the first leader of a new cluster.
func (n *Node) Start(bootstrap bool) {
	n.resume(bootstrap)
	n.wg.Add(2)
	go n.loop()
	go n.reconcile()
	n.signal()
}

// Stop ends the node's timers and reconciler.
func (n *Node) Stop() {
	n.once.Do(func() { close(n.stop) })
	n.wg.Wait()
}

// resume makes a node that may not wait for an election a leader at start:
// in a cluster without automatic failover, the node that led the current
// term, or — in a new cluster — the bootstrap node. With failover on, a
// bootstrap node stands at once instead of after a timeout.
func (n *Node) resume(bootstrap bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch {
	case !n.cfg.AutoFailover && n.led != 0 && n.led == n.term:
		n.leadLocked()
	case !n.cfg.AutoFailover && bootstrap && n.term == 0:
		n.term, n.votedFor = 1, n.cfg.NodeID
		n.leadLocked()
	case bootstrap && n.term == 0:
		n.electionAt = n.now()
	}
}

// leadLocked makes this node leader of the current term, which it holds
// the vote of. The caller holds n.mu.
func (n *Node) leadLocked() {
	n.state, n.leader, n.leaderHTTP = Leader, n.cfg.NodeID, n.cfg.HTTP
	n.led = n.term
	if err := n.persistLocked(); err != nil {
		slog.Warn("cluster: could not record the term this node leads", "term", n.term, "err", err)
	}
	n.acks = map[string]time.Time{}
	n.signal()
}

// tickEvery is how often the loop looks at its timers.
func (n *Node) tickEvery() time.Duration {
	return max(min(n.cfg.Heartbeat/2, 100*time.Millisecond), time.Millisecond)
}

func (n *Node) loop() {
	defer n.wg.Done()
	t := time.NewTicker(n.tickEvery())
	defer t.Stop()
	nextBeat := time.Time{}
	for {
		select {
		case <-n.stop:
			return
		case <-t.C:
		}
		now := n.now()
		if n.isLeader() {
			if now.After(nextBeat) {
				nextBeat = now.Add(n.cfg.Heartbeat)
				n.beat(false)
			}
			n.checkLease()
			continue
		}
		if n.dueToStand(now) {
			go func() { _, _ = n.campaign(context.Background()) }()
		}
	}
}

func (n *Node) isLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state == Leader
}

// dueToStand reports whether a follower that has not heard from a leader
// for its timeout stands now.
func (n *Node) dueToStand(now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.cfg.AutoFailover && n.state == Follower && !now.Before(n.electionAt) && !now.Before(n.holdUntil)
}

// campaign stands in the next term and reports the term and whether it won.
func (n *Node) campaign(ctx context.Context) (uint64, bool) {
	pos := n.hooks.Position()
	n.mu.Lock()
	n.term++
	n.votedFor, n.state, n.leader, n.leaderHTTP = n.cfg.NodeID, Candidate, "", ""
	n.electionAt = n.now().Add(n.timeout())
	if err := n.persistLocked(); err != nil {
		n.state = Follower
		n.mu.Unlock()
		slog.Warn("cluster: could not record the vote for this node; not standing", "err", err)
		return 0, false
	}
	term, started := n.term, n.now()
	n.mu.Unlock()
	slog.Info("cluster: standing for election", "term", term, "lastLSN", pos.LSN, "lastTerm", pos.Term)

	won := n.collectVotes(ctx, VoteRequest{Term: term, Candidate: n.cfg.NodeID, Last: pos})
	n.mu.Lock()
	defer n.mu.Unlock()
	if !won || n.state != Candidate || n.term != term {
		if n.state == Candidate && n.term == term {
			n.state = Follower
		}
		return term, false
	}
	slog.Info("cluster: elected leader", "term", term)
	n.leaseUntil = started.Add(n.cfg.Lease())
	n.leadLocked()
	return term, true
}

// collectVotes asks every other node and reports whether a majority,
// this node included, granted the vote.
func (n *Node) collectVotes(ctx context.Context, req VoteRequest) bool {
	ctx, cancel := context.WithTimeout(ctx, n.cfg.ElectionTimeout)
	defer cancel()
	others := n.cfg.others()
	replies := make(chan VoteReply, len(others))
	for _, p := range others {
		go func() {
			r, err := n.tr.RequestVote(ctx, p, req)
			if err != nil {
				r = VoteReply{Reason: err.Error()}
			}
			replies <- r
		}()
	}
	granted := 1
	for range others {
		r := <-replies
		n.observeTerm(r.Term)
		if r.Granted {
			granted++
		}
		if granted >= n.cfg.Majority() {
			return true
		}
	}
	return granted >= n.cfg.Majority()
}

// observeTerm steps this node down when a reply carries a later term.
func (n *Node) observeTerm(term uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if term > n.term {
		n.followLocked(term, "", "")
	}
}

// beat sends a heartbeat to every other node and records the answers; with
// release, it tells them this node has handed over.
func (n *Node) beat(release bool) {
	n.mu.Lock()
	req := HeartbeatRequest{Term: n.term, Leader: n.cfg.NodeID, LeaderHTTP: n.cfg.HTTP, Release: release}
	n.mu.Unlock()
	sent := n.now()
	var wg sync.WaitGroup
	for _, p := range n.cfg.others() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*n.cfg.Heartbeat)
			defer cancel()
			r, err := n.tr.Heartbeat(ctx, p, req)
			n.onBeatReply(p.ID, req.Term, sent, r, err)
		}()
	}
	if release {
		wg.Wait()
	}
}

// onBeatReply records a node's answer to a heartbeat sent at sent in term.
func (n *Node) onBeatReply(id string, term uint64, sent time.Time, r HeartbeatReply, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		n.peers[id].Reachable = false
		return
	}
	n.notePeerLocked(id, func(p *PeerStatus) {
		p.HTTP, p.Term, p.State, p.LastLSN, p.LastTerm = r.HTTP, r.Term, r.State, r.Last.LSN, r.Last.Term
	})
	if r.Term > n.term {
		n.followLocked(r.Term, "", "")
		return
	}
	if r.OK && n.state == Leader && n.term == term {
		n.acks[id] = sent
		n.extendLeaseLocked()
	}
}

// extendLeaseLocked moves the lease to the latest send time a majority has
// answered, plus the lease length. The caller holds n.mu.
func (n *Node) extendLeaseLocked() {
	times := []time.Time{n.now()}
	for _, t := range n.acks {
		times = append(times, t)
	}
	slices.SortFunc(times, func(a, b time.Time) int { return b.Compare(a) })
	if len(times) < n.cfg.Majority() {
		return
	}
	if until := times[n.cfg.Majority()-1].Add(n.cfg.Lease()); until.After(n.leaseUntil) {
		n.leaseUntil = until
	}
}

// checkLease steps a leader down once its lease has run out: it has not
// heard from a majority, and the others may be electing a successor.
func (n *Node) checkLease() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.cfg.AutoFailover || n.state != Leader || !n.now().After(n.leaseUntil) {
		return
	}
	slog.Warn("cluster: the leader's lease ran out without a majority answering; stepping down", "term", n.term)
	n.followLocked(n.term, "", "")
}

// applied is the role the hooks last made the server.
type applied struct {
	state  State
	term   uint64
	leader string
}

// reconcile applies role changes through the hooks, one at a time and in
// order, always the latest one.
func (n *Node) reconcile() {
	defer n.wg.Done()
	var done applied
	for {
		select {
		case <-n.stop:
			return
		case <-n.kick:
		}
		done = n.apply(done)
	}
}

// apply calls the hook the current role needs, if it differs from done.
func (n *Node) apply(done applied) applied {
	n.mu.Lock()
	want := applied{state: n.state, term: n.term, leader: n.leader}
	n.mu.Unlock()
	if want.state == Candidate {
		want.state = Follower // a candidate takes no writes and follows no one
		want.leader = ""
	}
	if want == done {
		return done
	}
	if want.state == Leader {
		n.takeOver(want.term)
		return want
	}
	leader, _ := n.cfg.peer(want.leader)
	n.hooks.Follow(want.term, leader)
	return want
}

// takeOver runs the Lead hook for term; writes open once it has returned.
func (n *Node) takeOver(term uint64) {
	err := n.hooks.Lead(term)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.state != Leader || n.term != term {
		return // stepped down meanwhile; the next pass follows
	}
	if err != nil {
		slog.Error("cluster: could not take over as leader; stepping down", "term", term, "err", err)
		n.holdUntil = n.now().Add(n.cfg.ElectionTimeout)
		n.followLocked(term, "", "")
		return
	}
	n.readyTerm = term
}
