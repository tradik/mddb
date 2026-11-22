package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// waitFor polls cond until it holds or two seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startWatch runs --watch in the background until the test ends.
func startWatch(t *testing.T, s *storeServer, o autoindexOptions) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runAutoindexWatch(ctx, mddb.New(s.URL), o) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("watch: %v", err)
		}
	})
}

func TestWatch_FollowsEditsDeletesAndOutages(t *testing.T) {
	for _, inGit := range []bool{false, true} {
		t.Run(map[bool]string{false: "dir", true: "git"}[inGit], func(t *testing.T) {
			files := map[string][]byte{"a.go": []byte("package a\n"), "b.go": []byte("package b\n")}
			var dir string
			if inGit {
				dir, _ = gitRepo(t, files)
			} else {
				dir = t.TempDir()
				writeTree(t, dir, files)
			}
			s := newStoreServer(t)
			o := indexOpts(dir)
			o.interval = 10 * time.Millisecond
			o.semantic.langs = "" // facts belong to the first pass only
			startWatch(t, s, o)
			waitFor(t, "first run", func() bool { return len(s.snapshot()) == 2 })

			// Save through a temporary file and a rename, as editors do.
			tmp := filepath.Join(dir, ".a.go.swp")
			if err := os.WriteFile(tmp, []byte("package a // saved\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, filepath.Join(dir, "a.go")); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "edit", func() bool { return strings.Contains(s.snapshot()["a.go"], "saved") })

			if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "delete", func() bool { _, ok := s.snapshot()["b.go"]; return !ok })

			// A file written while the server is down arrives once it is back.
			s.setDown(true)
			writeTree(t, dir, map[string][]byte{"c.go": []byte("package c\n")})
			time.Sleep(50 * time.Millisecond)
			s.setDown(false)
			waitFor(t, "retry", func() bool { _, ok := s.snapshot()["c.go"]; return ok })
			if got := s.snapshot(); !reflect.DeepEqual(len(got), 2) {
				t.Fatalf("documents: %v", got)
			}
		})
	}
}

func TestWatch_RefusesWhatItCannotDo(t *testing.T) {
	s := newStoreServer(t)
	ctx := context.Background()
	for _, o := range []autoindexOptions{
		{root: t.TempDir(), dryRun: true, interval: time.Second},
		{root: t.TempDir(), changedSince: "HEAD", interval: time.Second},
		{root: t.TempDir()},
	} {
		if err := runAutoindexWatch(ctx, mddb.New(s.URL), o); err == nil {
			t.Fatalf("%+v: no error", o)
		}
	}
	missing := indexOpts(filepath.Join(t.TempDir(), "nope"))
	missing.interval = time.Second
	if err := runAutoindexWatch(ctx, mddb.New(s.URL), missing); err == nil {
		t.Fatal("missing directory: no error")
	}
	// A first run that fails stops the watch.
	file := filepath.Join(t.TempDir(), "f")
	writeTree(t, filepath.Dir(file), map[string][]byte{"f": []byte("x")})
	notDir := indexOpts(file)
	notDir.interval = time.Second
	if err := runAutoindexWatch(ctx, mddb.New(s.URL), notDir); err == nil {
		t.Fatal("a file as root: no error")
	}
}

func TestWatch_CLIAndOutput(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{"a.go": []byte("package a\n")})
	fs := newFakeServer(t, autoindexRoutes())
	// The CLI path: a watch stopped by the signal context; here the root is
	// missing, so it returns at once.
	if _, err := runCLI(t, fs.URL, "autoindex", "--watch", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing root: no error")
	}
	rep := &autoindexReport{Added: 1, Errors: []string{"boom"}}
	out := captureStdout(t, func() {
		outputJSON = true
		defer func() { outputJSON = false }()
		if last := printWatchPass(rep, 3, time.Now(), ""); last != "boom" {
			t.Errorf("last: %q", last)
		}
	})
	if !strings.Contains(out, `"changed":3`) || !strings.Contains(out, `"errors":["boom"]`) {
		t.Fatalf("json: %s", out)
	}
	out = captureStdout(t, func() { printWatchPass(rep, 3, time.Now(), "boom") })
	if !strings.Contains(out, "3 changed — 1 added") {
		t.Fatalf("text: %s", out)
	}
	if got := changedFiles(map[string]fileStamp{"a": {size: 1}, "b": {size: 1}}, map[string]fileStamp{"a": {size: 2}, "c": {}}); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("changed: %v", got)
	}
	if _, err := lookAt(dir, true); err == nil {
		t.Fatal("lookAt with git outside a repository: no error")
	}
	// A retry that fails as the last one did prints nothing.
	if out := captureStdout(t, func() { printWatchPass(&autoindexReport{sendFailed: true, Errors: []string{"down"}}, 1, time.Now(), "down") }); out != "" {
		t.Fatalf("repeated failure printed: %q", out)
	}
}

// captureStdout returns what fn printed to standard output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	return <-done
}
