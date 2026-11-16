package main

import (
	"fmt"
	"testing"
)

func TestConsistentHashEmptyRing(t *testing.T) {
	if got := newConsistentHash(nil).Get("anything"); got != "" {
		t.Fatalf("empty ring placed a key on %q", got)
	}
}

func TestConsistentHashIsDeterministicAndCoversEveryMember(t *testing.T) {
	members := []string{"meta", "g2", "g3", "g4"}
	a, b := newConsistentHash(members), newConsistentHash(members)
	counts := map[string]int{}
	const keys = 10000
	for i := range keys {
		k := fmt.Sprintf("collection-%d", i)
		if a.Get(k) != b.Get(k) {
			t.Fatalf("two rings of the same members disagree on %q", k)
		}
		counts[a.Get(k)]++
	}
	// TEST-002: within 91-111% of an even share at 150 points per member.
	for _, m := range members {
		share := float64(counts[m]) / (keys / float64(len(members)))
		if share < 0.85 || share > 1.15 {
			t.Errorf("%s holds %.0f%% of an even share", m, share*100)
		}
	}
}

func TestConsistentHashAddingAMemberMovesOnlyItsKeys(t *testing.T) {
	before := newConsistentHash([]string{"a", "b", "c"})
	after := newConsistentHash([]string{"a", "b", "c", "d"})
	for i := range 5000 {
		k := fmt.Sprintf("k%d", i)
		if got := after.Get(k); got != before.Get(k) && got != "d" {
			t.Fatalf("%q moved from %s to %s, not to the new member", k, before.Get(k), got)
		}
	}
}

func TestConsistentHashWrapsPastTheLastPoint(t *testing.T) {
	ch := newConsistentHash([]string{"only"})
	ch.points = ch.points[:1] // one point: every larger hash wraps to it
	ch.members = map[uint32]string{ch.points[0]: "only"}
	for i := range 50 {
		if got := ch.Get(fmt.Sprint(i)); got != "only" {
			t.Fatalf("Get = %q", got)
		}
	}
}
