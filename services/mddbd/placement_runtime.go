package main

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
)

// Collection placement across groups of nodes (GO-052, stage 1).
//
// With MDDB_PLACEMENT_GROUPS the nodes form several groups. Each group is a
// cluster of its own (GO-051: a leader and followers holding the same data),
// and each collection belongs as a whole to exactly one group: its file
// (MDDB_STORAGE_LAYOUT=per-collection) and its settings live there and
// nowhere else. A collection is never split across groups.
//
// One group is the meta group. Its leader keeps the placement map — which
// group holds each collection, versioned — and the global settings: users,
// keys, tenants, module switches, aliases, webhooks, automations, embedding
// and chat settings. Every node fetches the map; the leader of every other
// group keeps a copy of the global settings, copied from the meta leader
// every few seconds (placement_global.go).
//
// A collection the map does not name yet belongs to the group the consistent
// hash of its name gives (consistent_hash.go); the group that comes to hold
// it records it in the map, so adding a group later moves nothing.
//
// Every node routes (placement_route.go): a request for a collection of
// another group goes to that group's leader, a write to a collection of its
// own group to its own leader, and a write to a global setting to the meta
// group's leader. A move copies a collection to another group while it takes
// writes, and switches the map once the copy has every write
// (placement_move.go).
type placementRuntime struct {
	s     *Server
	cfg   placementConfig
	ring  *ConsistentHash
	table atomic.Pointer[placementTable]

	// mapMu orders the meta leader's map writes with what it adopts: a
	// move's switch reaches the target before this node takes it.
	mapMu     sync.Mutex
	changedMu sync.Mutex    // serialises adopting maps
	changed   chan struct{} // closed and replaced when a newer map is adopted

	leaders leaderCache
	local   localCollections
	moves   moveBoard
	client  *http.Client // forwarded requests

	stop chan struct{}
	done chan struct{}
}

// initPlacementFromEnv joins the placement MDDB_PLACEMENT_GROUPS describes,
// if any. A configuration that cannot work stops the start.
func (s *Server) initPlacementFromEnv() {
	pc, ok, err := placementConfigFromEnv()
	if err == nil && ok {
		err = s.initPlacement(pc)
	}
	fatalOnErr(err, "Invalid placement configuration")
}

// errPlacementLayout explains why placement needs one file per collection.
var errPlacementLayout = errors.New("MDDB_PLACEMENT_GROUPS needs MDDB_STORAGE_LAYOUT=per-collection: " +
	"a collection is placed, copied and removed as its own file (mddbd -split-collections converts a database)")

// initPlacement builds the node's placement runtime. It needs the node's
// cluster (initCluster) and the per-collection layout.
func (s *Server) initPlacement(pc placementConfig) error {
	if !perCollectionLayout() {
		return errPlacementLayout
	}
	if s.cluster == nil {
		return errors.New("MDDB_PLACEMENT_GROUPS needs MDDB_CLUSTER_PEERS: each group is a cluster")
	}
	if err := pc.validate(s.NodeID, s.cluster.settings.node.Peers); err != nil {
		return err
	}
	p := &placementRuntime{s: s, cfg: pc, ring: newConsistentHash(pc.names), changed: make(chan struct{}),
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	p.local.init()
	p.moves.init()
	s.placement = p
	s.Binlog.SetEntryGate(p.entryGate)
	slog.Info("placement configured", "group", pc.self, "metaGroup", pc.meta, "groups", len(pc.names),
		"route", map[bool]string{true: "redirect", false: "forward"}[pc.redirect])
	return nil
}

// start runs the node's placement loop (placement_sync.go).
func (p *placementRuntime) start() {
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	go p.loop()
}

// shutdown stops the loop.
func (p *placementRuntime) shutdown() {
	if p.stop == nil {
		return
	}
	close(p.stop)
	<-p.done
}

// current is the map the node holds, nil before the first one arrives.
func (p *placementRuntime) current() *placementTable { return p.table.Load() }

// owner is the group holding collection c, and whether the node knows: a
// node without a map yet cannot tell.
func (p *placementRuntime) owner(c string) (string, bool) {
	t := p.current()
	if t == nil {
		return "", false
	}
	if e, ok := t.Entries[c]; ok {
		return e.Group, true
	}
	return p.ring.Get(c), true
}

// adopt takes t if it is newer than the map the node holds, and wakes what
// waits for a change. It reports whether it took it.
func (p *placementRuntime) adopt(t *placementTable) bool {
	p.changedMu.Lock()
	defer p.changedMu.Unlock()
	if old := p.table.Load(); t == nil || (old != nil && !t.newerThan(old)) {
		return false
	}
	p.table.Store(t)
	close(p.changed)
	p.changed = make(chan struct{})
	return true
}

// changes is closed when a newer map is adopted.
func (p *placementRuntime) changes() <-chan struct{} {
	p.changedMu.Lock()
	defer p.changedMu.Unlock()
	return p.changed
}

// leads reports whether this node leads its group.
func (p *placementRuntime) leads() bool { return p.s.cluster.node.CanWrite() == nil }

// leadsMeta reports whether this node leads the meta group.
func (p *placementRuntime) leadsMeta() bool { return p.cfg.self == p.cfg.meta && p.leads() }
