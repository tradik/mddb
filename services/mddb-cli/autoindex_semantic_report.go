package main

import (
	"fmt"
	"time"

	"mddb-cli/internal/lspindex"
)

// The --semantic section of the autoindex report (CODE-011).

// semanticReport is the report's semantic section.
type semanticReport struct {
	Mode      string             `json:"mode"`
	Languages []semanticLanguage `json:"languages,omitempty"`
	// Sent counts documents sent with facts; Unchanged, documents whose
	// content and facts the collection already held, so not sent at all;
	// FactsUpdated, documents whose content was unchanged but whose facts
	// were not, rewritten with a meta-only update.
	Sent         int      `json:"sent"`
	Unchanged    int      `json:"unchanged"`
	FactsUpdated int      `json:"factsUpdated"`
	Warnings     []string `json:"warnings,omitempty"`
}

// semanticLanguage is one language server's run.
type semanticLanguage struct {
	Language    string `json:"language"`
	Server      string `json:"server,omitempty"`
	Files       int    `json:"files"`
	Defs        int    `json:"defs"`
	Refs        int    `json:"refs"`
	Impls       int    `json:"impls"`
	Skipped     int    `json:"skipped"`
	TimedOut    int    `json:"timedOut"`
	Unencodable int    `json:"unencodable"`
	ElapsedMs   int64  `json:"elapsedMs"`
	LoadWaitMs  int64  `json:"loadWaitMs"`
	PeakRssMb   int    `json:"peakRssMb"`
	// Error is why the language got no facts, in auto mode.
	Error string `json:"error,omitempty"`
}

// languageStats is one language's section of the report.
func languageStats(lang string, res *lspindex.Result) semanticLanguage {
	s := res.Stats
	return semanticLanguage{Language: lang, Server: res.Server, Files: s.Files, Defs: s.Defs, Refs: s.Refs,
		Impls: s.Impls, Skipped: s.Skipped, TimedOut: s.TimedOut, Unencodable: s.Unencodable,
		ElapsedMs: s.Elapsed.Milliseconds(), LoadWaitMs: s.LoadWait.Milliseconds(), PeakRssMb: s.PeakRssMb}
}

// warn records a warning; the run goes on.
func (r *semanticReport) warn(msg string) { r.Warnings = append(r.Warnings, msg) }

// printSemantic prints the semantic section: one line per language, the
// documents' fate unless nothing was sent, then the warnings. Nothing
// without --semantic, so that output stays as it was.
func printSemantic(s *semanticReport, dryRun bool) {
	if s == nil {
		return
	}
	for _, l := range s.Languages {
		if l.Error != "" {
			fmt.Printf("  semantic %s: no facts\n", l.Language)
			continue
		}
		fmt.Printf("  semantic %s: %s — %d files, %d defs, %d refs, %d impls, %d skipped (%d timed out), %d unencodable in %s (load %s), peak RSS %d MB\n",
			l.Language, l.Server, l.Files, l.Defs, l.Refs, l.Impls, l.Skipped, l.TimedOut, l.Unencodable,
			msDuration(l.ElapsedMs), msDuration(l.LoadWaitMs), l.PeakRssMb)
	}
	if !dryRun {
		fmt.Printf("  semantic: %d documents sent with facts, %d unchanged, %d with only their facts updated\n",
			s.Sent, s.Unchanged, s.FactsUpdated)
	}
	for _, w := range s.Warnings {
		fmt.Println("  ! semantic:", w)
	}
}

// msDuration prints milliseconds as a duration rounded to a tenth of a second.
func msDuration(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
}
