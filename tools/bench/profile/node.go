package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A node is one mddbd process the harness owns: its data directory, its
// ports and its environment. The harness starts, stops and kills its own
// servers, because start-up time, peak memory and recovery cannot be measured
// on a server somebody else started.

// node is one server: started, stopped and started again on the same data
// and the same ports.
type node struct {
	bin      string
	dir      string
	env      []string // on top of the base environment
	httpAddr string
	grpcAddr string
	client   *http.Client
	cmd      *exec.Cmd
	exited   chan struct{} // closed when the process is gone
	started  time.Time
}

// stopGrace is how long a node gets to shut down before it is killed.
var stopGrace = 60 * time.Second

// newNode reserves two free loopback ports and a data directory under root.
func newNode(bin, root, name string, env ...string) (*node, error) {
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	n := &node{bin: bin, dir: dir, env: env, client: &http.Client{Timeout: 10 * time.Minute,
		Transport: &http.Transport{MaxIdleConnsPerHost: 64}}}
	var err error
	if n.httpAddr, err = freeAddr(); err == nil {
		n.grpcAddr, err = freeAddr()
	}
	return n, err
}

// freeAddr is a loopback address nothing listens on right now.
func freeAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String(), nil
}

func (n *node) url() string { return "http://" + n.httpAddr }

// environ is the process environment: the caller's without any MDDB_ setting
// (a developer's shell must not change what is measured), then the node's.
func (n *node) environ() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "MDDB_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"MDDB_PATH="+filepath.Join(n.dir, "mddb.db"),
		"MDDB_HTTP_ADDR="+n.httpAddr,
		"MDDB_GRPC_ADDR="+n.grpcAddr,
		"MDDB_MCP_ENABLED=false",
		"MDDB_UPDATE_CHECK=0",      // no network
		"MDDB_SEARCH_CACHE_SIZE=0", // every query is answered, not remembered
		"MDDB_EMBEDDING_AUTODETECT=false",
	)
	return append(env, n.env...)
}

// start launches the process; its output goes to <dir>/server.log.
func (n *node) start() error {
	logFile, err := os.OpenFile(filepath.Join(n.dir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 G703 -- inside the harness's own work directory
	if err != nil {
		return err
	}
	cmd := exec.Command(n.bin) // #nosec G204 G702 -- the server binary the operator named with -mddbd
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = n.environ(), n.dir, logFile, logFile
	n.started = time.Now()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	n.cmd, n.exited = cmd, make(chan struct{})
	running.Store(cmd.Process, struct{}{})
	go func(exited chan struct{}) {
		_ = cmd.Wait()
		running.Delete(cmd.Process)
		_ = logFile.Close()
		close(exited)
	}(n.exited)
	return nil
}

// running is every server process the harness started and that has not
// exited, so an interrupted run can stop them on its way out.
var running sync.Map

// killRunning kills every server still running.
func killRunning() {
	running.Range(func(p, _ any) bool {
		_ = p.(*os.Process).Kill()
		return true
	})
}

// health is the part of /health the harness reads.
type health struct {
	Status      string          `json:"status"`
	VectorIndex json.RawMessage `json:"vectorIndex"` // present while the vector index loads
}

// waitHealthy polls /health until the server answers healthy — and, with
// indexLoaded, until its vector index has finished loading — and returns the
// time since the process started.
func (n *node) waitHealthy(timeout time.Duration, indexLoaded bool) (time.Duration, error) {
	deadline := n.started.Add(timeout)
	for {
		if h, ok := n.health(); ok && h.Status == "healthy" && (!indexLoaded || h.VectorIndex == nil) {
			return time.Since(n.started), nil
		}
		select {
		case <-n.exited:
			return 0, fmt.Errorf("mddbd exited before it was healthy; see %s", filepath.Join(n.dir, "server.log"))
		case <-time.After(5 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("mddbd not healthy after %s; see %s", timeout, filepath.Join(n.dir, "server.log"))
		}
	}
}

func (n *node) health() (health, bool) {
	var h health
	resp, err := n.client.Get(n.url() + "/health") // #nosec G704 -- a loopback server the harness started
	if err != nil {
		return h, false
	}
	defer func() { _ = resp.Body.Close() }()
	return h, resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&h) == nil
}

// stop asks the process to shut down and waits for it; a process that does
// not take the signal, or outlives stopGrace, is killed.
func (n *node) stop() {
	if n.cmd == nil {
		return
	}
	if n.cmd.Process.Signal(os.Interrupt) != nil {
		n.kill()
		return
	}
	select {
	case <-n.exited:
		n.cmd = nil
	case <-time.After(stopGrace):
		n.kill()
	}
}

// kill is kill -9: no shutdown, no flush.
func (n *node) kill() {
	if n.cmd == nil {
		return
	}
	_ = n.cmd.Process.Kill()
	<-n.exited
	n.cmd = nil
}

// peakRSS is the running process's peak resident memory in MiB.
func (n *node) peakRSS() float64 {
	if n.cmd == nil {
		return 0
	}
	return peakRSSMB(n.cmd.Process.Pid)
}
