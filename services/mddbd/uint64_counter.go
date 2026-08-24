package main

import "sync/atomic"

// uint64Counter is an atomic counter with a high-water mark, used by the
// search limiter. Kept separate so the limiter reads as policy rather than as
// atomics.
type uint64Counter struct{ v atomic.Uint64 }

func (c *uint64Counter) add(delta uint64) uint64 { return c.v.Add(delta) }
func (c *uint64Counter) load() uint64            { return c.v.Load() }

// observeMax raises the counter to v if v is larger, retrying on races.
func (c *uint64Counter) observeMax(v uint64) {
	for {
		cur := c.v.Load()
		if v <= cur || c.v.CompareAndSwap(cur, v) {
			return
		}
	}
}
