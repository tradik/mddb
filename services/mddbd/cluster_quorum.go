package main

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// MDDB_REPLICATION_ACK=quorum (GO-051): a write on the leader returns once
// a majority of the cluster, the leader included, holds it. A follower
// acknowledges each batch it applies; the leader's write waits on the board
// below until enough have. A write that is not confirmed in
// MDDB_REPLICATION_ACK_TIMEOUT fails with errQuorumTimeout — it is committed
// on the leader and may survive or be cut by a failover, which is what an
// unconfirmed write means.
//
// With async (the default) a write returns once the leader has committed
// it: a leader that dies before its followers received a write takes that
// write with it into the rejected-writes file when it returns.

var errQuorumTimeout = errors.New("the write is not yet held by a majority of the cluster")

// ackBoard is what each follower last acknowledged.
type ackBoard struct {
	mu    sync.Mutex
	acked map[string]uint64
	moved chan struct{} // closed and replaced on every acknowledgement
}

func (b *ackBoard) init() {
	b.acked = map[string]uint64{}
	b.moved = make(chan struct{})
}

// record notes that follower id holds everything through lsn.
func (b *ackBoard) record(id string, lsn uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lsn <= b.acked[id] {
		return
	}
	b.acked[id] = lsn
	close(b.moved)
	b.moved = make(chan struct{})
}

// holding counts the followers among peers that hold lsn, and returns the
// channel that is closed on the next acknowledgement.
func (b *ackBoard) holding(peers map[string]bool, lsn uint64) (int, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for id, acked := range b.acked {
		if peers[id] && acked >= lsn {
			n++
		}
	}
	return n, b.moved
}

// waitQuorum holds a logged write until a majority holds lsn, the leader
// stops leading, or the timeout passes.
func (c *clusterRuntime) waitQuorum(lsn uint64) error {
	followers := map[string]bool{}
	for _, p := range c.settings.node.Peers {
		followers[p.ID] = p.ID != c.s.NodeID
	}
	need := c.settings.node.Majority() - 1 // this node holds it
	deadline := time.NewTimer(c.settings.ackTimeout)
	defer deadline.Stop()
	for {
		n, moved := c.acks.holding(followers, lsn)
		if n >= need {
			return nil
		}
		if err := c.writeError(); err != nil {
			return fmt.Errorf("%w: %w", errQuorumTimeout, err)
		}
		select {
		case <-moved:
		case <-deadline.C:
			return fmt.Errorf("%w after %s (LSN %d, %d of %d followers needed)", errQuorumTimeout, c.settings.ackTimeout, lsn, n, need)
		}
	}
}
