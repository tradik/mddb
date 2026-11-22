package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

func TestHookBlock_InstallAndUninstallKeepTheUsersHook(t *testing.T) {
	block := hookBlock("/bin/mddb cli", "/repo/it's", "/repo/.git/log", []string{"--prune", "-c", "x"})
	for _, want := range []string{`'/bin/mddb cli' autoindex hook run '/repo/it'\''s' '--prune' '-c' 'x' >>'/repo/.git/log' 2>&1 </dev/null &`, hookBegin, hookEnd} {
		if !strings.Contains(block, want) {
			t.Fatalf("block misses %q:\n%s", want, block)
		}
	}
	for _, own := range []string{"", "#!/bin/bash\necho mine\n", "#!/bin/sh\nexit 0"} {
		in := withBlock(own, block)
		if again := withBlock(in, block); again != in {
			t.Fatalf("a second install changed the hook:\n%s", again)
		}
		want := own
		if own == "" {
			want = "#!/bin/sh\n"
		}
		if out := withoutBlock(in); out != want {
			t.Fatalf("uninstall left %q, want %q", out, want)
		}
	}
	if s := "x\n" + hookBegin + "\nno end"; withoutBlock(s) != s {
		t.Fatal("a block without its end is left alone")
	}
}

func TestHook_InstallStatusUninstall(t *testing.T) {
	dir, git := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	hooks := filepath.Join(dir, ".git", "hooks")
	writeTree(t, hooks, map[string][]byte{"post-commit": []byte("#!/bin/sh\necho mine\n")})
	fs := newFakeServer(t, autoindexRoutes())

	out, err := runCLI(t, fs.URL, "--server", fs.URL, "autoindex", "hook", "install", dir, "--", "--prune", "-c", "proj")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "✓") != len(defaultHookEvents) {
		t.Fatalf("install: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(hooks, "post-commit"))
	if !strings.HasPrefix(string(data), "#!/bin/sh\necho mine\n") || !strings.Contains(string(data), "'--server' '"+fs.URL+"' '--prune' '-c' 'proj'") {
		t.Fatalf("post-commit:\n%s", data)
	}
	if st, _ := os.Stat(filepath.Join(hooks, "post-merge")); st.Mode()&0o100 == 0 {
		t.Fatal("hook not executable")
	}

	out, err = runCLI(t, fs.URL, "autoindex", "hook", "status", dir)
	if err != nil || !strings.Contains(out, "post-rewrite   installed") || !strings.Contains(out, "none (the next run") {
		t.Fatalf("status: %v %s", err, out)
	}

	// core.hooksPath moves the hooks; install follows git.
	git("config", "core.hooksPath", "githooks")
	if _, err := runCLI(t, fs.URL, "autoindex", "hook", "install", "--events", "post-merge", dir); err != nil {
		t.Fatal(err)
	}
	if !hookInstalled(filepath.Join(dir, "githooks"), "post-merge") {
		t.Fatal("core.hooksPath ignored")
	}
	if _, err := runCLI(t, fs.URL, "autoindex", "hook", "uninstall", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "githooks", "post-merge")); !os.IsNotExist(err) {
		t.Fatal("a hook only install wrote is removed")
	}
	git("config", "--unset", "core.hooksPath")
	out, err = runCLI(t, fs.URL, "autoindex", "hook", "uninstall", dir)
	if err != nil || strings.Count(out, "removed") != len(defaultHookEvents) {
		t.Fatalf("uninstall: %v %s", err, out)
	}
	if data, _ := os.ReadFile(filepath.Join(hooks, "post-commit")); string(data) != "#!/bin/sh\necho mine\n" {
		t.Fatalf("the user's hook changed: %q", data)
	}
	if out, _ := runCLI(t, fs.URL, "autoindex", "hook", "uninstall", dir); !strings.Contains(out, "No hook holds") {
		t.Fatalf("second uninstall: %s", out)
	}
}

func TestHook_InstallRefusesBadInput(t *testing.T) {
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	fs := newFakeServer(t, autoindexRoutes())
	for _, args := range [][]string{
		{dir, "--", "--watch"},
		{dir, "--", "--no-such-flag"},
		{dir, "--", "other/path"},
		{dir, "second"},
		{"--events", "pre-commit", dir},
		{"--events", "post-x/../../evil", dir},
		{t.TempDir()},
	} {
		if _, err := runCLI(t, fs.URL, append([]string{"autoindex", "hook", "install"}, args...)...); err == nil {
			t.Errorf("install %v: no error", args)
		}
	}
	for _, sub := range []string{"uninstall", "status", "run"} {
		if _, err := runCLI(t, fs.URL, "autoindex", "hook", sub, t.TempDir()); err == nil {
			t.Errorf("%s outside git: no error", sub)
		}
	}
	if _, err := runCLI(t, fs.URL, "autoindex", "hook", "run", "--watch", dir); err == nil {
		t.Error("run --watch: no error")
	}
}

func TestHookRun_SendsWhatChangedSinceTheLastRun(t *testing.T) {
	dir, git := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n"), "b.go": []byte("package b\n")})
	s := newStoreServer(t)
	ctx := context.Background()
	p, err := repoHookPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	o := indexOpts(dir)
	run := func() {
		t.Helper()
		if err := hookRun(ctx, mddb.New(s.URL), o); err != nil {
			t.Fatal(err)
		}
	}
	run() // no state: every file
	if len(s.snapshot()) != 2 {
		t.Fatalf("first run: %v", s.snapshot())
	}
	head := git("rev-parse", "HEAD")
	if data, _ := os.ReadFile(p.state); strings.TrimSpace(string(data)) != head {
		t.Fatalf("state %q, want %s", data, head)
	}

	git("rm", "-q", "b.go")
	writeTree(t, dir, map[string][]byte{"c.go": []byte("package c\n")})
	git("add", "c.go")
	git("commit", "-q", "-m", "two")
	run()
	if got := s.snapshot(); len(got) != 2 || got["c.go"] == "" || got["b.go"] != "" {
		t.Fatalf("second run: %v", got)
	}

	// The server down: the state stays, so the next run sends it again.
	writeTree(t, dir, map[string][]byte{"d.go": []byte("package d\n")})
	git("add", "d.go")
	git("commit", "-q", "-m", "three")
	s.setDown(true)
	run()
	if data, _ := os.ReadFile(p.state); strings.TrimSpace(string(data)) == git("rev-parse", "HEAD") {
		t.Fatal("state moved past a failed run")
	}
	s.setDown(false)

	// A state commit that is gone (rebased away) means every file.
	if err := os.WriteFile(p.state, []byte(strings.Repeat("0", 40)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	if got := s.snapshot(); got["d.go"] == "" {
		t.Fatalf("after a lost state: %v", got)
	}
}

func TestHookRun_LockAndDirty(t *testing.T) {
	dir, _ := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	p, err := repoHookPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := newStoreServer(t)
	// A run holds the lock: the next hook only marks the tree dirty.
	if err := os.WriteFile(p.lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hookRun(context.Background(), mddb.New(s.URL), indexOpts(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.dirty); err != nil || len(s.snapshot()) != 0 {
		t.Fatalf("dirty not marked or sent anyway: %v %v", err, s.snapshot())
	}
	// A lock older than staleLock belongs to a dead run and is taken over;
	// the dirty mark set meanwhile costs one more pass, then both go.
	old := time.Now().Add(-2 * staleLock)
	if err := os.Chtimes(p.lock, old, old); err != nil {
		t.Fatal(err)
	}
	if err := hookRun(context.Background(), mddb.New(s.URL), indexOpts(dir)); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p.lock, p.dirty} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", f)
		}
	}
	if len(s.snapshot()) != 1 {
		t.Fatalf("taken-over run sent: %v", s.snapshot())
	}
	if ok, err := takeLock(filepath.Join(dir, "no-dir", "lock")); ok || err == nil {
		t.Fatalf("lock in a missing directory: %v %v", ok, err)
	}
}

// TestHook_RealCommitDoesNotWait commits through an installed hook: git
// returns while the run is still going, and the run lands.
func TestHook_RealCommitDoesNotWait(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir, git := gitRepo(t, map[string][]byte{"a.go": []byte("package a\n")})
	exe := filepath.Join(t.TempDir(), "slow")
	marker := filepath.Join(dir, "ran")
	writeTree(t, filepath.Dir(exe), map[string][]byte{"slow": []byte("#!/bin/sh\nsleep 1\ntouch " + shellQuote(marker) + "\nexit 3\n")})
	if err := os.Chmod(exe, 0o755); err != nil { // #nosec G302 -- a test's fake binary
		t.Fatal(err)
	}
	if err := hookInstall(dir, exe, []string{"post-commit"}, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	cmd := exec.Command("git", "-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit failed because of the hook: %v %s", err, out)
	}
	if took := time.Since(start); took > 800*time.Millisecond {
		t.Fatalf("commit waited %s for the hook", took)
	}
	waitForLong(t, func() bool { _, err := os.Stat(marker); return err == nil })
	_ = git
}

// waitForLong is waitFor with room for a background process.
func waitForLong(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("the hook's run never happened")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
