package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	// extraMeta is added to every document: a reference repository's
	// licence and source (AGT-003).
	extraMeta map[string][]string
}

// autoindexReport is what a run did; printed as text or, with --json, as is.
type autoindexReport struct {
	Collection  string     `json:"collection"`
	Root        string     `json:"root"`
	Git         *gitInfo   `json:"git,omitempty"`
	Files       int        `json:"files"`
	Bytes       int64      `json:"bytes"`
	Added       int        `json:"added"`
	Updated     int        `json:"updated"`
	Unchanged   int        `json:"unchanged"`
	Failed      int        `json:"failed"`
	Pruned      int        `json:"pruned"`
	DurationMs  int64      `json:"durationMs"`
	DryRun      bool       `json:"dryRun,omitempty"`
	IncludeDirs []string   `json:"includePaths,omitempty"`
	Plan        []autoFile `json:"plan,omitempty"`
	ToPrune     []string   `json:"toPrune,omitempty"`
	Skipped     []autoSkip `json:"skipped,omitempty"`
	// Converted lists the files the server converted to Markdown, with the
	// format each was converted from.
	Converted []autoConverted `json:"converted,omitempty"`
	Errors    []string        `json:"errors,omitempty"`
}

// autoConverted is a document the server converted from a file: its key
// (the file's path, or a path below it for a mailbox's messages and an
// email's attachments), the format, and how well it converted.
type autoConverted struct {
	Rel        string   `json:"path"`
	Format     string   `json:"format"`
	Confidence *float64 `json:"conversionConfidence,omitempty"`
	Warnings   []string `json:"conversionWarnings,omitempty"`
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
server extracts symbols. A second run only rewrites files whose content changed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.root = "."
			if len(args) == 1 {
				o.root = args[0]
			}
			return runAutoindex(context.Background(), newClient(), o)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.collection, "collection", "c", "", "Collection name (default: the repository or directory name)")
	f.StringVar(&o.lang, "lang", "en", "Language code stored on every document")
	f.StringArrayVar(&o.include, "include", nil, "Only index paths matching this glob (repeatable)")
	f.StringArrayVar(&o.exclude, "exclude", nil, "Skip paths matching this glob (repeatable)")
	f.Int64Var(&o.maxSize, "max-file-size", defaultMaxFileSize, "Skip files larger than this many bytes")
	f.StringVar(&o.profile, "profile", "", `Ingest profile: "" (default) or "fast"`)
	f.BoolVar(&o.prune, "prune", false, "Delete documents whose file no longer exists")
	f.BoolVar(&o.dryRun, "dry-run", false, "Show what would be indexed, skipped and pruned; write nothing")
	return cmd
}

// runAutoindex plans the run, then carries it out unless it is a dry run.
func runAutoindex(ctx context.Context, client *mddb.Client, o autoindexOptions) error {
	start := time.Now()
	root, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", o.root)
	}
	rep := &autoindexReport{Root: root, Git: detectGit(root), DryRun: o.dryRun}
	rep.Collection = o.collection
	if rep.Collection == "" {
		name := filepath.Base(root)
		if rep.Git != nil && rep.Git.Name != "" {
			name = rep.Git.Name
		}
		rep.Collection = sanitizeCollection(name)
	}

	files, err := planFiles(ctx, client, root, rep, o)
	if err != nil {
		return err
	}
	rep.IncludeDirs = includeDirs(files)
	if o.prune {
		if rep.ToPrune, err = prunable(ctx, client, rep.Collection, files); err != nil {
			return err
		}
	}
	if o.dryRun {
		rep.Plan = files
		return printAutoindex(rep, start)
	}
	if err := setIncludePaths(ctx, client, rep.Collection, rep.IncludeDirs); err != nil {
		rep.Errors = append(rep.Errors, "code.includePaths: "+err.Error())
	}
	ingestFiles(ctx, client, rep, files, o)
	if len(rep.ToPrune) > 0 {
		pruneDocs(ctx, client, rep, o.lang)
	}
	if _, err := client.SearchAdvisor(ctx, rep.Collection, true); err != nil {
		rep.Errors = append(rep.Errors, "search advisor: "+err.Error())
	}
	return printAutoindex(rep, start)
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
		c.extensions = langs.Extensions
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

// ingestFiles sends the files in batches of at most 200 documents or 4 MiB.
// Unchanged content is skipped by the server (skipDuplicates), which is what
// makes a second run cheap.
func ingestFiles(ctx context.Context, client *mddb.Client, rep *autoindexReport, files []autoFile, o autoindexOptions) {
	var batch []mddb.IngestDocument
	size := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		resp, err := client.Ingest(ctx, mddb.IngestRequest{Collection: rep.Collection, Documents: batch,
			Options: mddb.IngestOptions{SkipDuplicates: true, Profile: o.profile}})
		if err != nil {
			rep.Failed += len(batch)
			rep.Errors = append(rep.Errors, err.Error())
		} else {
			rep.Added, rep.Updated, rep.Unchanged = rep.Added+resp.Added, rep.Updated+resp.Updated, rep.Unchanged+resp.Skipped
			rep.Failed += resp.Failed
			rep.Errors = append(rep.Errors, resp.Errors...)
			rep.noteConversions(resp.Conversions)
		}
		batch, size = nil, 0
	}
	for _, f := range files {
		content, err := os.ReadFile(f.abs) // #nosec G304 -- a file under the directory the user asked to index
		if err != nil {
			rep.Failed++
			rep.Errors = append(rep.Errors, f.Rel+": "+err.Error())
			continue
		}
		if len(batch) > 0 && (len(batch) >= autoindexBatchDocs || size+wireSize(f, content) > autoindexBatchBytes) {
			flush()
		}
		meta := fileMeta(f, rep.Git)
		for k, v := range o.extraMeta {
			meta[k] = v
		}
		doc := mddb.IngestDocument{Key: f.Rel, Lang: o.lang, Meta: meta}
		if f.Format != "" {
			// The server converts it, with the converters /v1/upload uses.
			doc.Format, doc.Content = f.Format, content
			rep.Converted = append(rep.Converted, autoConverted{Rel: f.Rel, Format: f.Format})
		} else {
			doc.ContentMD = string(content)
		}
		batch = append(batch, doc)
		size += wireSize(f, content)
	}
	flush()
}

// noteConversions records how well the server converted each document:
// the file's own, and the messages and attachments it held.
func (rep *autoindexReport) noteConversions(convs []mddb.IngestConversion) {
	for _, c := range convs {
		confidence := c.Confidence
		entry := autoConverted{Rel: c.Key, Format: c.Format, Confidence: &confidence, Warnings: c.Warnings}
		found := false
		for i := range rep.Converted {
			if rep.Converted[i].Rel == c.Key {
				rep.Converted[i], found = entry, true
			}
		}
		if !found {
			rep.Converted = append(rep.Converted, entry)
		}
	}
}

// quality is the report's note on a conversion: its confidence, and the
// first warning when there is one.
func (c autoConverted) quality() string {
	if c.Confidence == nil {
		return ""
	}
	q := fmt.Sprintf(" (confidence %.2f", *c.Confidence)
	if len(c.Warnings) > 0 {
		q += ": " + c.Warnings[0]
	}
	return q + ")"
}

// wireSize is what a file adds to a batch: its bytes, or their base64 when
// it is sent for conversion.
func wireSize(f autoFile, content []byte) int {
	if f.Format != "" {
		return base64.StdEncoding.EncodedLen(len(content))
	}
	return len(content)
}

func printAutoindex(rep *autoindexReport, start time.Time) error {
	rep.DurationMs = time.Since(start).Milliseconds()
	if outputJSON {
		// A struct of plain fields: marshalling it cannot fail.
		data, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	if rep.DryRun {
		fmt.Printf("Dry run: %d files (%.1f MB) would go to collection %q; %d skipped; %d would be pruned\n",
			rep.Files, float64(rep.Bytes)/1e6, rep.Collection, len(rep.Skipped), len(rep.ToPrune))
		for _, f := range rep.Plan {
			fmt.Printf("  + %s (%s %s%s)\n", f.Rel, f.Kind, f.Language, f.Format)
		}
		for _, s := range rep.Skipped {
			fmt.Printf("  - %s: %s\n", s.Rel, s.Reason)
		}
		for _, p := range rep.ToPrune {
			fmt.Printf("  x %s\n", p)
		}
		return nil
	}
	fmt.Printf("✓ %s: %d files (%.1f MB) — %d added, %d updated, %d unchanged, %d failed, %d pruned, %d skipped in %dms\n",
		rep.Collection, rep.Files, float64(rep.Bytes)/1e6, rep.Added, rep.Updated, rep.Unchanged, rep.Failed,
		rep.Pruned, len(rep.Skipped), rep.DurationMs)
	for _, c := range rep.Converted {
		fmt.Printf("  ~ %s: converted from %s%s\n", c.Rel, c.Format, c.quality())
	}
	for _, e := range rep.Errors {
		fmt.Println("  !", e)
	}
	return nil
}
