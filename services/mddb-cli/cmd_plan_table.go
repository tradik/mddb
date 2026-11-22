package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// The plan subcommands that read the backlog as a whole: list, and the
// Markdown table in and out (AGT-007).

func newPlanListCmd(f *planFlags) *cobra.Command {
	var statuses []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the backlog's rows",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := f.st
			docs, err := s.rows(context.Background(), statuses)
			if err != nil {
				return err
			}
			if outputJSON {
				return printPlanJSON(docs)
			}
			printPlanRows(s.collection, docs)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&statuses, "status", nil, "Only rows with this status: todo, doing, done (repeatable)")
	return cmd
}

func newPlanImportCmd(f *planFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "import <file.md>",
		Short: "Add the rows of a Markdown table",
		Long: `Add one row per line of the first Markdown table in a file, after the rows the
backlog already has. Prompt, Size and Model columns become the row's prompt,
size and model; other columns are kept as meta, so plan export writes the same
table back. A row whose prompt is already in the backlog is not added twice.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := f.st
			data, err := os.ReadFile(args[0]) // #nosec G304 -- the file the user named
			if err != nil {
				return err
			}
			n, have, err := s.importTable(context.Background(), string(data))
			if err != nil {
				return err
			}
			fmt.Printf("Imported %d rows into %s (%d already there)\n", n, s.collection, have)
			return nil
		},
	}
}

func newPlanExportCmd(f *planFlags) *cobra.Command {
	statuses := []string{statusTodo, statusDoing}
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Print the backlog as a Markdown table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := f.st
			docs, err := s.rows(context.Background(), statuses)
			if err != nil {
				return err
			}
			fmt.Print(renderTable(exportTable(docs)))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&statuses, "status", statuses, "Rows with this status (repeatable)")
	return cmd
}
