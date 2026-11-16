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
		checkHeldOnlyBy(t, tp, c, owner, 3)
	}
	if len(perGroup) != 3 {
		t.Fatalf("30 collections landed in %d groups: %v", len(perGroup), perGroup)
	}
	t.Logf("collections per group: %v", perGroup)
}

// checkHeldOnlyBy checks that collection c, of docs documents, is held by
// group owner alone, and that every node answers for it with its leader's
// checksum (a follower of the owner from its own copy, which may lag).
func checkHeldOnlyBy(t *testing.T, tp *testPlacement, c, owner string, docs int) {
	t.Helper()
	leader := tp.leader(owner)
	want, count := leader.s.collectionChecksum(c)
	if count != docs {
		t.Fatalf("%s: the leader of %s holds %d documents, want %d", c, owner, count, docs)
	}
	for _, n := range tp.nodes {
		if n.group != owner && n.holds(c) {
			t.Fatalf("%s is held by %s of group %s, not only by %s", c, n.id, n.group, owner)
		}
		got := nodeChecksum(t, n, c)
		if (n.group != owner || n == leader) && (got.Checksum != want || got.DocumentCount != docs) {
			t.Fatalf("%s through %s: %+v, want %s/%d", c, n.id, got, want, docs)
		}
	}
}

// checksumAnswer is the body of /v1/checksum.
type checksumAnswer struct {
	Checksum      string `json:"checksum"`
	DocumentCount int    `json:"documentCount"`
}

// nodeChecksum is /v1/checksum of c through node n.
func nodeChecksum(t *testing.T, n *placementNodeT, c string) checksumAnswer {
	t.Helper()
	var got checksumAnswer
	if _, err := n.call(http.MethodGet, "/v1/checksum?collection="+c, nil, &got); err != nil {
		t.Fatal(err)
	}
	return got
}
