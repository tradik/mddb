package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// `mddb-cli autoindex hook` (AGT-006): keep a collection in step with a
// repository's commits through git hooks.

func newAutoindexHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Re-index a repository after each commit, merge, checkout and rewrite",
		Long: `Install git hooks that re-index the repository after it moves.

The hook starts "autoindex hook run" in the background and returns at once: the
commit never waits for the server and never fails because of it. The run sends
the files changed since the commit it last indexed (the first run sends all),
with the autoindex flags given to install after "--". Its output goes to
mddb-autoindex.log in the repository's git directory. The server address comes
from --server, else MDDB_SERVER; the API key from MDDB_API_KEY in the
environment the hook runs in — it is never written into the hook.`,
	}
	cmd.AddCommand(newHookInstallCmd(), newHookUninstallCmd(), newHookStatusCmd(), newHookRunCmd())
	return cmd
}

func newHookInstallCmd() *cobra.Command {
	var events []string
	cmd := &cobra.Command{
		Use:   "install [path] [-- autoindex flags]",
		Short: "Add the autoindex block to the repository's hooks",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, flags, err := splitHookArgs(cmd, args)
			if err != nil {
				return err
			}
			if cmd.Root().PersistentFlags().Changed("server") {
				flags = append([]string{"--server", serverURL}, flags...)
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			return hookInstall(root, exe, events, flags)
		},
	}
	cmd.Flags().StringSliceVar(&events, "events", defaultHookEvents, "Hooks to install")
	return cmd
}

func newHookUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall [path]",
		Short: "Take the autoindex block out of the repository's hooks",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return hookUninstall(rootArg(args))
		},
	}
}

func newHookStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [path]",
		Short: "Show which hooks hold the autoindex block and the commit last indexed",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return hookStatus(rootArg(args))
		},
	}
}

func newHookRunCmd() *cobra.Command {
	var o autoindexOptions
	cmd := &cobra.Command{
		Use:    "run [path]",
		Short:  "What the hooks run: index the files changed since the commit last indexed",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			o.root = rootArg(args)
			return hookRun(context.Background(), newClient(), o)
		},
	}
	addAutoindexFlags(cmd.Flags(), &o)
	return cmd
}

// splitHookArgs is install's path (before "--") and the autoindex flags
// (after it), checked by parsing them as hook run will.
func splitHookArgs(cmd *cobra.Command, args []string) (string, []string, error) {
	pathArgs, flags := args, []string(nil)
	if at := cmd.ArgsLenAtDash(); at >= 0 {
		pathArgs, flags = args[:at], args[at:]
	}
	if len(pathArgs) > 1 {
		return "", nil, errors.New("install takes one path; autoindex flags go after --")
	}
	var o autoindexOptions
	fs := pflag.NewFlagSet("autoindex", pflag.ContinueOnError)
	fs.SetOutput(new(strings.Builder))
	addAutoindexFlags(fs, &o)
	if err := fs.Parse(flags); err != nil {
		return "", nil, err
	}
	if o.watch || o.dryRun || o.changedSince != "" || fs.NArg() > 0 {
		return "", nil, errors.New("a hook takes autoindex flags, but not --watch, --dry-run, --changed-since or a path")
	}
	return rootArg(pathArgs), flags, nil
}

// hookInstall writes the block into each event's hook.
func hookInstall(root, exe string, events, flags []string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	p, err := repoHookPaths(abs)
	if err != nil {
		return err
	}
	block := hookBlock(exe, abs, p.log, flags)
	for _, ev := range events {
		if !strings.HasPrefix(ev, "post-") || strings.ContainsAny(ev, `/\`) {
			return fmt.Errorf("%q is not a post-* hook", ev)
		}
		if err := installHook(p.hooks, ev, block); err != nil {
			return err
		}
		fmt.Printf("✓ %s\n", filepath.Join(p.hooks, ev))
	}
	return nil
}

// hookUninstall takes the block out of every hook that has it.
func hookUninstall(root string) error {
	p, err := repoHookPaths(root)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(p.hooks)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	removed := 0
	for _, e := range entries {
		had, err := uninstallHook(p.hooks, e.Name())
		if err != nil {
			return err
		}
		if had {
			removed++
			fmt.Printf("✓ removed from %s\n", filepath.Join(p.hooks, e.Name()))
		}
	}
	if removed == 0 {
		fmt.Println("No hook holds the autoindex block.")
	}
	_ = os.Remove(p.state)
	return nil
}

// hookStatus prints which hooks hold the block, the commit last indexed,
// and whether a run is going.
func hookStatus(root string) error {
	p, err := repoHookPaths(root)
	if err != nil {
		return err
	}
	for _, ev := range defaultHookEvents {
		state := "not installed"
		if hookInstalled(p.hooks, ev) {
			state = "installed"
		}
		fmt.Printf("%-14s %s\n", ev, state)
	}
	last := "none (the next run sends every file)"
	if data, err := os.ReadFile(p.state); err == nil { // #nosec G304 -- under the repository's git directory
		last = strings.TrimSpace(string(data))
	}
	fmt.Println("last indexed  ", last)
	if _, err := os.Stat(p.lock); err == nil {
		fmt.Println("a run is going")
	}
	fmt.Println("log           ", p.log)
	return nil
}

// hookRun indexes what changed since the commit last indexed, holding the
// lock. A hook that fires while a run holds it marks the tree dirty and
// leaves; the run then goes once more, so a rebase of fifty commits costs
// two runs, not fifty.
func hookRun(ctx context.Context, client *mddb.Client, o autoindexOptions) error {
	if o.watch || o.dryRun {
		return errors.New("hook run does not take --watch or --dry-run")
	}
	root, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	p, err := repoHookPaths(root)
	if err != nil {
		return err
	}
	o.root = root
	ok, err := takeLock(p.lock)
	if err != nil {
		return err
	}
	if !ok {
		return os.WriteFile(p.dirty, nil, 0o600)
	}
	defer func() { _ = os.Remove(p.lock) }()
	for {
		_ = os.Remove(p.dirty)
		if err := hookPass(ctx, client, o, root, p.state); err != nil {
			return err
		}
		if _, err := os.Stat(p.dirty); err != nil {
			return nil
		}
	}
}

// hookPass runs once from the commit in the state file — all files when
// there is none or it is gone (a rebase, a gc) — and records HEAD when
// everything reached the server.
func hookPass(ctx context.Context, client *mddb.Client, o autoindexOptions, root, stateFile string) error {
	start := time.Now()
	since := ""
	if data, err := os.ReadFile(stateFile); err == nil { // #nosec G304 -- under the repository's git directory
		since = strings.TrimSpace(string(data))
	}
	if since != "" {
		if _, err := runGit(root, "cat-file", "-e", since+"^{commit}"); err != nil {
			since = ""
		}
	}
	o.changedSince = since
	head := detectGit(root)
	fmt.Printf("%s autoindex since %s\n", start.Format(time.RFC3339), orAll(since))
	rep, err := autoindexPass(ctx, client, o)
	if err != nil {
		return err
	}
	if err := printAutoindex(rep, start); err != nil {
		return err
	}
	if rep.sendFailed || head == nil || head.Commit == "" {
		return nil
	}
	return os.WriteFile(stateFile, []byte(head.Commit+"\n"), 0o600)
}

// orAll names the start of a run for the log.
func orAll(since string) string {
	if since == "" {
		return "the start (every file)"
	}
	return since
}
