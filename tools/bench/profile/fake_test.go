package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// A stand-in for mddbd, so the orchestration — start, health, stop, kill -9,
// restart, a leader and a follower — runs in the unit tests without a server
// build. The test binary is the stand-in: TestMain turns into it when
// fakeServerEnv is set, which the tests set before they name os.Args[0] as
// the binary to measure.
//
// It keeps documents per collection in memory and appends every write to a
// file under MDDB_PATH before acknowledging it, so a kill -9 loses nothing
// and a restart reads everything back. A follower (MDDB_REPLICATION_ROLE=
// follower) has no documents of its own: it answers reads by asking the
// leader, whose MDDB_GRPC_ADDR the fake also serves HTTP on.

const fakeServerEnv = "BENCH_PROFILE_FAKE_MDDBD"

func TestMain(m *testing.M) {
	if os.Getenv(fakeServerEnv) == "" {
		os.Exit(m.Run())
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("mddbd version 0.0.0-fake")
		return
	}
	if err := runFake(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// fakeServer is the stand-in's state.
type fakeServer struct {
	mu       sync.Mutex
	docs     map[string]map[string]string // collection → key → text
	log      *os.File
	leader   string // a follower's leader, as a base URL
	followUp bool   // a follower: reads go to the leader
}

func runFake() error {
	path := os.Getenv("MDDB_PATH")
	f := &fakeServer{docs: map[string]map[string]string{}}
	if os.Getenv("MDDB_REPLICATION_ROLE") == "follower" {
		f.followUp, f.leader = true, "http://"+os.Getenv("MDDB_REPLICATION_LEADER_ADDR")
	}
	if err := f.replay(path); err != nil {
		return err
	}
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- the test's own scratch file
	if err != nil {
		return err
	}
	f.log = logFile
	errs := make(chan error, 2)
	for _, addr := range []string{os.Getenv("MDDB_HTTP_ADDR"), os.Getenv("MDDB_GRPC_ADDR")} {
		go func(addr string) { errs <- http.ListenAndServe(addr, f) }(addr) // #nosec G114 -- a loopback test stand-in
	}
	return <-errs
}

// replay reads the write log of an earlier run.
func (f *fakeServer) replay(path string) error {
	in, err := os.Open(path) // #nosec G304 -- the test's own scratch file
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var d fakeDoc
		if json.Unmarshal(sc.Bytes(), &d) == nil {
			f.put(d)
		}
	}
	return nil
}

type fakeDoc struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
	ContentMd  string `json:"contentMd"`
}

func (f *fakeServer) put(d fakeDoc) {
	if f.docs[d.Collection] == nil {
		f.docs[d.Collection] = map[string]string{}
	}
	f.docs[d.Collection][d.Key] = d.ContentMd
}

// write logs and stores documents, then acknowledges.
func (f *fakeServer) write(docs ...fakeDoc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range docs {
		raw, _ := json.Marshal(d)
		_, _ = f.log.Write(append(raw, '\n'))
		f.put(d)
	}
}

// checksum is a digest of a collection's keys and texts.
func (f *fakeServer) checksum(collection string) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	docs := f.docs[collection]
	keys := make([]string, 0, len(docs))
	for k := range docs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = io.WriteString(h, k+"\x00"+docs[k]+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil)), len(keys)
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.followUp && (r.URL.Path == "/v1/get" || r.URL.Path == "/v1/checksum") {
		f.proxy(w, r)
		return
	}
	var body struct {
		fakeDoc
		Documents []fakeDoc `json:"documents"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch r.URL.Path {
	case "/health":
		writeJSON(w, map[string]any{"status": "healthy"})
	case "/v1/add":
		f.write(body.fakeDoc)
		writeJSON(w, map[string]any{"key": body.Key})
	case "/v1/add-batch":
		for i := range body.Documents {
			body.Documents[i].Collection = body.Collection
		}
		f.write(body.Documents...)
		writeJSON(w, map[string]any{"added": len(body.Documents)})
	case "/v1/get":
		f.get(w, body.Collection, body.Key)
	case "/v1/checksum":
		sum, n := f.checksum(r.URL.Query().Get("collection"))
		writeJSON(w, map[string]any{"checksum": sum, "documentCount": n})
	case "/v1/indexing-status":
		writeJSON(w, map[string]any{"pendingEmbeddings": 0})
	case "/v1/vector-search", "/v1/fts", "/v1/hybrid-search":
		f.search(w, body.Collection)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeServer) get(w http.ResponseWriter, collection, key string) {
	f.mu.Lock()
	_, ok := f.docs[collection][key]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, map[string]any{"key": key})
}

// search answers with the collection's first ten keys in key order.
func (f *fakeServer) search(w http.ResponseWriter, collection string) {
	f.mu.Lock()
	keys := make([]string, 0, len(f.docs[collection]))
	for k := range f.docs[collection] {
		keys = append(keys, k)
	}
	f.mu.Unlock()
	sort.Strings(keys)
	type result struct {
		Document struct {
			Key string `json:"key"`
		} `json:"document"`
	}
	var out []result
	for _, k := range keys[:min(10, len(keys))] {
		var res result
		res.Document.Key = k
		out = append(out, res)
	}
	writeJSON(w, map[string]any{"results": out, "algorithm": "fake"})
}

// proxy sends a follower's read to the leader.
func (f *fakeServer) proxy(w http.ResponseWriter, r *http.Request) {
	req, _ := http.NewRequest(r.Method, f.leader+r.URL.RequestURI(), r.Body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// useFake makes os.Args[0] a server the harness can measure and returns its
// path.
func useFake(t *testing.T) string {
	t.Helper()
	t.Setenv(fakeServerEnv, "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// containsAll reports whether s contains every one of parts.
func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
