package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// plan (AGT-007): a project's backlog, release plans and progress notes as
// documents in its "<repo>-work" collection, so a session asks for the next
// row instead of reading a whole BACKLOG.md. Every subcommand is a thin
// layer over the document API; see docs/WORK-TRACKING.md.

// planFlags are the flags every plan subcommand shares.
type planFlags struct {
	collection, lang string
	// st is the store the flags name, set before a subcommand runs.
	st planStore
}

// workDir is os.Getwd; a variable so a test can make it fail.
var workDir = os.Getwd

// store builds the planStore the flags name. The collection defaults to the
// one autoindex would pick for the working directory, with "-work" added.
func (f planFlags) store() (planStore, error) {
	coll := f.collection
	if coll == "" {
		root, err := workDir()
		if err != nil {
			return planStore{}, err
		}
		coll = autoindexCollection("", root, detectGit(root)) + "-work"
	}
	return planStore{client: newClient(), collection: coll, lang: f.lang}, nil
}

func newPlanCmd() *cobra.Command {
	f := &planFlags{}
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Keep a project's backlog in its -work collection",
		Long: `Keep a project's backlog as documents in a collection: one document per row,
key backlog/<slug>, the prompt as content and kind, status, size, order, issues,
release and model as meta.

The collection defaults to the one autoindex picks for the working directory
(the repository's name, else the directory's) with "-work" added.`,
	}
	cmd.PersistentPreRunE = func(*cobra.Command, []string) error {
		var err error
		f.st, err = f.store()
		return err
	}
	cmd.PersistentFlags().StringVarP(&f.collection, "collection", "c", "", "Collection (default: <repository or directory name>-work)")
	cmd.PersistentFlags().StringVar(&f.lang, "lang", "en", "Language code of the plan's documents")
	cmd.AddCommand(newPlanAddCmd(f), newPlanNextCmd(f), newPlanDoneCmd(f), newPlanListCmd(f),
		newPlanImportCmd(f), newPlanExportCmd(f))
	return cmd
}

func newPlanAddCmd(f *planFlags) *cobra.Command {
	var r planRow
	var issues []int
	cmd := &cobra.Command{
		Use:   `add "<prompt>"`,
		Short: "Add a row at the end of the backlog",
		Long: `Add a row. Its slug is the prompt's first words unless --slug names one; a
derived slug that is taken gets "-2", "-3". The same prompt added twice is one
row. Issue numbers written as #N in the prompt are added to --issue.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := f.st
			ctx := context.Background()
			var err error
			if r.size, err = planSize(r.size); err != nil {
				return err
			}
			r.prompt, r.issues = strings.TrimSpace(args[0]), rowIssues(args[0], issues)
			slugGiven := r.slug != ""
			if !slugGiven {
				r.slug = slugify(r.prompt)
			}
			if r.order == 0 {
				if r.order, err = s.nextOrder(ctx); err != nil {
					return err
				}
			}
			doc, added, err := s.add(ctx, r, slugGiven)
			if err != nil {
				return err
			}
			if outputJSON {
				return printPlanJSON(doc)
			}
			if !added {
				fmt.Printf("Already in the backlog: %s\n", doc.Key)
				return nil
			}
			fmt.Printf("Added %s (order %s) to %s\n", doc.Key, metaFirst(*doc, "order"), s.collection)
			return nil
		},
	}
	cmd.Flags().StringVar(&r.size, "size", "", "Size: S, M or L")
	cmd.Flags().IntSliceVar(&issues, "issue", nil, "Issue number (repeatable)")
	cmd.Flags().StringVar(&r.release, "release", "", "Release the row belongs to")
	cmd.Flags().StringVar(&r.model, "model", "", "Model to run the session on")
	cmd.Flags().IntVar(&r.order, "order", 0, "Position (default: after the last row)")
	cmd.Flags().StringVar(&r.slug, "slug", "", "Slug (default: from the prompt)")
	return cmd
}

// planSize checks a --size value and writes it in capitals.
func planSize(size string) (string, error) {
	switch s := strings.ToUpper(size); s {
	case "", "S", "M", "L":
		return s, nil
	default:
		return "", fmt.Errorf("--size is S, M or L, not %q", size)
	}
}

func newPlanNextCmd(f *planFlags) *cobra.Command {
	var peek bool
	cmd := &cobra.Command{
		Use:   "next",
		Short: "Take the first todo row and print its prompt",
		Long: `Take the first todo row by order, mark it doing and print its prompt. The row
is taken with a conditional write: two sessions asking at the same moment get
different rows. --peek prints the row without taking it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := f.st
			ctx := context.Background()
			take := s.claim
			if peek {
				take = s.first
			}
			doc, err := take(ctx)
			if err != nil {
				return err
			}
			if outputJSON {
				return printPlanJSON(doc)
			}
			if doc == nil {
				fmt.Printf("No todo rows in %s\n", s.collection)
				return nil
			}
			fmt.Printf("%s (size %s, order %s)\n\n%s\n", rowSlug(*doc), orDash(metaFirst(*doc, "size")),
				metaFirst(*doc, "order"), strings.TrimSpace(doc.ContentMD))
			return nil
		},
	}
	cmd.Flags().BoolVar(&peek, "peek", false, "Show the next row without taking it")
	return cmd
}

func newPlanDoneCmd(f *planFlags) *cobra.Command {
	var commit, note string
	cmd := &cobra.Command{
		Use:   "done <slug>",
		Short: "Mark a row done",
		Long: `Mark a row done and record the commit. --note also writes a progress note,
progress/<date>-<slug>, with the row's issues and release: what was done, what
is left, what not to do.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := f.st
			ctx := context.Background()
			slug := strings.TrimPrefix(args[0], planKey)
			doc, err := s.update(ctx, planKey+slug, func(d *mddb.Document) {
				d.Meta["status"] = []string{statusDone}
				if commit != "" {
					d.Meta["commit"] = []string{commit}
				}
			})
			if err != nil {
				return err
			}
			if note != "" {
				if doc, err = s.progress(ctx, *doc, note, time.Now()); err != nil {
					return err
				}
			}
			if outputJSON {
				return printPlanJSON(doc)
			}
			fmt.Printf("Done: %s\n", slug)
			if note != "" {
				fmt.Printf("Progress note: %s\n", doc.Key)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&commit, "commit", "", "Commit that finished the row")
	cmd.Flags().StringVar(&note, "note", "", "Write a progress note with this text")
	return cmd
}
