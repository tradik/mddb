package main

import (
	"strings"
	"testing"
	"time"
)

// A follower out of sync fails the gate only where the baseline's was in
// sync; late probes are a note.
func TestCompareReplication(t *testing.T) {
	in := &Replication{InSync: true}
	out := &Replication{OutOfSync: "catching up: 4 short", LateProbes: 2}
	for _, tc := range []struct {
		name            string
		base, cur       *Replication
		failures, notes int
	}{
		{"not measured", in, nil, 0, 0},
		{"in sync", in, in, 0, 0},
		{"regressed", in, out, 1, 1},
		{"no baseline", nil, out, 1, 0},
		{"as in the baseline", &Replication{}, out, 0, 2},
	} {
		var c comparison
		c.compareReplication(tc.base, tc.cur)
		if len(c.Failures) != tc.failures || len(c.Notes) != tc.notes {
			t.Errorf("%s: failures %v notes %v", tc.name, c.Failures, c.Notes)
		}
		if tc.failures > 0 && !strings.Contains(c.Failures[0], "4 short") {
			t.Errorf("%s: the failure does not say where: %v", tc.name, c.Failures)
		}
	}
}

func TestRenderOutOfSync(t *testing.T) {
	r := sample()
	r.Replication = &Replication{OutOfSync: "a | b", LagLoad: 20, LagProbes: 5, LateProbes: 5}
	md := render(r)
	if !containsAll(md, "| Follower out of sync | a / b |", "5 probes under 20 writes/s: p50 / p95 / p99 | — (no probe arrived) |",
		"| 5 of 5 |", "| not in sync after 0 s |") {
		t.Errorf("out of sync:\n%s", md)
	}
}

// A follower that never reaches the leader is recorded, not a failed run.
func TestSettleRecordsWhereItStopped(t *testing.T) {
	old := pollPeriod
	pollPeriod = time.Millisecond
	t.Cleanup(func() { pollPeriod = old })
	pair := replicaPair{
		leader:     nodeAt(answering(t, 200, `{"checksum":"a","documentCount":2}`)),
		follower:   nodeAt(answering(t, 200, `{"checksum":"a","documentCount":1}`)),
		collection: "c", timeout: 5 * time.Millisecond,
	}
	var r Replication
	if pair.settle(&r, "stage") || !strings.HasPrefix(r.OutOfSync, "stage: follower has 1 documents (checksum a), leader 2") {
		t.Errorf("out of sync recorded as %q", r.OutOfSync)
	}
}

// An interrupted run kills the servers it started.
func TestKillRunning(t *testing.T) {
	n, err := newNode(writeScript(t, "#!/bin/sh\nwhile :; do sleep 0.05; done\n"), t.TempDir(), "n")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.start(); err != nil {
		t.Fatal(err)
	}
	killRunning()
	select {
	case <-n.exited:
	case <-time.After(5 * time.Second):
		n.kill()
		t.Fatal("killRunning left the server running")
	}
}
