package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"mddb-bench/internal/benchkit"
)

// Recovery: what a crash and an absence cost. The crash scenario kills the
// server with SIGKILL while clients write, restarts it, times it to ready and
// reads back every write the server had acknowledged. The replication
// scenario runs a leader and a follower, times a document from the leader's
// acknowledgement to the follower while the leader is written to, then takes
// the follower down, writes on, and times the follower catching up. Both
// drive the server only through its environment and its HTTP API.

// present reports whether the server at base has the document.
func present(client *http.Client, base, collection, key string) bool {
	_, err := benchkit.Post(client, base+"/v1/get", map[string]any{"collection": collection, "key": key, "lang": "en"})
	return err == nil
}

// checksum is the server's checksum and document count of a collection.
func checksum(client *http.Client, base, collection string) (string, int, error) {
	resp, err := client.Get(base + "/v1/checksum?collection=" + url.QueryEscape(collection))
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Checksum string `json:"checksum"`
		Count    int    `json:"documentCount"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("checksum: %s", resp.Status)
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out.Checksum, out.Count, err
}

// crash is the kill -9 scenario on a fresh node.
func crash(bin, root string, p profile) (*Recovery, error) {
	n, err := newNode(bin, root, "crash")
	if err != nil {
		return nil, err
	}
	defer n.kill()
	if err := n.start(); err != nil {
		return nil, err
	}
	if _, err := n.waitHealthy(p.Timeout, true); err != nil {
		return nil, err
	}
	c := textCorpus("crash", 20_000)
	w := writer{client: n.client, url: n.url(), collection: c.collection}
	acked := writeUntilKilled(n, w, c, p)

	if err := n.start(); err != nil {
		return nil, err
	}
	ready, err := n.waitHealthy(p.Timeout, true)
	if err != nil {
		return nil, fmt.Errorf("after kill -9: %w", err)
	}
	r := &Recovery{AckedWrites: len(acked), ReadySeconds: round3(ready.Seconds())}
	for _, key := range acked {
		if !present(n.client, n.url(), c.collection, key) {
			r.LostWrites++
		}
	}

	n.stop()
	if err := n.start(); err != nil {
		return nil, err
	}
	clean, err := n.waitHealthy(p.Timeout, true)
	r.CleanRestartSeconds = round3(clean.Seconds())
	return r, err
}

// writeUntilKilled writes single documents on KillWriters clients, kills the
// node after KillAfter with the writes in flight, and returns the keys the
// server acknowledged.
func writeUntilKilled(n *node, w writer, c *corpus, p profile) []string {
	var (
		mu    sync.Mutex
		acked []string
		wg    sync.WaitGroup
		dead  atomic.Bool
	)
	for client := 0; client < p.KillWriters; client++ {
		wg.Add(1)
		go func(client int) {
			defer wg.Done()
			for i := 0; !dead.Load(); i++ {
				doc := c.document(i)
				key := fmt.Sprintf("c%d-%08d", client, i)
				doc["key"] = key
				if w.addOne(doc) != nil {
					return // the server is gone, or going
				}
				mu.Lock()
				acked = append(acked, key)
				mu.Unlock()
			}
		}(client)
	}
	time.Sleep(p.KillAfter)
	n.kill()
	dead.Store(true)
	wg.Wait()
	return acked
}

// replicationSecret authenticates the follower to the leader; the leader
// refuses snapshot and binlog requests without one.
const replicationSecret = "MDDB_REPLICATION_SECRET=bench-profile" // #nosec G101 -- a throwaway value for two loopback processes the harness starts and stops

// replication is the leader and follower scenario.
func replication(bin, root string, p profile) (*Replication, error) {
	leader, err := newNode(bin, root, "leader", "MDDB_REPLICATION_ROLE=leader", "MDDB_NODE_ID=bench-leader", replicationSecret)
	if err != nil {
		return nil, err
	}
	defer leader.kill()
	follower, err := newNode(bin, root, "follower", "MDDB_REPLICATION_ROLE=follower", "MDDB_NODE_ID=bench-follower",
		"MDDB_REPLICATION_LEADER_ADDR="+leader.grpcAddr, replicationSecret)
	if err != nil {
		return nil, err
	}
	defer follower.kill()

	c := textCorpus("repl", p.BaseDocs)
	w := writer{client: leader.client, url: leader.url(), collection: c.collection}
	pair := replicaPair{leader: leader, follower: follower, collection: c.collection, timeout: p.Timeout}
	for _, step := range []func() error{
		leader.start,
		func() error { _, err := leader.waitHealthy(p.Timeout, true); return err },
		func() error { _, err := w.addAll(p.BaseDocs, p.LoadBatch, p.LoadParallel, c.document); return err },
		follower.start,
		func() error { _, err := follower.waitHealthy(p.Timeout, true); return err },
		pair.waitInSync,
	} {
		if err := step(); err != nil {
			return nil, err
		}
	}

	r := &Replication{LagProbes: p.LagProbes, LagLoad: p.LagLoad, CatchupDocs: p.CatchupDocs}
	lag, late, err := pair.lagUnderLoad(w, c, p.LagProbes, p.LagLoad)
	if err != nil {
		return nil, err
	}
	r.Lag, r.LateProbes = summarize(lag), late
	// From here a follower that does not reach the leader is a result, not
	// a failed run: the scenario records it and the gate judges it.
	pair.timeout = p.CatchupLimit
	if !pair.settle(r, "after the lag probes") {
		return r, nil
	}

	follower.stop()
	missed := func(i int) map[string]any {
		doc := c.document(i)
		doc["key"] = fmt.Sprintf("late-%08d", i)
		return doc
	}
	if _, err := w.addAll(p.CatchupDocs, p.LoadBatch, p.LoadParallel, missed); err != nil {
		return nil, err
	}
	if err := follower.start(); err != nil {
		return nil, err
	}
	r.InSync = pair.settle(r, "catching up after its downtime")
	r.CatchupSeconds = round3(time.Since(follower.started).Seconds())
	return r, nil
}

// settle waits for the follower to reach the leader and reports whether it
// did; when it did not, r.OutOfSync says where and how far apart they were.
func (rp replicaPair) settle(r *Replication, stage string) bool {
	if err := rp.waitInSync(); err != nil {
		r.OutOfSync = stage + ": " + err.Error()
		return false
	}
	return true
}

// replicaPair is a leader and the follower replicating one collection of it.
type replicaPair struct {
	leader, follower *node
	collection       string
	timeout          time.Duration
}

// waitInSync blocks until the follower's checksum of the collection equals
// the leader's.
func (rp replicaPair) waitInSync() error {
	deadline := time.Now().Add(rp.timeout)
	for {
		want, n, err := checksum(rp.leader.client, rp.leader.url(), rp.collection)
		if err != nil {
			return err
		}
		got, m, err := checksum(rp.follower.client, rp.follower.url(), rp.collection)
		if err == nil && got == want && n == m {
			return nil
		}
		if time.Now().After(deadline) {
			diff := fmt.Errorf("follower has %d documents (checksum %s), leader %d (checksum %s) after %s", m, got, n, want, rp.timeout)
			if err != nil {
				diff = fmt.Errorf("%w: %w", diff, err)
			}
			return diff
		}
		time.Sleep(pollPeriod)
	}
}

// lateProbe is how long a probe may take to reach the follower before the
// follower counts as unable to keep up with the load.
var lateProbe = 30 * time.Second

// lagUnderLoad writes load documents a second to the leader on one client
// while another writes probe marker documents and times each from the
// leader's acknowledgement to the follower returning it. Milliseconds, one
// per probe that arrived; late is how many did not arrive within lateProbe —
// probing stops at the first, since every later probe queues behind it.
func (rp replicaPair) lagUnderLoad(w writer, c *corpus, probes, load int) (lag []float64, late int, err error) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(time.Second / time.Duration(max(load, 1)))
		defer tick.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			doc := c.document(i)
			doc["key"] = fmt.Sprintf("load-%08d", i)
			_ = w.addOne(doc)
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	for i := 0; i < probes; i++ {
		key := fmt.Sprintf("probe-%08d", i)
		doc := c.document(i)
		doc["key"] = key
		if err := w.addOne(doc); err != nil {
			return nil, 0, err
		}
		acked := time.Now()
		for !present(rp.follower.client, rp.follower.url(), rp.collection, key) {
			if time.Since(acked) > lateProbe {
				return lag, probes - i, nil
			}
			time.Sleep(time.Millisecond)
		}
		lag = append(lag, float64(time.Since(acked).Microseconds())/1000)
		time.Sleep(pollPeriod)
	}
	return lag, 0, nil
}
