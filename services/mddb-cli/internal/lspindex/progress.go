package lspindex

import (
	"context"
	"encoding/json"
	"time"
)

// LSP has no "workspace loaded" signal, but servers announce long work —
// loading packages, indexing the workspace — as work-done progress
// ($/progress begin ... end). phpactor answers references with an exception
// while its index is still being built, and gopls answers from the files
// seen so far while it loads, so references are asked only once the server
// has been quiet for a moment. The canary still judges the answers.

// progressSettle is how long the server must report no progress before it
// counts as idle; measured from the start, so a server that begins
// indexing just after initialized is not missed. A variable for the tests.
var progressSettle = 500 * time.Millisecond

// maxLoadWait caps the wait: a server that keeps a progress open forever
// (a file watcher, say) must not stall the run until Options.Timeout. After
// it the run goes on and the canary decides whether the answers are usable.
var maxLoadWait = 10 * time.Minute

// trackProgress records $/progress begin and end by token.
func (c *conn) trackProgress(params json.RawMessage) {
	var p struct {
		Token json.RawMessage `json:"token"`
		Value struct {
			Kind string `json:"kind"`
		} `json:"value"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastProgress = time.Now()
	switch p.Value.Kind {
	case "begin":
		c.progress[string(p.Token)] = true
	case "end":
		delete(c.progress, string(p.Token))
	}
}

// waitIdle returns once no progress is open and none was reported for
// progressSettle, after limit, or when the stream ends (the next request
// reports that). Only ctx ending is an error.
func (c *conn) waitIdle(ctx context.Context, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	tick := time.NewTicker(progressSettle / 10)
	defer tick.Stop()
	for {
		c.mu.Lock()
		idle := len(c.progress) == 0 && time.Since(c.lastProgress) >= progressSettle
		c.mu.Unlock()
		if idle || time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return nil
		case <-tick.C:
		}
	}
}
