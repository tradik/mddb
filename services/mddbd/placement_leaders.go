package main

import (
	"context"
	"sync"
	"time"

	"mddb/internal/cluster"
)

// Where each group's leader is (GO-052). A node knows its own group's
// leader from the election; another group's it learns by asking that
// group's nodes, and forgets when a request sent there fails.
type leaderCache struct {
	mu     sync.Mutex
	byName map[string]leaderSeen
}

type leaderSeen struct {
	id string
	at time.Time
}

// leaderStale is how long another group's leader is trusted unasked.
const leaderStale = 5 * time.Second

func (lc *leaderCache) get(group string) (leaderSeen, bool) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	l, ok := lc.byName[group]
	return l, ok
}

func (lc *leaderCache) set(group, id string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.byName == nil {
		lc.byName = map[string]leaderSeen{}
	}
	lc.byName[group] = leaderSeen{id: id, at: time.Now()}
}

// forget drops what is known of group's leader.
func (lc *leaderCache) forget(group string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	delete(lc.byName, group)
}

// leaderOf is the node leading group, "" when none is known.
func (p *placementRuntime) leaderOf(group string) string {
	if group == p.cfg.self {
		id, _, _ := p.s.cluster.node.Leader()
		return id
	}
	if l, ok := p.leaders.get(group); ok && l.id != "" {
		return l.id
	}
	return p.probeLeader(group)
}

// leaderNode is the configured node leading group.
func (p *placementRuntime) leaderNode(group string) (placementNode, bool) {
	id := p.leaderOf(group)
	if id == "" {
		return placementNode{}, false
	}
	n, g, ok := p.cfg.node(id)
	return n, ok && g == group
}

// knownLeaderHTTP is the REST address of group's leader as far as it is
// known, without asking anyone: for answers given under a lock.
func (p *placementRuntime) knownLeaderHTTP(group string) string {
	id := ""
	if group == p.cfg.self {
		id, _, _ = p.s.cluster.node.Leader()
	} else if l, ok := p.leaders.get(group); ok {
		id = l.id
	}
	n, _, _ := p.cfg.node(id)
	return n.HTTP
}

// probeLeader asks group's nodes who leads it, and remembers the answer.
func (p *placementRuntime) probeLeader(group string) string {
	for _, n := range p.cfg.groups[group] {
		ctx, cancel := context.WithTimeout(context.Background(), 2*p.s.cluster.settings.node.Heartbeat+time.Second)
		r, err := p.s.cluster.tr.Probe(ctx, cluster.Peer{ID: n.ID, Addr: n.GRPC})
		cancel()
		if err != nil {
			continue
		}
		leader := r.Leader
		if r.State == cluster.Leader {
			leader = n.ID
		}
		if _, g, ok := p.cfg.node(leader); ok && g == group {
			p.leaders.set(group, leader)
			return leader
		}
	}
	return ""
}

// refreshLeaders asks again about every other group whose leader is
// unknown or was learned long ago.
func (p *placementRuntime) refreshLeaders() {
	for _, g := range p.cfg.names {
		if g == p.cfg.self {
			continue
		}
		if l, ok := p.leaders.get(g); ok && l.id != "" && time.Since(l.at) < leaderStale {
			continue
		}
		p.probeLeader(g)
	}
}
