package main

import (
	"context"
	"log/slog"
	"time"

	proto "mddb/proto"
)

// The placement loop of every node (GO-052), once per MDDB_PLACEMENT_REFRESH:
// fetch the map, learn where the other groups' leaders are and, on a
// group's leader, do the group's part — record the collections it holds,
// copy the global settings, finish fences and drop copies the map placed
// elsewhere. The meta leader also seals the map in its term and gives up
// moves nobody runs any more.

// globalSyncEvery is how many rounds pass between copies of the global
// settings.
const globalSyncEvery = 5

// rpcTimeout bounds one placement call to another node.
const rpcTimeout = 5 * time.Second

func (p *placementRuntime) loop() {
	defer close(p.done)
	tick := time.NewTicker(p.cfg.refresh)
	defer tick.Stop()
	seen := map[string]time.Time{}
	for round := 0; ; round++ {
		p.step(seen, round)
		select {
		case <-p.stop:
			return
		case <-tick.C:
		}
	}
}

// step is one round of the loop.
func (p *placementRuntime) step(seen map[string]time.Time, round int) {
	p.refreshMap()
	p.refreshLeaders()
	if !p.leads() {
		return
	}
	if p.cfg.self == p.cfg.meta {
		p.metaDuties()
	} else if round%globalSyncEvery == 0 {
		if err := p.syncGlobal(); err != nil {
			slog.Debug("placement: copying the global settings failed", "err", err)
		}
	}
	p.pinLocal()
	p.liftStaleFences()
	p.dropStale(seen)
}

// refreshMap fetches the map: the meta leader reads its own, every other
// node asks the meta leader.
func (p *placementRuntime) refreshMap() {
	if p.leadsMeta() {
		p.adopt(p.storedMap())
		return
	}
	c, err := p.leaderClient(p.cfg.meta)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	m, err := c.GetPlacement(withReplicationSecret(ctx), &proto.GetPlacementRequest{})
	if err != nil {
		p.leaders.forget(p.cfg.meta)
		return
	}
	p.adopt(tableFromProto(m))
}

// metaDuties are the meta leader's: a map written in an earlier term is
// written again in this one, so every node takes it over any map a former
// leader wrote and lost; and a move in the map that no coordinator here
// runs — its leader changed — is given up.
func (p *placementRuntime) metaDuties() {
	term := p.s.cluster.node.Term()
	if p.storedMap().Term < term {
		if _, err := p.changeMap(func(*placementTable) (bool, error) { return true, nil }); err != nil {
			slog.Warn("placement: sealing the map in this term failed", "err", err)
			return
		}
	}
	for c, e := range p.current().Entries {
		if e.MovingTo != "" && !p.moves.running(c) {
			slog.Warn("placement: giving up a move its coordinator no longer runs", "collection", c, "to", e.MovingTo)
			p.abandonMove(c)
		}
	}
}

// pinLocal records in the map the collections this group holds that the
// map does not place yet, so they stay here whatever the hash would say.
func (p *placementRuntime) pinLocal() {
	t, cf := p.current(), p.s.collectionStore()
	if t == nil || cf == nil {
		return
	}
	var missing []string
	for _, c := range cf.names() {
		if _, placed := t.Entries[c]; !placed {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return
	}
	req := &proto.PinCollectionsRequest{Group: p.cfg.self, Collections: missing}
	if p.leadsMeta() {
		_, _ = p.pin(req)
		return
	}
	c, err := p.leaderClient(p.cfg.meta)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	if m, err := c.PinCollections(withReplicationSecret(ctx), req); err == nil {
		p.adopt(tableFromProto(m))
	}
}

// liftStaleFences lifts the fence of a collection no move is switching any
// more: the move ended, or was given up.
func (p *placementRuntime) liftStaleFences() {
	t := p.current()
	for _, c := range p.local.fencedNames() {
		if t == nil || t.Entries[c].MovingTo == "" {
			p.local.lift(c)
		}
	}
}
