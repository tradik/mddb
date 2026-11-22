package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Git hooks for autoindex (AGT-006): after a commit, merge, checkout or
// rewrite, the hook starts `autoindex hook run` in the background, which
// sends what changed since the commit it last indexed. The commit never
// waits for it and never fails because of it.

const (
	hookBegin = "# >>> mddb autoindex >>>"
	hookEnd   = "# <<< mddb autoindex <<<"
	// staleLock is how old a lock may be before a run takes it over: the run
	// that held it died without removing it.
	staleLock = time.Hour
	// hookMode is a new hook's mode: read, write and execute for its owner,
	// who is the one git runs it as.
	hookMode = 0o700
)

// defaultHookEvents are the hooks install writes when not told otherwise:
// every way HEAD or the working tree moves after the fact.
var defaultHookEvents = []string{"post-commit", "post-merge", "post-checkout", "post-rewrite"}

// hookPaths are the files a repository's hooks keep, from git itself, so
// core.hooksPath and worktrees are honoured.
type hookPaths struct {
	hooks, state, lock, dirty, log string
}

// repoHookPaths asks git where root's hooks and autoindex's files live.
func repoHookPaths(root string) (hookPaths, error) {
	var p hookPaths
	for dst, name := range map[*string]string{&p.hooks: "hooks", &p.state: "mddb-autoindex.state",
		&p.lock: "mddb-autoindex.lock", &p.dirty: "mddb-autoindex.dirty", &p.log: "mddb-autoindex.log"} {
		out, err := runGit(root, "rev-parse", "--git-path", name)
		if err != nil {
			return p, errors.New(root + " is not in a git repository")
		}
		*dst = strings.TrimSpace(string(out))
		if !filepath.IsAbs(*dst) {
			*dst = filepath.Join(root, *dst)
		}
	}
	return p, nil
}

// shellQuote quotes s for sh: inside single quotes nothing is special but
// the single quote itself.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookBlock is what install adds to a hook: start the run in the
// background with its output in the log, and leave the hook's exit code to
// the rest of the file.
func hookBlock(exe, root, log string, args []string) string {
	words := []string{shellQuote(exe), "autoindex", "hook", "run", shellQuote(root)}
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return "\n" + hookBegin + "\n" +
		"(" + strings.Join(words, " ") + " >>" + shellQuote(log) + " 2>&1 </dev/null &)\n" +
		hookEnd + "\n"
}

// withBlock is a hook's content with block in place of any earlier one, or
// added at the end; a new hook starts with a shebang.
func withBlock(content, block string) string {
	if content == "" {
		content = "#!/bin/sh\n"
	}
	return withoutBlock(content) + block
}

// withoutBlock is a hook's content with the block install added taken out,
// byte for byte what it was before install.
func withoutBlock(content string) string {
	begin := strings.Index(content, "\n"+hookBegin+"\n")
	if begin < 0 {
		return content
	}
	end := strings.Index(content[begin:], hookEnd+"\n")
	if end < 0 {
		return content
	}
	return content[:begin] + content[begin+end+len(hookEnd)+1:]
}

// installHook writes block into the named hook, keeping what was there.
func installHook(dir, event, block string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	file := filepath.Join(dir, event)
	old, err := os.ReadFile(file) // #nosec G304 -- a hook of the repository the user named
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	mode := os.FileMode(hookMode)
	if st, err := os.Stat(file); err == nil {
		mode = st.Mode().Perm() | 0o100
	}
	// A new hook is the owner's alone; an existing one keeps its mode.
	// #nosec G306 G703 -- git runs a hook only when it is executable; the path is from git rev-parse
	if err := os.WriteFile(file, []byte(withBlock(string(old), block)), hookMode); err != nil {
		return err
	}
	// git runs a hook only when its owner may execute it.
	return os.Chmod(file, mode) // #nosec G302 -- the hook's own mode plus the owner's execute bit
}

// uninstallHook takes the block out of the named hook, and removes the hook
// when nothing but the shebang install wrote is left. It reports whether
// there was a block.
func uninstallHook(dir, event string) (bool, error) {
	file := filepath.Join(dir, event)
	old, err := os.ReadFile(file) // #nosec G304 -- a hook of the repository the user named
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rest := withoutBlock(string(old))
	if rest == string(old) {
		return false, nil
	}
	if rest == "#!/bin/sh\n" {
		return true, os.Remove(file)
	}
	return true, os.WriteFile(file, []byte(rest), hookMode) // #nosec G306 G703 -- an existing file keeps its mode; the path is from git rev-parse
}

// hookInstalled reports whether the named hook holds the block.
func hookInstalled(dir, event string) bool {
	data, err := os.ReadFile(filepath.Join(dir, event)) // #nosec G304 -- a hook of the repository the user named
	return err == nil && strings.Contains(string(data), "\n"+hookBegin+"\n")
}

// takeLock creates the lock file, taking over one older than staleLock. It
// reports false when another run holds it.
func takeLock(file string) (bool, error) {
	ok, err := createLock(file)
	if ok || err != nil {
		return ok, err
	}
	if st, err := os.Stat(file); err == nil && time.Since(st.ModTime()) >= staleLock {
		_ = os.Remove(file)
		return createLock(file)
	}
	return false, nil
}

// createLock creates file if it does not exist; false, nil when it does.
func createLock(file string) (bool, error) {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- under the repository's git directory
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, f.Close()
}
