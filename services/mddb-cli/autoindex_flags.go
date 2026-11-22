package main

import (
	"time"

	"github.com/spf13/pflag"
)

// defaultWatchInterval is how often --watch looks for changed files.
const defaultWatchInterval = 2 * time.Second

// addAutoindexFlags registers autoindex's flags on f. `autoindex` and
// `autoindex hook run` share them, so a hook runs with the flags it was
// installed with (AGT-006).
func addAutoindexFlags(f *pflag.FlagSet, o *autoindexOptions) {
	f.StringVarP(&o.collection, "collection", "c", "", "Collection name (default: the repository or directory name)")
	f.StringVar(&o.lang, "lang", "en", "Language code stored on every document")
	f.StringArrayVar(&o.include, "include", nil, "Only index paths matching this glob (repeatable)")
	f.StringArrayVar(&o.exclude, "exclude", nil, "Skip paths matching this glob (repeatable)")
	f.Int64Var(&o.maxSize, "max-file-size", defaultMaxFileSize, "Skip files larger than this many bytes")
	f.StringVar(&o.profile, "profile", "", `Ingest profile: "" (default) or "fast"`)
	f.BoolVar(&o.prune, "prune", false, "Delete documents whose file no longer exists")
	f.BoolVar(&o.dryRun, "dry-run", false, "Show what would be indexed, skipped and pruned; write nothing")
	f.BoolVar(&o.noFrontmatter, "no-frontmatter", false, "Store Markdown files as they are; do not read frontmatter as meta")
	f.BoolVar(&o.images, "images", false, "Store the images of Markdown files (their linked JPEG, PNG, GIF and WebP files) and PDFs as image documents")
	f.StringVar(&o.semantic.langs, "semantic", "", "Send language-server facts: auto, or a list ("+semanticLanguagesUsage()+"); off by default")
	f.Lookup("semantic").NoOptDefVal = semanticAuto
	f.DurationVar(&o.semantic.timeout, "semantic-timeout", defaultSemanticTimeout, "Stop a language's server after this long")
	f.IntVar(&o.semantic.concurrency, "semantic-concurrency", defaultSemanticConcurrency, "Requests in flight to a language server")
	f.IntVar(&o.semantic.memoryMb, "semantic-memory-mb", 0, "Memory limit passed to servers that take one (gopls), in MiB; 0 = none")
	f.StringVar(&o.semantic.compileCommands, "compile-commands", "", "compile_commands.json or its directory, for C and C++ (default: the root, then <root>/build)")
	f.StringVar(&o.changedSince, "changed-since", "", "Only send the files git reports changed since this commit, and untracked ones; with --prune delete the deleted ones")
	f.BoolVar(&o.watch, "watch", false, "Index, then keep running and index every file that changes")
	f.DurationVar(&o.interval, "interval", defaultWatchInterval, "How often --watch looks for changed files")
}
