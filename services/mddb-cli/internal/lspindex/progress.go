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
// has been quiet for a moment. rust-analyzer says when it is done itself
// (experimental/serverStatus), and that is waited for instead. The canary
// still judges the answers.

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

// serverStatus is rust-analyzer's experimental/serverStatus: quiescent once
// the workspace is loaded and nothing is pending, health "error" when it
// could not load it.
type serverStatus struct {
	Health    string `json:"health"`
	Quiescent bool   `json:"quiescent"`
	Message   string `json:"message"`
}

// trackStatus keeps the latest status.
func (c *conn) trackStatus(params json.RawMessage) {
	var s serverStatus
	if json.Unmarshal(params, &s) != nil {
		return
	}
	c.mu.Lock()
	c.status = &s
	c.mu.Unlock()
}

// errUnhealthy is the server's own word that it failed to load the project.
type errUnhealthy struct{ message string }

func (e *errUnhealthy) Error() string { return e.message }

// idle reports whether the server is done loading. A server that reports
// its status is idle once quiescent, whatever progress it still runs; one
// quiescent with health "error" ends the wait with *errUnhealthy. Any other
// is idle once no progress is open and none was reported for
// progressSettle.
func (c *conn) idle() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.status; s != nil {
		if s.Quiescent && s.Health == "error" {
			return false, &errUnhealthy{message: s.Message}
		}
		return s.Quiescent, nil
	}
	return len(c.progress) == 0 && time.Since(c.lastProgress) >= progressSettle, nil
}

// waitIdle returns once the server is idle, after limit, or when the stream
// ends (the next request reports that). Only ctx ending and a server that
// reports it failed to load are errors.
func (c *conn) waitIdle(ctx context.Context, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	tick := time.NewTicker(progressSettle / 10)
	defer tick.Stop()
	for {
		idle, err := c.idle()
		if err != nil {
			return err
		}
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
