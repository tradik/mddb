package main

import (
	"fmt"
	"net/http"
	"testing"
)

// GO-052 acceptance, on in-process groups (placement_e2e_helpers_test.go).

// threeGroups is the acceptance setup: a meta group of three nodes, a group
// of two and a group of one.
func threeGroups(t *testing.T) *testPlacement {
	return startTestPlacement(t, map[string]int{"meta": 3, "g2": 2, "g3": 1}, "meta")
}

// writeCollections writes n collections of docs documents each, the i-th
// write through the i-th node in turn.
func writeCollections(t *testing.T, tp *testPlacement, n, docs int) []string {
	t.Helper()
	var names []string
	k := 0
	for c := range n {
		name := fmt.Sprintf("coll-%02d", c)
		names = append(names, name)
		for d := range docs {
			node := tp.nodes[k%len(tp.nodes)]
			k++
			if err := node.add(name, fmt.Sprintf("doc-%d", d), fmt.Sprintf("alpha %s word%d", name, d)); err != nil {
				t.Fatalf("writing %s through %s: %v", name, node.id, err)
			}
		}
	}
	return names
}

func TestPlacementRoutesEveryRequestToTheGroupHoldingTheCollection(t *testing.T) {
	tp := threeGroups(t)
	names := writeCollections(t, tp, 30, 3)
	perGroup := map[string]int{}
	for _, c := range names {
		owner, _ := tp.nodes[0].s.placement.owner(c)
		perGroup[owner]++
		leader := tp.leader(owner)
		want, count := leader.s.collectionChecksum(c)
		if count != 3 {
			t.Fatalf("%s: the leader of %s holds %d documents, want 3", c, owner, count)
		}
		for _, n := range tp.nodes {
			if n.group != owner && n.holds(c) {
				t.Fatalf("%s is held by %s of group %s, not only by %s", c, n.id, n.group, owner)
			}
			// Every node answers for it, with the owner's checksum.
			var got struct {
				Checksum      string `json:"checksum"`
				DocumentCount int    `json:"documentCount"`
			}
			if _, err := n.call(http.MethodGet, "/v1/checksum?collection="+c, nil, &got); err != nil {
				t.Fatal(err)
			}
			if n.group == owner && n != leader {
				continue // a follower answers from its own copy, which may lag
			}
			if got.Checksum != want || got.DocumentCount != 3 {
				t.Fatalf("%s through %s: %+v, want %s/3", c, n.id, got, want)
			}
		}
	}
	if len(perGroup) != 3 {
		t.Fatalf("30 collections landed in %d groups: %v", len(perGroup), perGroup)
	}
	t.Logf("collections per group: %v", perGroup)
}
