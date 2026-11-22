package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// storeServer is a server that keeps what autoindex sends: key → content.
// It answers the calls a run makes, so two runs can be compared by what
// they leave.
type storeServer struct {
	*httptest.Server
	mu   sync.Mutex
	docs map[string]string
	down bool
}

func newStoreServer(t *testing.T) *storeServer {
	t.Helper()
	s := &storeServer{docs: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *storeServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down && r.URL.Path != "/v1/code-languages" {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"down"}`))
		return
	}
	var body map[string]interface{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/code-languages":
		_, _ = w.Write([]byte(`{"extensions":{".go":"go"},"languages":["go"]}`))
	case "/v1/ingest":
		added := 0
		for _, d := range body["documents"].([]interface{}) {
			doc := asMap(d)
			s.docs[asString(doc["key"])] = asString(doc["contentMd"])
			added++
		}
		_, _ = fmt.Fprintf(w, `{"added":%d}`, added)
	case "/v1/aggregate":
		var buckets []map[string]interface{}
		for k := range s.docs {
			buckets = append(buckets, map[string]interface{}{"value": k, "count": 1})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"facets": map[string]interface{}{"path": buckets}})
	case "/v1/delete-batch":
		n := 0
		for _, d := range body["documents"].([]interface{}) {
			if _, ok := s.docs[asString(asMap(d)["key"])]; ok {
				delete(s.docs, asString(asMap(d)["key"]))
				n++
			}
		}
		_, _ = fmt.Fprintf(w, `{"deleted":%d}`, n)
	case "/v1/search":
		_, _ = w.Write([]byte(`[]`))
	case "/v1/collection-config":
		_, _ = w.Write([]byte(`{"configured":false}`))
	default:
		_, _ = w.Write([]byte(`{}`))
	}
}

func (s *storeServer) snapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.docs))
	for k, v := range s.docs {
		out[k] = v
	}
	return out
}

func (s *storeServer) setDown(down bool) {
	s.mu.Lock()
	s.down = down
	s.mu.Unlock()
}

// gitRepo makes a repository with one commit of files and returns a git
// runner for it.
func gitRepo(t *testing.T, files map[string][]byte) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	writeTree(t, dir, files)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	return dir, git
}

func indexOpts(root string) autoindexOptions {
	return autoindexOptions{root: root, collection: "c", lang: "en", maxSize: 1 << 20, prune: true}
}

// TestChangedSince_SameAsAFullRun is the property AGT-006 rests on: random
// edits, then a narrowed run, leave the documents a full --prune run leaves.
func TestChangedSince_SameAsAFullRun(t *testing.T) {
	rng := rand.New(rand.NewSource(6)) // #nosec G404 -- a test's reproducible edits
	for round := 0; round < 8; round++ {
		files := map[string][]byte{}
		for i := 0; i < 6; i++ {
			// Distinct sizes: git sees a change by size, mtime and (not on
			// Windows) inode, so a rename over a file of the same size and
			// mtime would pass as unchanged there.
			files[fmt.Sprintf("d%d/f%d.go", i%2, i)] = []byte(fmt.Sprintf("package d\n// %d %s\n", i, strings.Repeat("x", i)))
		}
		dir, git := gitRepo(t, files)
		base := git("rev-parse", "HEAD")
		narrow, full := newStoreServer(t), newStoreServer(t)
		ctx := context.Background()
		if _, err := autoindexPass(ctx, mddb.New(narrow.URL), indexOpts(dir)); err != nil {
			t.Fatal(err)
		}
		for e := 0; e < 5; e++ {
			name := fmt.Sprintf("d%d/f%d.go", rng.Intn(3), rng.Intn(9))
			p := filepath.Join(dir, filepath.FromSlash(name))
			switch rng.Intn(4) {
			case 0:
				_ = os.Remove(p)
			case 1:
				_ = os.MkdirAll(filepath.Dir(p), 0o750)
				_ = os.Rename(filepath.Join(dir, "d0", "f0.go"), p)
			default:
				writeTree(t, dir, map[string][]byte{name: []byte(fmt.Sprintf("package d\n// edit %d %d\n", round, e))})
			}
		}
		if rng.Intn(2) == 0 {
			git("add", "-A")
			git("commit", "-q", "-m", "edits", "--allow-empty")
		}
		o := indexOpts(dir)
		o.changedSince = base
		if _, err := autoindexPass(ctx, mddb.New(narrow.URL), o); err != nil {
			t.Fatal(err)
		}
		if _, err := autoindexPass(ctx, mddb.New(full.URL), indexOpts(dir)); err != nil {
			t.Fatal(err)
		}
		if got, want := narrow.snapshot(), full.snapshot(); !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: narrowed run left %v, full run %v", round, got, want)
		}
	}
}

func TestChangedSince_Errors(t *testing.T) {
	ctx := context.Background()
	s := newStoreServer(t)
	o := indexOpts(t.TempDir())
	o.changedSince = "HEAD"
	if _, err := autoindexPass(ctx, mddb.New(s.URL), o); err == nil || !strings.Contains(err.Error(), "needs a git repository") {
		t.Fatalf("outside git: %v", err)
	}
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	o = indexOpts(dir)
	o.changedSince = "--output=/tmp/x"
	if _, err := autoindexPass(ctx, mddb.New(s.URL), o); err == nil || !strings.Contains(err.Error(), "not an option") {
		t.Fatalf("option as ref: %v", err)
	}
	o.changedSince = "no-such-ref"
	if _, err := autoindexPass(ctx, mddb.New(s.URL), o); err == nil || !strings.Contains(err.Error(), "git diff no-such-ref") {
		t.Fatalf("bad ref: %v", err)
	}
	// --semantic keeps the run whole: the scope is every file.
	o.semantic.langs = "go"
	if only, err := changedScope(dir, &autoindexReport{Git: &gitInfo{}}, o); err != nil || only != nil {
		t.Fatalf("semantic: %v %v", only, err)
	}
}

func TestChangedSince_CLIAndNarrowedReport(t *testing.T) {
	dir, git := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n"), "b.go": []byte("package b\n"), "big.go": []byte("package big\n")})
	writeTree(t, dir, map[string][]byte{"a.go": []byte("package a // changed\n"), "big.go": []byte(strings.Repeat("x", 64)), "new.go": []byte("package n\n")})
	git("rm", "-q", "b.go")
	fs := newFakeServer(t, autoindexRoutes())
	out, err := runCLI(t, fs.URL, "--json", "autoindex", "--dry-run", "--prune", "--max-file-size", "32", "--changed-since", "HEAD", dir)
	if err != nil {
		t.Fatal(err)
	}
	var rep autoindexReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	var plan []string
	for _, f := range rep.Plan {
		plan = append(plan, f.Rel)
	}
	if !reflect.DeepEqual(plan, []string{"a.go", "new.go"}) || rep.Files != 2 {
		t.Fatalf("plan: %v %d", plan, rep.Files)
	}
	if !reflect.DeepEqual(rep.ToPrune, []string{"b.go", "big.go"}) || len(rep.Skipped) != 1 || rep.Skipped[0].Rel != "big.go" {
		t.Fatalf("prune %v skipped %v", rep.ToPrune, rep.Skipped)
	}
	if len(callsTo(fs, "POST", "/v1/aggregate")) != 0 {
		t.Fatal("a narrowed run knows what is gone: no aggregate")
	}
	// A real narrowed run neither sets include paths nor asks the advisor.
	fs = newFakeServer(t, autoindexRoutes())
	if _, err := runCLI(t, fs.URL, "autoindex", "--changed-since", "HEAD", dir); err != nil {
		t.Fatal(err)
	}
	if len(callsTo(fs, "POST", "/v1/search-advisor"))+len(callsTo(fs, "GET", "/v1/collection-config")) != 0 {
		t.Fatalf("narrowed run touched the collection's settings: %+v", fs.calls())
	}
}

func TestGitError(t *testing.T) {
	if got := gitError(fmt.Errorf("plain")); got != "plain" {
		t.Fatal(got)
	}
	if got := gitError(&exec.ExitError{Stderr: []byte(" fatal: x \n")}); got != "fatal: x" {
		t.Fatal(got)
	}
}
