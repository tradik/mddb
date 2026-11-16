package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"mddb/internal/cluster"
	proto "mddb/proto"
)

// Moving a collection to another group (GO-052), run by the meta leader.
//
//  1. The map marks the collection as moving; it stays where it is and
//     serves reads and writes there.
//  2. The target's leader imports it: a copy of its keys, then every write
//     the source logs to it, applied as it comes.
//  3. When the target is close behind, the source fences the collection:
//     routed writes to it wait, the running ones finish, and the source's
//     log takes no more writes to it. The source answers with the LSN its
//     last write to the collection has.
//  4. Once the target has applied that LSN the map names the target; the
//     target learns it first, then the source, whose waiting writes go to
//     the target. Nothing acknowledged is lost: every write the source took
//     is in the copy, and the source takes none after the fence.
//  5. The source drops its copy after the drop grace (placement_drop.go).
//
// Anything going wrong before step 4 gives the move up: the map clears the
// mark, the fence lifts, the collection stays where it was, and the target
// drops what it copied.

// Errors of a move request.
var (
	errMoveSameGroup = errors.New("the collection is already in that group")
	errMoveRunning   = errors.New("a move of the collection is running")
	errNotMetaLeader = errors.New("this node does not lead the meta group")
)

// Bounds of the steps of a move.
const (
	catchUpRounds = 3
	catchUpQuick  = 250 * time.Millisecond // a round this short means close behind
)

// catchUpTimeout bounds each wait for the target; a variable so a test can
// shorten it.
var catchUpTimeout = 2 * time.Minute

// startMove begins moving c to group to and returns the move.
func (p *placementRuntime) startMove(c, to string) (*move, error) {
	if !p.leadsMeta() {
		return nil, errNotMetaLeader
	}
	if _, ok := p.cfg.groups[to]; !ok {
		return nil, fmt.Errorf("unknown node group %q", to)
	}
	from, _ := p.owner(c)
	if from == to {
		return nil, errMoveSameGroup
	}
	m := p.moves.begin(c, from, to)
	if m == nil {
		return nil, errMoveRunning
	}
	if _, err := p.changeMap(func(t *placementTable) (bool, error) {
		t.Entries[c] = placementEntry{Group: from, MovingTo: to}
		return true, nil
	}); err != nil {
		p.moves.finish(m, err)
		return m, nil
	}
	p.pushMap(to, from)
	go p.runMove(m)
	return m, nil
}

// runMove runs m to its end.
func (p *placementRuntime) runMove(m *move) {
	st := p.moves.get(m)
	ctx, cancel := context.WithCancel(context.Background())
	err := p.copyAndSwitch(ctx, m)
	cancel() // ends the target's import
	if err != nil {
		slog.Warn("placement: move given up", "collection", st.Collection, "from", st.From, "to", st.To, "err", err)
		p.abandonMove(st.Collection)
	} else {
		slog.Info("placement: collection moved", "collection", st.Collection, "from", st.From, "to", st.To,
			"elapsed", time.Since(m.start).Round(time.Millisecond))
	}
	p.moves.finish(m, err)
}

// copyAndSwitch is steps 2 to 4.
func (p *placementRuntime) copyAndSwitch(ctx context.Context, m *move) error {
	st := p.moves.get(m)
	dst, err := p.leaderClient(st.To)
	if err != nil {
		return err
	}
	stream, err := dst.ImportCollection(withReplicationSecret(ctx),
		&proto.ImportCollectionRequest{Collection: st.Collection, SourceGroup: st.From})
	if err != nil {
		return err
	}
	w := watchImport(stream, func(pr importState) {
		p.moves.update(m, func(s *moveStatus) {
			s.KeysCopied, s.AppliedLSN = pr.keys, pr.applied
			s.SourceLSN = max(s.SourceLSN, pr.source)
		})
	})
	if err := w.wait(func(pr importState) bool { return pr.snapshotDone }, 0); err != nil {
		return err
	}
	p.moves.update(m, func(s *moveStatus) { s.Phase = movePhaseCatchingUp })
	if err := p.catchUp(m, w); err != nil {
		return err
	}
	p.moves.update(m, func(s *moveStatus) { s.Phase = movePhaseFenced })
	through, err := p.fenceSource(st)
	if err != nil {
		return err
	}
	p.moves.update(m, func(s *moveStatus) { s.SourceLSN = max(s.SourceLSN, through) })
	if err := w.wait(func(pr importState) bool { return pr.applied >= through }, catchUpTimeout); err != nil {
		return err
	}
	if err := p.switchTo(st); err != nil {
		return err
	}
	p.pushMap(st.From)
	return nil
}

// switchTo names the target in the map. The target takes the new map
// before anyone else: this node's held writes, and every node's fetch, see
// it only after, so none is sent to a target that does not know yet.
func (p *placementRuntime) switchTo(st moveStatus) error {
	p.mapMu.Lock()
	defer p.mapMu.Unlock()
	next, err := p.writeMap(func(t *placementTable) (bool, error) {
		t.Entries[st.Collection] = placementEntry{Group: st.To}
		return true, nil
	})
	if err != nil {
		return err
	}
	// Until the target has it: a target that cannot be told now learns it
	// within a refresh, and a request this node sends there meanwhile may
	// come back once.
	m := next.toProto(p.s.NodeID)
	deadline := time.Now().Add(p.cfg.fenceTimeout)
	for {
		err := p.pushTo(st.To, m)
		if err == nil || !time.Now().Before(deadline) {
			break
		}
		p.leaders.forget(st.To)
		time.Sleep(p.cfg.refresh / 4)
	}
	p.adopt(next)
	return nil
}

// catchUp waits until the target is close behind the source: a round in
// which it reaches the source's last LSN quickly, or a few rounds.
func (p *placementRuntime) catchUp(m *move, w *importWatch) error {
	st := p.moves.get(m)
	for range catchUpRounds {
		lsn, err := p.groupLSN(st.From)
		if err != nil {
			return err
		}
		p.moves.update(m, func(s *moveStatus) { s.SourceLSN = max(s.SourceLSN, lsn) })
		began := time.Now()
		if err := w.wait(func(pr importState) bool { return pr.applied >= lsn }, catchUpTimeout); err != nil {
			return err
		}
		if time.Since(began) < catchUpQuick {
			return nil
		}
	}
	return nil
}

// groupLSN is the last LSN of group's leader.
func (p *placementRuntime) groupLSN(group string) (uint64, error) {
	n, ok := p.leaderNode(group)
	if !ok {
		return 0, fmt.Errorf("node group %q has no known leader", group)
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	r, err := p.s.cluster.tr.Probe(ctx, cluster.Peer{ID: n.ID, Addr: n.GRPC})
	return r.Last.LSN, err
}

// fenceSource fences the collection on the source's leader.
func (p *placementRuntime) fenceSource(st moveStatus) (uint64, error) {
	src, err := p.leaderClient(st.From)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.fenceTimeout+rpcTimeout)
	defer cancel()
	r, err := src.FenceCollection(withReplicationSecret(ctx), &proto.FenceCollectionRequest{Collection: st.Collection})
	if err != nil {
		return 0, err
	}
	return r.ThroughLsn, nil
}

// abandonMove clears c's move from the map and lifts the source's fence.
func (p *placementRuntime) abandonMove(c string) {
	var from, to string
	if _, err := p.changeMap(func(t *placementTable) (bool, error) {
		e := t.Entries[c]
		from, to = e.Group, e.MovingTo
		t.Entries[c] = placementEntry{Group: e.Group}
		return e.MovingTo != "", nil
	}); err != nil {
		slog.Warn("placement: clearing a move from the map failed", "collection", c, "err", err)
		return
	}
	p.pushMap(from, to)
	if src, err := p.leaderClient(from); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		_, _ = src.FenceCollection(withReplicationSecret(ctx), &proto.FenceCollectionRequest{Collection: c, Release: true})
		cancel()
	}
}

// pushMap hands the current map to the leaders of groups, in order; the
// other nodes fetch it within a refresh.
func (p *placementRuntime) pushMap(groups ...string) {
	p.pushTable(p.current(), groups...)
}

// pushTable hands t to the leaders of groups, in order; a group that
// cannot be reached learns it within a refresh.
func (p *placementRuntime) pushTable(t *placementTable, groups ...string) {
	m := t.toProto(p.s.NodeID)
	for _, g := range groups {
		if g != p.cfg.self && g != "" {
			_ = p.pushTo(g, m)
		}
	}
}

// pushTo hands m to group's leader.
func (p *placementRuntime) pushTo(group string, m *proto.PlacementMap) error {
	c, err := p.leaderClient(group)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	_, err = c.PushPlacement(withReplicationSecret(ctx), m)
	return err
}

// importState is an import's last reported progress.
type importState struct {
	keys         uint64
	snapshotDone bool
	applied      uint64
	source       uint64
}

// importWatch reads an import's progress as it comes.
type importWatch struct {
	mu      sync.Mutex
	last    importState
	err     error
	changed chan struct{}
}

// watchImport reads stream in the background, calling seen for each report.
func watchImport(stream proto.MDDBReplication_ImportCollectionClient, seen func(importState)) *importWatch {
	w := &importWatch{changed: make(chan struct{})}
	go func() {
		for {
			pr, err := stream.Recv()
			w.mu.Lock()
			if err != nil {
				w.err = fmt.Errorf("the target's import ended: %w", err)
			} else {
				w.last = importState{keys: pr.KeysCopied, snapshotDone: pr.SnapshotDone, applied: pr.AppliedLsn, source: pr.SourceLsn}
				seen(w.last)
			}
			close(w.changed)
			w.changed = make(chan struct{})
			w.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return w
}

// wait waits until cond holds for the latest progress, the import fails,
// or timeout passes (0: no limit).
func (w *importWatch) wait(cond func(importState) bool, timeout time.Duration) error {
	var limit <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		limit = t.C
	}
	for {
		w.mu.Lock()
		ok, err, ch := cond(w.last), w.err, w.changed
		w.mu.Unlock()
		if ok {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ch:
		case <-limit:
			return errors.New("the target did not catch up in time")
		}
	}
}
