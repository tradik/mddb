package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// The failure paths of AGT-006: git calls that fail, files that cannot be
// written, a working directory that is gone.

// failGit makes every git call whose arguments contain word fail while the
// test runs.
func failGit(t *testing.T, word string) {
	t.Helper()
	orig := runGit
	runGit = func(dir string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), word) {
			return nil, errors.New("git failed: " + word)
		}
		return orig(dir, args...)
	}
	t.Cleanup(func() { runGit = orig })
}

// goneCwd moves into a directory and removes it, so filepath.Abs fails.
func goneCwd(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not remove the working directory")
	}
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
}

func TestEdges_GitCallsThatFail(t *testing.T) {
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	s := newStoreServer(t)
	ctx := context.Background()

	failGit(t, "--others --exclude-standard -z")
	if _, err := gitChanged(dir, "HEAD"); err == nil || !strings.Contains(err.Error(), "git ls-files") {
		t.Fatalf("ls-files failing: %v", err)
	}
	o := indexOpts(dir)
	o.interval = time.Second
	if err := runAutoindexWatch(ctx, mddb.New(s.URL), o); err == nil {
		t.Fatal("watch with a failing listing: no error")
	}
	// hook run: the pass fails, and so does the run.
	if err := hookRun(ctx, mddb.New(s.URL), indexOpts(dir)); err == nil {
		t.Fatal("hook run with a failing listing: no error")
	}
}

func TestEdges_WatchKeepsGoingThroughErrors(t *testing.T) {
	// A listing that starts failing mid-watch is said once and retried.
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	s := newStoreServer(t)
	o := indexOpts(dir)
	o.interval = 5 * time.Millisecond
	startWatch(t, s, o)
	waitFor(t, "first run", func() bool { return len(s.snapshot()) == 1 })
	// Without its .git the directory's listing fails (swapping runGit here
	// would race with the watch).
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)

	// A root that disappears: the pass fails and the watch carries on.
	root := filepath.Join(t.TempDir(), "root")
	writeTree(t, root, map[string][]byte{"b.go": []byte("package b\n")})
	s2 := newStoreServer(t)
	o2 := indexOpts(root)
	o2.interval = 5 * time.Millisecond
	startWatch(t, s2, o2)
	waitFor(t, "first run", func() bool { return len(s2.snapshot()) == 1 })
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
}

func TestEdges_NoWorkingDirectory(t *testing.T) {
	s := newStoreServer(t)
	goneCwd(t)
	o := indexOpts(".")
	o.interval = time.Second
	if err := runAutoindexWatch(context.Background(), mddb.New(s.URL), o); err == nil {
		t.Fatal("watch without a working directory: no error")
	}
	if err := hookInstall(".", "/x", defaultHookEvents, nil); err == nil {
		t.Fatal("install without a working directory: no error")
	}
}

func TestEdges_HookFilesThatCannotBeWritten(t *testing.T) {
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	p, err := repoHookPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A hook that is a directory cannot be read: install, uninstall and
	// hookUninstall say so.
	if err := os.MkdirAll(filepath.Join(p.hooks, "post-commit"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := installHook(p.hooks, "post-commit", "x"); err == nil {
		t.Fatal("install into a directory: no error")
	}
	if _, err := uninstallHook(p.hooks, "post-commit"); err == nil {
		t.Fatal("uninstall from a directory: no error")
	}
	if err := hookUninstall(dir); err == nil {
		t.Fatal("hookUninstall over a directory: no error")
	}
	if had, err := uninstallHook(p.hooks, "post-none"); had || err != nil {
		t.Fatalf("missing hook: %v %v", had, err)
	}
	writeTree(t, p.hooks, map[string][]byte{"post-plain": []byte("#!/bin/sh\n")})
	if had, err := uninstallHook(p.hooks, "post-plain"); had || err != nil {
		t.Fatalf("hook without a block: %v %v", had, err)
	}
	if err := os.RemoveAll(p.hooks); err != nil {
		t.Fatal(err)
	}
	// Hooks that are a file: nothing can be made under them.
	if err := os.WriteFile(p.hooks, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hookInstall(dir, "/x", []string{"post-commit"}, nil); err == nil {
		t.Fatal("install under a file: no error")
	}
	// Windows reports a file read as a directory as not found.
	if err := hookUninstall(dir); err == nil && runtime.GOOS != "windows" {
		t.Fatal("uninstall under a file: no error")
	}
	if err := os.Remove(p.hooks); err != nil {
		t.Fatal(err)
	}
	// A read-only hooks directory: the new hook cannot be written.
	if err := os.Mkdir(p.hooks, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.hooks, 0o750) }) // #nosec G302 -- restore for cleanup
	if err := installHook(p.hooks, "post-commit", "x"); err == nil && canDenyWrites() {
		t.Fatal("install into a read-only directory: no error")
	}
	if err := hookUninstall(dir); err != nil {
		t.Fatalf("uninstall with no hooks: %v", err)
	}
}

func TestEdges_HookRunFiles(t *testing.T) {
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	p, err := repoHookPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := newStoreServer(t)
	ctx := context.Background()
	// A state file that is a directory cannot be written: the run says so
	// in its report and goes on.
	if err := os.Mkdir(p.state, 0o750); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := hookRun(ctx, mddb.New(s.URL), indexOpts(dir)); err != nil {
			t.Error(err)
		}
	})
	if !strings.Contains(out, "! state:") {
		t.Fatalf("state error not reported: %s", out)
	}
	if err := os.Remove(p.state); err != nil {
		t.Fatal(err)
	}
	// status with a state and a lock, from the working directory.
	writeTree(t, filepath.Dir(p.state), map[string][]byte{filepath.Base(p.state): []byte("abc\n"), filepath.Base(p.lock): nil})
	t.Chdir(dir)
	fs := newFakeServer(t, autoindexRoutes())
	got, err := runCLI(t, fs.URL, "autoindex", "hook", "status")
	if err != nil || !strings.Contains(got, "last indexed   abc") || !strings.Contains(got, "a run is going") {
		t.Fatalf("status: %v %s", err, got)
	}
	// A lock that cannot be created is an error.
	if err := os.Remove(p.lock); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Dir(p.lock)
	if err := os.Chmod(gitDir, 0o500); err != nil { // #nosec G302 -- a test's read-only git directory
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(gitDir, 0o750) }() // #nosec G302 -- restore for cleanup
	if err := hookRun(ctx, mddb.New(s.URL), indexOpts(dir)); err == nil && canDenyWrites() {
		t.Fatal("lock in a read-only directory: no error")
	}
}

func TestEdges_InstallWithoutExecutable(t *testing.T) {
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	orig := executable
	executable = func() (string, error) { return "", errors.New("no exe") }
	t.Cleanup(func() { executable = orig })
	fs := newFakeServer(t, autoindexRoutes())
	if _, err := runCLI(t, fs.URL, "autoindex", "hook", "install", dir); err == nil {
		t.Fatal("no executable: no error")
	}
}

// canDenyWrites reports whether a read-only directory stops this process:
// not for root, and not on Windows, which ignores a directory's mode.
func canDenyWrites() bool {
	return runtime.GOOS != "windows" && os.Geteuid() != 0
}
