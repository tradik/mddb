package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// What a group's leader knows of its own collections during a move
// (GO-052): which are fenced (writes held while the map switches), which
// it is importing or dropping, and how many routed writes to each are
// running.
type localCollections struct {
	mu        sync.Mutex
	fenced    map[string]chan struct{} // routed writes wait; closed when the fence is lifted
	gated     map[string]bool          // the entry gate refuses writes (fence complete)
	importing map[string]bool
	dropping  map[string]bool
	inflight  map[string]int
	idle      map[string]chan struct{} // closed when inflight drops to 0
}

func (l *localCollections) init() {
	l.fenced = map[string]chan struct{}{}
	l.gated = map[string]bool{}
	l.importing = map[string]bool{}
	l.dropping = map[string]bool{}
	l.inflight = map[string]int{}
	l.idle = map[string]chan struct{}{}
}

// errFenceBusy ends a fence that writes still running did not let through.
var errFenceBusy = errors.New("writes to the collection did not finish in time")

// enter counts a routed write to c as running and returns its end, or,
// while c is fenced, the channel to wait on instead. Checking and counting
// under one lock is what lets a fence know every write it must wait for.
func (l *localCollections) enter(c string) (func(), <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ch, fenced := l.fenced[c]; fenced {
		return nil, ch
	}
	l.inflight[c]++
	return func() { l.leave(c) }, nil
}

// leave ends a write enter counted.
func (l *localCollections) leave(c string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inflight[c]--
	if l.inflight[c] > 0 {
		return
	}
	delete(l.inflight, c)
	if ch, ok := l.idle[c]; ok {
		close(ch)
		delete(l.idle, c)
	}
}

// fence holds new writes to c and waits until the running ones end, at
// most d. It lifts itself and fails when they do not.
func (l *localCollections) fence(c string, d time.Duration) error {
	l.mu.Lock()
	if _, already := l.fenced[c]; !already {
		l.fenced[c] = make(chan struct{})
	}
	var idle chan struct{}
	if l.inflight[c] > 0 {
		if idle = l.idle[c]; idle == nil {
			idle = make(chan struct{})
			l.idle[c] = idle
		}
	}
	l.mu.Unlock()
	if idle != nil {
		select {
		case <-idle:
		case <-time.After(d):
			l.lift(c)
			return errFenceBusy
		}
	}
	// The routed writes have ended; from now on no write to c is logged.
	l.mark(l.gated, c, true)
	return nil
}

// lift ends c's fence; the writes it held go on.
func (l *localCollections) lift(c string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ch, ok := l.fenced[c]; ok {
		close(ch)
		delete(l.fenced, c)
	}
	delete(l.gated, c)
}

// isGated reports whether the entry gate refuses writes to c.
func (l *localCollections) isGated(c string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gated[c]
}

func (l *localCollections) isImporting(c string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.importing[c]
}

// mark sets or clears a flag of c in m.
func (l *localCollections) mark(m map[string]bool, c string, on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if on {
		m[c] = true
		return
	}
	delete(m, c)
}

// passes reports whether c may be written by the node itself, whatever the
// map says: a copy being imported or dropped.
func (l *localCollections) passes(c string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.importing[c] || l.dropping[c]
}

// fencedNames is every fenced collection.
func (l *localCollections) fencedNames() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.fenced))
	for c := range l.fenced {
		out = append(out, c)
	}
	return out
}

// collectionElsewhereError is a write this node does not take: another
// group holds the collection, or a move is switching it over.
type collectionElsewhereError struct {
	Collection string
	Group      string // "" while the map is unknown or the collection fenced
	LeaderHTTP string
}

func (e *collectionElsewhereError) Error() string {
	switch {
	case e.Group == "":
		return fmt.Sprintf("collection %q is being placed; retry shortly", e.Collection)
	case e.LeaderHTTP == "":
		return fmt.Sprintf("collection %q is held by node group %q, whose leader is not known yet; retry shortly", e.Collection, e.Group)
	}
	return fmt.Sprintf("collection %q is held by node group %q: send the request to %s", e.Collection, e.Group, e.LeaderHTTP)
}

// enterWrite admits a routed write to c on this node: it waits while c is
// fenced — at most the fence timeout — and reports false when the write
// must be routed again (the map changed) or refused (the wait ran out).
func (p *placementRuntime) enterWrite(ctx context.Context, c string) (release func(), again bool, err error) {
	deadline := time.NewTimer(p.cfg.fenceTimeout)
	defer deadline.Stop()
	changed := p.changes()
	for {
		release, wait := p.local.enter(c)
		if release != nil {
			return release, false, nil
		}
		select {
		case <-wait:
		case <-changed:
			return nil, true, nil
		case <-deadline.C:
			return nil, false, errFenceHeld
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

// errFenceHeld answers a write held longer than the fence timeout.
var errFenceHeld = errors.New("the collection is being moved to another node group; retry shortly")
