package main

import "context"

// Bounded vector reindexes (GO-054). A reindex sends every document of a
// collection to the embedding provider and rewrites its vectors; several at
// once — one tenant reindexing all its collections — took the provider and
// the disk from everybody. MDDB_REINDEX_MAX_CONCURRENT bounds the server,
// and a tenant's MaxConcurrentReindexes (default
// MDDB_TENANT_MAX_CONCURRENT_REINDEXES) bounds one tenant. A reindex over a
// limit waits for a slot — an async job shows status "queued" meanwhile —
// rather than failing: it was asked for and will run. Both default to 0, no
// limit.

// reindexSlotKey marks a context that already holds the reindex slots, so a
// job that took them before calling reindexVectors is not counted twice.
type reindexSlotKey struct{}

// reindexSlot takes the collection's tenant's reindex slot and the server's,
// waiting as long as ctx allows. onWait, when not nil, is called before
// waiting. The release function must be called; on error it does nothing.
func (s *Server) reindexSlot(ctx context.Context, collection string, onWait func()) (context.Context, func(), error) {
	if ctx.Value(reindexSlotKey{}) != nil {
		return ctx, func() {}, nil
	}
	rt := s.tenantLimiter()
	var held []chan struct{}
	release := func() {
		for _, ch := range held {
			<-ch
		}
	}
	tenant := TenantOfCollection(collection)
	if n := s.effectiveLimits(tenant).MaxConcurrentReindexes; tenant != "" && n > 0 {
		ch := rt.slotChan("r|"+tenant, n)
		if err := takeSlot(ctx, ch, onWait); err != nil {
			return ctx, func() {}, err
		}
		held = append(held, ch)
	}
	if rt.reindexAll != nil {
		if err := takeSlot(ctx, rt.reindexAll, onWait); err != nil {
			release()
			return ctx, func() {}, err
		}
		held = append(held, rt.reindexAll)
	}
	return context.WithValue(ctx, reindexSlotKey{}, true), release, nil
}

// takeSlot takes a slot of ch, waiting until ctx ends when none is free.
func takeSlot(ctx context.Context, ch chan struct{}, onWait func()) error {
	select {
	case ch <- struct{}{}:
		return nil
	default:
	}
	if onWait != nil {
		onWait()
	}
	select {
	case ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
