package main

import (
	"encoding/json"
	"fmt"
	"time"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// The autoindex report: what each ingest call did, and how a run is printed.

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
	// pruneCounts is how many documents carry each path in ToPrune: more
	// than one means a mail file's messages and attachments (GO-046).
	pruneCounts map[string]int
	Skipped     []autoSkip `json:"skipped,omitempty"`
	// Converted lists the files the server converted to Markdown, with the
	// format each was converted from.
	Converted []autoConverted `json:"converted,omitempty"`
	// Semantic is the --semantic section (CODE-011); absent without it.
	Semantic *semanticReport `json:"semantic,omitempty"`
	Errors   []string        `json:"errors,omitempty"`
	// announced is the server's `semantic` map from /v1/code-languages:
	// the languages it keeps facts for; nil from a server before 2.19.3.
	announced map[string]bool
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
		printSemantic(rep.Semantic, true)
		return nil
	}
	fmt.Printf("✓ %s: %d files (%.1f MB) — %d added, %d updated, %d unchanged, %d failed, %d pruned, %d skipped in %dms\n",
		rep.Collection, rep.Files, float64(rep.Bytes)/1e6, rep.Added, rep.Updated, rep.Unchanged, rep.Failed,
		rep.Pruned, len(rep.Skipped), rep.DurationMs)
	for _, c := range rep.Converted {
		fmt.Printf("  ~ %s: converted from %s%s\n", c.Rel, c.Format, c.quality())
	}
	printSemantic(rep.Semantic, false)
	for _, e := range rep.Errors {
		fmt.Println("  !", e)
	}
	return nil
}
