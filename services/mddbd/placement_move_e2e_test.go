package main

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// GO-052: a collection moved between groups while it is written and read.

// moveLoad writes and reads a collection through every node until finish,
// and keeps the keys whose writes were acknowledged and every error seen.
type moveLoad struct {
	mu      sync.Mutex
	acked   []string
	errs    []string
	reads   int
	writes  int
	stop    chan struct{}
	stopped sync.WaitGroup
}

func startMoveLoad(tp *testPlacement, c string, seeded int) *moveLoad {
	l := &moveLoad{stop: make(chan struct{})}
	for w := range 3 {
		l.stopped.Go(func() { l.write(tp, c, w) })
	}
	l.stopped.Go(func() { l.read(tp, c, seeded) })
	return l
}

func (l *moveLoad) stopping() bool {
	select {
	case <-l.stop:
		return true
	default:
		return false
	}
}

func (l *moveLoad) write(tp *testPlacement, c string, w int) {
	for i := 0; !l.stopping(); i++ {
		node := tp.nodes[(w+i)%len(tp.nodes)]
		key := fmt.Sprintf("w%d-%05d", w, i)
		err := node.add(c, key, "written during the move "+key)
		l.mu.Lock()
		l.writes++
		if err != nil {
			l.errs = append(l.errs, "write "+key+" via "+node.id+": "+err.Error())
		} else {
			l.acked = append(l.acked, key)
		}
		l.mu.Unlock()
	}
}

func (l *moveLoad) read(tp *testPlacement, c string, seeded int) {
	for i := 0; !l.stopping(); i++ {
		node := tp.nodes[i%len(tp.nodes)]
		key := fmt.Sprintf("doc-%04d", i%seeded)
		_, err := node.call(http.MethodPost, "/v1/get", map[string]string{"collection": c, "key": key, "lang": "en"}, nil)
		l.mu.Lock()
		l.reads++
		if err != nil {
			l.errs = append(l.errs, "read "+key+" via "+node.id+": "+err.Error())
		}
		l.mu.Unlock()
	}
}

func (l *moveLoad) finish() {
	close(l.stop)
	l.stopped.Wait()
}

// mustOwner is the group the meta leader's map places c in.
func mustOwner(t *testing.T, tp *testPlacement, c string) string {
	t.Helper()
	g, ok := tp.leader("meta").s.placement.owner(c)
	if !ok {
		t.Fatal("the meta leader has no map")
	}
	return g
}

// otherGroup is a group of tp other than g, not the meta group if it can.
func otherGroup(g string) string {
	if g == "g2" {
		return "g3"
	}
	return "g2"
}

func TestPlacementMoveUnderLoadLosesNoWriteAndServesReads(t *testing.T) {
	tp := threeGroups(t)
	const c, seeded = "moving", 200
	from := mustOwner(t, tp, c)
	leader := tp.leader(from)
	for i := range seeded {
		if err := writeDoc(leader.s, c, i); err != nil {
			t.Fatal(err)
		}
	}
	to := otherGroup(from)
	load := startMoveLoad(tp, c, seeded)
	time.Sleep(100 * time.Millisecond)
	var moved struct {
		Move moveStatus `json:"move"`
	}
	via := tp.nodes[len(tp.nodes)-1] // not in the meta group: the request is forwarded
	if _, err := via.call(http.MethodPost, "/v1/placement/move?wait=true", map[string]string{"collection": c, "group": to}, &moved); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	load.finish()
	t.Logf("move %s→%s: %+v; %d writes, %d reads during it", from, to, moved.Move, load.writes, load.reads)
	if len(load.errs) > 0 {
		t.Fatalf("%d of %d requests failed, first: %s", len(load.errs), load.writes+load.reads, load.errs[0])
	}
	if got := mustOwner(t, tp, c); got != to {
		t.Fatalf("the map places %s in %s, want %s", c, got, to)
	}
	sum := checkMovedCollection(t, tp, c, to, seeded+len(load.acked))
	// The old group drops its copy after the grace; the new group's
	// followers hold it.
	waitE2E(t, "the old group to drop its copy", func() bool {
		for _, n := range tp.nodes {
			if _, count := n.s.collectionChecksum(c); n.group == from && count > 0 {
				return false
			}
		}
		return true
	})
	for _, n := range tp.nodes {
		if n.group == to {
			waitE2E(t, n.id+" to hold the moved collection", func() bool {
				got, _ := n.s.collectionChecksum(c)
				return got == sum
			})
		}
	}
}

// checkMovedCollection checks that group to holds all want documents of c
// and that every node outside it answers with that checksum, which it
// returns.
func checkMovedCollection(t *testing.T, tp *testPlacement, c, to string, want int) string {
	t.Helper()
	sum, count := tp.leader(to).s.collectionChecksum(c)
	if count != want {
		t.Fatalf("the new group holds %d documents, want %d (seeded and acknowledged)", count, want)
	}
	for _, n := range tp.nodes {
		if got := nodeChecksum(t, n, c); n.group != to && (got.Checksum != sum || got.DocumentCount != want) {
			t.Fatalf("through %s: %+v, want %s/%d", n.id, got, sum, want)
		}
	}
	return sum
}
