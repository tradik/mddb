package lspindex

import (
	"context"
	"sync"
)

// forEach runs fn over items with n workers. The first error stops the feed
// and is returned once every worker has finished, so no goroutine outlives
// the call; a ctx that ends returns its error.
func forEach[T any](ctx context.Context, n int, items []T, fn func(context.Context, T) error) error {
	inner, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	feed := make(chan T)
	for range min(n, len(items)) {
		wg.Go(func() {
			for it := range feed {
				if err := fn(inner, it); err != nil {
					once.Do(func() { first = err; cancel() })
				}
			}
		})
	}
send:
	for _, it := range items {
		select {
		case feed <- it:
		case <-inner.Done():
			break send
		}
	}
	close(feed)
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}
