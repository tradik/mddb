package main

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// ConsistentHash places a key on one of a fixed set of members: the default
// group of a collection the placement map does not name yet (GO-052). Each
// member owns ringReplicas points on a ring of FNV-1a hashes, and a key goes
// to the member owning the first point at or after the key's hash. Adding a
// group moves only the keys that land on its new points; every other key
// keeps its member.
//
// The ring is built once from the configuration and never changes, so it
// needs no lock.
type ConsistentHash struct {
	points  []uint32          // sorted
	members map[uint32]string // point -> member
}

// ringReplicas is how many points each member owns.
const ringReplicas = 150

// newConsistentHash builds the ring of members.
//
// The replica index comes first in each hashed point name, and that ordering
// is the whole point (TEST-002). With the member first, every point of one
// member shared a prefix, and FNV-1a placed them in long contiguous arcs:
// measured at 4 members x 150 points, one member owned a run of 50
// consecutive positions, and the key distribution ran from 39.6% to 160.8%
// of an even share. Swapping the fields brings the longest run to 5 and the
// spread to 91-111%.
func newConsistentHash(members []string) *ConsistentHash {
	ch := &ConsistentHash{members: map[uint32]string{}}
	for _, m := range members {
		for i := range ringReplicas {
			p := ringHash(fmt.Sprintf("%d-%s", i, m))
			ch.members[p] = m // a collision, vanishingly rare, goes to the later member everywhere alike
			ch.points = append(ch.points, p)
		}
	}
	sort.Slice(ch.points, func(i, j int) bool { return ch.points[i] < ch.points[j] })
	return ch
}

// Get is the member key belongs to; "" on an empty ring.
func (ch *ConsistentHash) Get(key string) string {
	if len(ch.points) == 0 {
		return ""
	}
	h := ringHash(key)
	i := sort.Search(len(ch.points), func(i int) bool { return ch.points[i] >= h })
	if i == len(ch.points) {
		i = 0
	}
	return ch.members[ch.points[i]]
}

func ringHash(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key)) // #nosec G104 -- fnv's Write never returns an error
	return h.Sum32()
}
