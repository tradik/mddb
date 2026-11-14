package main

import (
	"path"
	"strings"
)

// matchSyncGlobs reports whether a repository-relative path, or its base
// name, matches one of the globs — the rule of `mddb-cli autoindex
// --include/--exclude` (its matchAny), so a git sync (#421) and autoindex
// select the same files. "dir/**" matches everything under dir. The two
// implementations run the same cases, testdata/globs/cases.json.
func matchSyncGlobs(globs []string, rel string) bool {
	for _, g := range globs {
		if ok, _ := path.Match(g, rel); ok {
			return true
		}
		if ok, _ := path.Match(g, path.Base(rel)); ok {
			return true
		}
		if strings.HasSuffix(g, "/**") && strings.HasPrefix(rel, strings.TrimSuffix(g, "**")) {
			return true
		}
	}
	return false
}
