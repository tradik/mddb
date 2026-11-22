package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// autoindex --watch (AGT-006): index the directory, then keep the collection
// in step with it while someone works.
//
// It polls: every --interval it lists the files as a run would and compares
// each one's size and modification time with the last look. Polling needs no
// dependency and works where file events do not arrive — a WSL2 checkout
// under /mnt/c, NFS, a bind mount into a container — and it has no
// inotify watch limit to run out of. A tick costs one listing (git ls-files
// in a repository) and one stat per file.

// fileStamp is what a look records about a file.
type fileStamp struct {
	size int64
	mod  time.Time
}

// watchPass is one line of --watch output: --json prints it as one JSON
// object per line.
type watchPass struct {
	Time    string   `json:"time"`
	Changed int      `json:"changed"`
	Added   int      `json:"added"`
	Updated int      `json:"updated"`
	Pruned  int      `json:"pruned"`
	Failed  int      `json:"failed"`
	Ms      int64    `json:"durationMs"`
	Errors  []string `json:"errors,omitempty"`
}

// runAutoindexWatch indexes the tree, then every o.interval sends the files
// that changed since the last look until ctx is done. A pass that could not
// reach the server keeps its files for the next one. Semantic facts come with
// the first run only: language servers need the whole tree.
func runAutoindexWatch(ctx context.Context, client *mddb.Client, o autoindexOptions) error {
	if o.dryRun || o.changedSince != "" {
		return errors.New("--watch does not take --dry-run or --changed-since")
	}
	if o.interval <= 0 {
		return errors.New("--interval must be positive")
	}
	root, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	inGit := detectGit(root) != nil
	// The first look comes before the first run, so an edit made while it
	// runs is still seen.
	last, err := lookAt(root, inGit)
	if err != nil {
		return err
	}
	if err := runAutoindex(ctx, client, o); err != nil {
		return err
	}
	o.semantic.langs = ""
	fmt.Fprintf(os.Stderr, "watching %s every %s; Ctrl-C stops\n", root, o.interval)
	pending := map[string]bool{}
	lastErr := ""
	tick := time.NewTicker(o.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		now, err := lookAt(root, inGit)
		if err != nil {
			lastErr = reportOnce(lastErr, err.Error())
			continue
		}
		for _, p := range changedFiles(last, now) {
			pending[p] = true
		}
		last = now
		if len(pending) == 0 {
			continue
		}
		o.only = maps.Clone(pending)
		// The pass runs to its end even when a signal arrives meanwhile: a
		// batch is not left half sent.
		start := time.Now()
		rep, err := autoindexPass(context.WithoutCancel(ctx), client, o)
		if err != nil {
			lastErr = reportOnce(lastErr, err.Error())
			continue
		}
		if !rep.sendFailed {
			pending = map[string]bool{}
		}
		lastErr = printWatchPass(rep, len(o.only), start, lastErr)
	}
}

// lookAt records the size and modification time of every file a run would
// list. A file gone between listing and stat is left out: the next look
// sees it gone.
func lookAt(root string, inGit bool) (map[string]fileStamp, error) {
	list := walkFiles
	if inGit {
		list = gitFiles
	}
	paths, err := list(root)
	if err != nil {
		return nil, err
	}
	stamps := make(map[string]fileStamp, len(paths))
	for _, rel := range paths {
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil && !st.IsDir() {
			stamps[rel] = fileStamp{size: st.Size(), mod: st.ModTime()}
		}
	}
	return stamps, nil
}

// changedFiles lists, sorted, the paths that appeared, disappeared or
// changed size or modification time between two looks.
func changedFiles(before, after map[string]fileStamp) []string {
	set := map[string]bool{}
	for p, a := range after {
		if b, ok := before[p]; !ok || b.size != a.size || !b.mod.Equal(a.mod) {
			set[p] = true
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			set[p] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// printWatchPass prints a pass as one line, and its errors unless they are
// the ones printed last time: a server that is down is said once, not every
// tick, and a retry that fails as the last one did prints nothing. It
// returns the errors it has now seen.
func printWatchPass(rep *autoindexReport, changed int, start time.Time, lastErr string) string {
	errs := strings.Join(rep.Errors, "\n")
	if rep.sendFailed && errs == lastErr {
		return lastErr
	}
	p := watchPass{Time: start.Format("15:04:05"), Changed: changed, Added: rep.Added, Updated: rep.Updated,
		Pruned: rep.Pruned, Failed: rep.Failed, Ms: time.Since(start).Milliseconds(), Errors: rep.Errors}
	if outputJSON {
		// A struct of plain fields: marshalling it cannot fail.
		data, _ := json.Marshal(p)
		fmt.Println(string(data))
		return errs
	}
	fmt.Printf("%s %d changed — %d added, %d updated, %d pruned, %d failed in %dms\n",
		p.Time, p.Changed, p.Added, p.Updated, p.Pruned, p.Failed, p.Ms)
	return reportOnce(lastErr, errs)
}

// reportOnce prints msg to standard error unless it is empty or the same as
// the last message, and returns it as the new last message.
func reportOnce(last, msg string) string {
	if msg != "" && msg != last {
		for _, line := range strings.Split(msg, "\n") {
			fmt.Fprintln(os.Stderr, "  !", line)
		}
	}
	return msg
}
