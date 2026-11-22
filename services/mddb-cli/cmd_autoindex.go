package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// autoindex (AGT-001): a directory in, a searchable collection out, with
// nothing to decide first. The CLI walks the directory, marks each file's
// kind and language from the server's own table, sends the files in batches,
// and lets the server do the rest — symbols, chunks, embeddings — then asks
// the search advisor to store a retrieval profile for the collection.

const (
	autoindexBatchDocs  = 200
	autoindexBatchBytes = 4 << 20
	defaultMaxFileSize  = 1 << 20
)

type autoindexOptions struct {
	root, collection, lang, profile string
	include, exclude                []string
	maxSize                         int64
	prune, dryRun                   bool
	// images sends each Markdown file's linked images and asks the server
	// to store them, and a PDF's pictures, as image documents (RAG-010).
	images bool
	// noFrontmatter stores Markdown files as they are; by default the
	// server's ingest reads their frontmatter as meta (#373).
	noFrontmatter bool
	// extraMeta is added to every document: a reference repository's
	// licence and source (AGT-003).
	extraMeta map[string][]string
	// semantic runs language servers and sends their facts (CODE-011).
	semantic semanticOptions
	// changedSince narrows a run to the files git reports changed since
	// this commit, plus untracked ones (AGT-006).
	changedSince string
	// only, when not nil, narrows a run to these paths: the ones --watch
	// saw change, or the ones --changed-since listed.
	only map[string]bool
	// watch keeps the command running and indexes what changes, checking
	// every interval (AGT-006).
	watch    bool
	interval time.Duration
}

func newAutoindexCmd() *cobra.Command {
	var o autoindexOptions
	cmd := &cobra.Command{
		Use:   "autoindex [path]",
		Short: "Index a directory or repository in one command",
		Long: `Index every text file under a directory into one collection.

In a git repository the files are the ones git tracks or would track; elsewhere
everything except node_modules, target, dist, build, vendor and similar. Binary
files are skipped by content, except the documents the server converts to
Markdown: PDF, DOCX, ODT, RTF, XLSX, ODS, PPTX, ODP and EPUB by content, and
HTML, TeX, CSV, TSV, EML and MBOX by extension. Each document gets meta path, extension, kind,
language, size and modified (and gitCommit, gitBranch in a repository); the
server extracts symbols. A Markdown file's frontmatter becomes meta too, read by
the server as /v1/upload reads it, and the body is stored without it; the meta
above wins for a key both name, and --no-frontmatter turns this off.
A second run only rewrites files whose content changed.

--semantic runs a language server (gopls; basedpyright or pyrefly; phpactor or
PHPantom; clangd for C and C++; rust-analyzer for Rust) over the checkout, one
language after another, and sends where each symbol is defined, referenced and
implemented with the documents. Alone it means auto: every language in the tree
that the server keeps facts for and that has a server installed; a missing
server is a warning. --semantic=go,python names the languages: a missing or
failing server is then an error and nothing is sent. Write the list with '=': a
separate word is the path. clangd needs the build's compile_commands.json:
--compile-commands names it (or its directory); by default the root and
<root>/build are searched. rust-analyzer needs the workspace's Cargo.toml at the
root or one directory below it.

--changed-since REF sends only the files git reports changed between REF and
the working tree, and the untracked ones; with --prune it deletes the documents
of the files deleted or renamed away. --watch indexes, then keeps running: every
--interval (2s) it looks at the files' size and modification time and sends
the ones that changed, deleting the gone ones with --prune; Ctrl-C stops it.
Both leave the documents a full --prune run would. --semantic needs the whole
tree, so it keeps a --changed-since run whole and runs only on --watch's first
pass. "autoindex hook install" re-indexes after each commit instead.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.root = rootArg(args)
			if o.watch {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				return runAutoindexWatch(ctx, newClient(), o)
			}
			return runAutoindex(context.Background(), newClient(), o)
		},
	}
	addAutoindexFlags(cmd.Flags(), &o)
	cmd.AddCommand(newAutoindexHookCmd())
	return cmd
}

// rootArg is the directory a command was given, "." when none.
func rootArg(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return "."
}

// runAutoindex runs one pass and prints its report.
func runAutoindex(ctx context.Context, client *mddb.Client, o autoindexOptions) error {
	start := time.Now()
	rep, err := autoindexPass(ctx, client, o)
	if err != nil {
		return err
	}
	return printAutoindex(rep, start)
}

// autoindexPass plans the run, then carries it out unless it is a dry run.
func autoindexPass(ctx context.Context, client *mddb.Client, o autoindexOptions) (*autoindexReport, error) {
	root, err := filepath.Abs(o.root)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", o.root)
	}
	rep := &autoindexReport{Root: root, Git: detectGit(root), DryRun: o.dryRun}
	rep.Collection = autoindexCollection(o.collection, root, rep.Git)
	if o.only, err = changedScope(root, rep, o); err != nil {
		return nil, err
	}

	files, err := planFiles(ctx, client, root, rep, o)
	if err != nil {
		return nil, err
	}
	rep.IncludeDirs = includeDirs(files)
	if o.only != nil {
		files = narrowTo(rep, files, o)
	} else if o.prune {
		if rep.ToPrune, rep.pruneCounts, err = prunable(ctx, client, rep.Collection, files); err != nil {
			return nil, err
		}
	}
	// Facts come before the first write: a named language that fails stops
	// the run with nothing sent.
	facts, err := semanticFacts(ctx, root, files, rep, o.semantic)
	if err != nil {
		return nil, err
	}
	if o.dryRun {
		rep.Plan = files
		return rep, nil
	}
	// A narrowed run changes a few files: the include roots and the
	// retrieval profile are the full run's to set.
	whole := o.only == nil
	if whole {
		if err := setIncludePaths(ctx, client, rep.Collection, rep.IncludeDirs); err != nil {
			rep.Errors = append(rep.Errors, "code.includePaths: "+err.Error())
		}
	}
	semSync := newSemanticSync(ctx, client, rep, facts)
	ingestFiles(ctx, client, rep, files, o, semSync)
	if semSync != nil {
		semSync.reconcile(ctx, client, rep.Collection, o.lang)
		semSync.count(rep)
	}
	if len(rep.ToPrune) > 0 {
		pruneDocs(ctx, client, rep, o.lang)
	}
	if whole {
		if _, err := client.SearchAdvisor(ctx, rep.Collection, true); err != nil {
			rep.Errors = append(rep.Errors, "search advisor: "+err.Error())
		}
	}
	return rep, nil
}

// autoindexCollection is the collection a run writes to: the one asked for,
// else the repository's name, else the directory's.
func autoindexCollection(asked, root string, g *gitInfo) string {
	if asked != "" {
		return asked
	}
	name := filepath.Base(root)
	if g != nil && g.Name != "" {
		name = g.Name
	}
	return sanitizeCollection(name)
}

// planFiles lists and classifies the files, recording skips in the report.
func planFiles(ctx context.Context, client *mddb.Client, root string, rep *autoindexReport, o autoindexOptions) ([]autoFile, error) {
	var paths []string
	var err error
	if rep.Git != nil {
		paths, err = gitFiles(root)
	} else {
		paths, err = walkFiles(root)
	}
	if err != nil {
		return nil, err
	}
	c := classifier{include: o.include, exclude: o.exclude, maxSize: o.maxSize}
	if langs, err := client.CodeLanguages(ctx); err == nil {
		c.extensions, rep.announced = langs.Extensions, langs.Semantic
	} else {
		rep.Errors = append(rep.Errors, "code languages unavailable, the server will classify files: "+err.Error())
	}
	var files []autoFile
	for _, rel := range paths {
		f, skip := c.classify(root, rel)
		if skip != nil {
			rep.Skipped = append(rep.Skipped, *skip)
			continue
		}
		files = append(files, *f)
		rep.Files++
		rep.Bytes += f.Size
	}
	return files, nil
}

// fileMeta is the metadata a file's document carries. Symbols (defines,
// uses, imports) are the server's to extract, not the client's to send.
func fileMeta(f autoFile, g *gitInfo) map[string][]string {
	m := map[string][]string{
		"path": {f.Rel}, "extension": {filepath.Ext(f.Rel)},
		"size": {strconv.FormatInt(f.Size, 10)}, "modified": {f.modified},
	}
	if f.Kind != "" {
		m["kind"] = []string{f.Kind}
	}
	if f.Language != "" {
		m["language"] = []string{f.Language}
	}
	if g != nil && g.Commit != "" {
		m["gitCommit"], m["gitBranch"] = []string{g.Commit}, []string{g.Branch}
	}
	return m
}
