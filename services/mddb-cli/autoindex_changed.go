package main

import (
	"bytes"
	"errors"
	"maps"
	"path"
	"slices"
	"strings"
)

// Narrowed autoindex runs (AGT-006): --changed-since, --watch and the git
// hooks send only the files that changed, and with --prune delete only the
// documents of the ones that are gone. The documents a narrowed run leaves
// are the ones a full --prune run would leave.

// pruneLookupLimit bounds the documents looked up for one gone path: a
// narrowed run does not know how many a mail file left, so it asks.
const pruneLookupLimit = 1000

// changedScope is the set of paths a run is narrowed to: o.only when the
// caller gave one, else what git reports changed since o.changedSince, else
// nil (every file). --semantic needs the whole tree — a change in one file
// moves references in any other — so it keeps a run whole.
func changedScope(root string, rep *autoindexReport, o autoindexOptions) (map[string]bool, error) {
	if o.semantic.langs != "" {
		return nil, nil
	}
	if o.only != nil || o.changedSince == "" {
		return o.only, nil
	}
	if rep.Git == nil {
		return nil, errors.New("--changed-since needs a git repository")
	}
	if strings.HasPrefix(o.changedSince, "-") {
		return nil, errors.New("--changed-since takes a commit, not an option")
	}
	return gitChanged(root, o.changedSince)
}

// gitChanged lists the paths, relative to root, that differ between ref and
// the working tree — added, modified, deleted, both sides of a rename — and
// the untracked files git does not ignore.
func gitChanged(root, ref string) (map[string]bool, error) {
	diff, err := runGit(root, "diff", "--relative", "--name-only", "--no-renames", "-z", ref, "--")
	if err != nil {
		return nil, errors.New("git diff " + ref + ": " + gitError(err))
	}
	untracked, err := runGit(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, errors.New("git ls-files: " + gitError(err))
	}
	set := map[string]bool{}
	for _, p := range bytes.Split(append(append(diff, 0), untracked...), []byte{0}) {
		if len(p) > 0 {
			set[path.Clean(string(p))] = true
		}
	}
	return set, nil
}

// narrowTo keeps the files in o.only and the skips among them, and with
// --prune marks the paths in o.only that are no longer files to index —
// deleted, renamed away, newly ignored, excluded or too large — as gone.
func narrowTo(rep *autoindexReport, files []autoFile, o autoindexOptions) []autoFile {
	var kept []autoFile
	present := map[string]bool{}
	rep.Files, rep.Bytes = 0, 0
	for _, f := range files {
		if o.only[f.Rel] {
			kept = append(kept, f)
			present[f.Rel] = true
			rep.Files++
			rep.Bytes += f.Size
		}
	}
	var skipped []autoSkip
	for _, s := range rep.Skipped {
		if o.only[s.Rel] {
			skipped = append(skipped, s)
		}
	}
	rep.Skipped = skipped
	if o.prune {
		rep.pruneCounts = map[string]int{}
		for _, p := range slices.Sorted(maps.Keys(o.only)) {
			if !present[p] {
				rep.ToPrune = append(rep.ToPrune, p)
				rep.pruneCounts[p] = pruneLookupLimit
			}
		}
	}
	return kept
}
