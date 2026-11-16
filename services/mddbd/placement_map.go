package main

import (
	"encoding/json"
	"time"

	"mddb/internal/binlog"
	proto "mddb/proto"

	bolt "go.etcd.io/bbolt"
)

// The placement map (GO-052): which group holds each collection. The meta
// group's leader keeps it under one key of its main file, written through
// its log, so its followers hold it too and a new meta leader continues it.

// placementEntry is where one collection is.
type placementEntry struct {
	Group    string `json:"group"`
	MovingTo string `json:"movingTo,omitempty"` // a move is copying it there
}

// placementTable is one version of the map. It is never changed once made:
// a change is a new table.
type placementTable struct {
	Version uint64                    `json:"version"`
	Term    uint64                    `json:"term"` // the meta group's term it was written in
	Entries map[string]placementEntry `json:"entries"`
}

var (
	bucketPlacement = []byte("placement")
	keyPlacementMap = []byte("map")
)

// newerThan orders maps by the meta group's term, then by version: a map a
// new meta leader writes wins over one its predecessor wrote and lost.
func (t *placementTable) newerThan(o *placementTable) bool {
	if t.Term != o.Term {
		return t.Term > o.Term
	}
	return t.Version > o.Version
}

// clone is a copy to change.
func (t *placementTable) clone() *placementTable {
	out := &placementTable{Version: t.Version, Term: t.Term, Entries: make(map[string]placementEntry, len(t.Entries)+1)}
	for k, v := range t.Entries {
		out.Entries[k] = v
	}
	return out
}

func (t *placementTable) toProto(leader string) *proto.PlacementMap {
	m := &proto.PlacementMap{Version: t.Version, Term: t.Term, LeaderId: leader}
	for c, e := range t.Entries {
		m.Entries = append(m.Entries, &proto.PlacementEntry{Collection: c, Group: e.Group, MovingTo: e.MovingTo})
	}
	return m
}

func tableFromProto(m *proto.PlacementMap) *placementTable {
	t := &placementTable{Version: m.Version, Term: m.Term, Entries: make(map[string]placementEntry, len(m.Entries))}
	for _, e := range m.Entries {
		t.Entries[e.Collection] = placementEntry{Group: e.Group, MovingTo: e.MovingTo}
	}
	return t
}

// storedMap is the map in this node's main file; an empty map when none was
// written yet.
func (p *placementRuntime) storedMap() *placementTable {
	var t *placementTable
	_ = p.s.DBView(func(tx *bolt.Tx) error {
		t, _ = readMap(tx.Bucket(bucketPlacement))
		return nil
	})
	return t
}

// readMap is the map stored in b (nil: no bucket yet), with its entries
// never nil; an empty map when none is stored or it cannot be read, with
// the error.
func readMap(b *bolt.Bucket) (*placementTable, error) {
	t := &placementTable{}
	var err error
	if b != nil {
		if raw := b.Get(keyPlacementMap); raw != nil {
			err = json.Unmarshal(raw, t)
		}
	}
	return t.clone(), err
}

// changeMap writes a new version of the map on the meta leader: change
// edits a copy of the stored one, and reports false when nothing is to be
// written. The new map is adopted at once.
func (p *placementRuntime) changeMap(change func(*placementTable) (bool, error)) (*placementTable, error) {
	var next *placementTable
	err := binlog.Logged(p.s.DBUpdate, p.s.Binlog, func(tx *bolt.Tx, bo *binlog.BinlogOps) error {
		b, err := tx.CreateBucketIfNotExists(bucketPlacement)
		var cur *placementTable
		if err == nil {
			cur, err = readMap(b)
		}
		if err != nil {
			return err
		}
		next = cur.clone()
		write, err := change(next)
		if err != nil || !write {
			next = cur
			return err
		}
		// Versions grow, and a new meta leader's first map is later than
		// any its predecessor wrote, as far as clocks agree.
		next.Version = max(cur.Version+1, uint64(time.Now().UnixNano())) // #nosec G115 -- a time after 1970
		next.Term = p.s.cluster.node.Term()
		raw, _ := json.Marshal(next)
		bo.Put(string(bucketPlacement), keyPlacementMap, raw)
		return b.Put(keyPlacementMap, raw)
	})
	if err != nil {
		return nil, err
	}
	p.adopt(next)
	return next, nil
}
