package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Promotion and demotion: an operator moving the leadership on purpose.
//
// A node is promoted only when no node it can reach has a later log. If the
// leader is reachable it is asked to step down first; this node then waits
// until it holds everything the leader wrote, and stands. With automatic
// failover off and the other nodes down, an operator who knows the old
// leader is gone can force it: the node takes the next term without votes.
// A forced leader beside a live one is two leaders until they hear of each
// other — the older one then steps down and gives up what it took since.

// PromoteOptions is how a promotion goes.
type PromoteOptions struct {
	// Force takes the leadership without a majority's votes; only without
	// automatic failover, where an operator decides what is down.
	Force bool
	// CatchUp bounds the wait for this node to hold all the old leader
	// wrote (default: twice the election timeout).
	CatchUp time.Duration
}

// Promote makes this node the leader.
func (n *Node) Promote(ctx context.Context, opts PromoteOptions) error {
	if opts.Force && n.cfg.AutoFailover {
		return fmt.Errorf("force is for clusters without automatic failover: here a majority elects the leader")
	}
	if n.leading() {
		return nil
	}
	probes := n.probeAll(ctx)
	if err := n.handOver(ctx, probes, opts.CatchUp); err != nil {
		return err
	}
	if err := n.checkNotBehind(probes); err != nil {
		return err
	}
	if opts.Force {
		return n.forceLead(ctx, probes)
	}
	term, won := n.campaign(ctx)
	if !won {
		return fmt.Errorf("%w in term %d", ErrElectionLost, term)
	}
	return n.waitReady(ctx, term)
}

// leading reports whether this node leads and has taken over.
func (n *Node) leading() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state == Leader && n.readyTerm == n.term
}

// probeAll asks every other node about itself; unreachable nodes are left
// out.
func (n *Node) probeAll(ctx context.Context) map[string]ProbeReply {
	ctx, cancel := context.WithTimeout(ctx, n.cfg.ElectionTimeout)
	defer cancel()
	var mu sync.Mutex
	out := map[string]ProbeReply{}
	var wg sync.WaitGroup
	for _, p := range n.cfg.others() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := n.tr.Probe(ctx, p); err == nil {
				mu.Lock()
				out[p.ID] = r
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// handOver asks a reachable leader to step down and waits until this node
// holds everything it wrote.
func (n *Node) handOver(ctx context.Context, probes map[string]ProbeReply, wait time.Duration) error {
	id, ok := leaderAmong(probes)
	if !ok {
		return nil
	}
	peer, _ := n.cfg.peer(id)
	reply, err := n.tr.StepDown(ctx, peer, n.Term(), n.cfg.NodeID)
	if err != nil {
		return fmt.Errorf("the leader %s did not step down: %w", id, err)
	}
	slog.Info("cluster: the leader stepped down for this node's promotion", "leader", id, "lastLSN", reply.Last.LSN)
	if wait <= 0 {
		wait = 2 * n.cfg.ElectionTimeout
	}
	return n.catchUp(ctx, reply.Last, wait)
}

// leaderAmong is the reachable node that says it leads.
func leaderAmong(probes map[string]ProbeReply) (string, bool) {
	ids := make([]string, 0, len(probes))
	for id, p := range probes {
		if p.State == Leader {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", false
	}
	sort.Strings(ids)
	return ids[0], true
}

// catchUp waits until this node's log reaches target.
func (n *Node) catchUp(ctx context.Context, target Position, wait time.Duration) error {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	poll := time.NewTicker(n.tickEvery())
	defer poll.Stop()
	for !n.hooks.Position().AtLeast(target) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w (at %+v, the leader wrote through %+v)", ErrNotCaughtUp, n.hooks.Position(), target)
		case <-poll.C:
		}
	}
	return nil
}

// checkNotBehind refuses a promotion when a reachable node's log is later
// than this node's: making this node leader would cut that node's writes.
func (n *Node) checkNotBehind(probes map[string]ProbeReply) error {
	mine := n.hooks.Position()
	for id, p := range probes {
		if !mine.AtLeast(p.Last) {
			return fmt.Errorf("%w: %s is at LSN %d (term %d), this node at LSN %d (term %d)",
				ErrBehind, id, p.Last.LSN, p.Last.Term, mine.LSN, mine.Term)
		}
	}
	return nil
}

// forceLead takes the term after every term this node has seen, without
// votes.
func (n *Node) forceLead(ctx context.Context, probes map[string]ProbeReply) error {
	n.mu.Lock()
	next := n.term
	for _, p := range probes {
		next = max(next, p.Term)
	}
	n.term, n.votedFor = next+1, n.cfg.NodeID
	term := n.term
	slog.Warn("cluster: leadership forced without a majority", "term", term)
	n.leadLocked()
	n.mu.Unlock()
	return n.waitReady(ctx, term)
}

// waitReady waits until the Lead hook of term has completed.
func (n *Node) waitReady(ctx context.Context, term uint64) error {
	poll := time.NewTicker(n.tickEvery())
	defer poll.Stop()
	for {
		n.mu.Lock()
		ready, lost := n.readyTerm == term, n.term != term || n.state != Leader
		n.mu.Unlock()
		switch {
		case ready:
			return nil
		case lost:
			return fmt.Errorf("%w: lost the leadership of term %d while taking over", ErrElectionLost, term)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}
}

// StepDown makes the leader a follower that takes no writes, tells the
// others they may elect a successor, and returns its last position. The
// node does not stand again for twice the election timeout, which leaves
// the election to successor.
func (n *Node) StepDown(term uint64, successor string) (StepDownReply, error) {
	n.mu.Lock()
	if n.state != Leader {
		cur := n.term
		n.mu.Unlock()
		return StepDownReply{Term: cur}, ErrNotLeader
	}
	if term > n.term {
		n.adoptTermLocked(term)
	}
	n.holdUntil = n.now().Add(2 * n.cfg.ElectionTimeout)
	n.followLocked(n.term, "", "")
	cur := n.term
	n.mu.Unlock()
	slog.Info("cluster: stepped down", "term", cur, "successor", successor)
	// The position is read once no write can start: the gate checks the
	// role under the log's own lock.
	pos := n.hooks.Position()
	n.beat(true)
	return StepDownReply{Term: cur, Last: pos}, nil
}

// Demote steps this node down if it leads.
func (n *Node) Demote() error {
	_, err := n.StepDown(0, "")
	return err
}
