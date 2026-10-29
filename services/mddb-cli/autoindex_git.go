package main

import (
	"bytes"
	"context"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

// Git awareness for autoindex (AGT-001). In a repository the file list comes
// from git, which already knows what is ignored, and every document records
// the commit and branch it was indexed at.

// gitTimeout bounds each git call: a hung credential prompt must not hang
// the indexer.
const gitTimeout = 30 * time.Second

// gitInfo describes the repository a directory is in.
type gitInfo struct {
	Commit string `json:"commit,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Name is the repository's name from the origin remote, if any.
	Name string `json:"name,omitempty"`
}

// runGit runs git in dir and returns its standard output.
func runGit(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	// #nosec G204 -- fixed git subcommands; dir is the directory the user asked to index
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	return cmd.Output()
}

// detectGit reports the repository dir is in, or nil when it is not in one
// or git is not installed.
func detectGit(dir string) *gitInfo {
	if _, err := runGit(dir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil
	}
	info := &gitInfo{}
	if out, err := runGit(dir, "rev-parse", "HEAD"); err == nil {
		info.Commit = strings.TrimSpace(string(out))
	}
	if out, err := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(string(out))
	}
	if out, err := runGit(dir, "remote", "get-url", "origin"); err == nil {
		info.Name = repoName(strings.TrimSpace(string(out)))
	}
	return info
}

// repoName is the last path element of a remote URL:
// git@github.com:tradik/mddb.git and https://github.com/tradik/mddb both
// give "mddb".
func repoName(remote string) string {
	remote = strings.TrimSuffix(strings.TrimRight(remote, "/"), ".git")
	if i := strings.LastIndexAny(remote, "/:"); i >= 0 {
		remote = remote[i+1:]
	}
	return remote
}

// gitFiles lists the files git would track under dir — committed, and new
// files that are not ignored — relative to dir.
func gitFiles(dir string) ([]string, error) {
	out, err := runGit(dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if s := path.Clean(string(p)); len(p) > 0 && !seen[s] {
			seen[s] = true
			files = append(files, s)
		}
	}
	sort.Strings(files)
	return files, nil
}
