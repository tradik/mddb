package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

// mddb-cli alias (GO-044): stable names for collections.
func newAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage collection aliases",
		Long: `An alias is a stable name for a collection. Clients use the alias; moving it
to another collection is one atomic step, so a rebuilt collection replaces the
old one without any client changing a name. See also reindex-swap.`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List aliases",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			aliases, err := newClient().Aliases(context.Background())
			if err != nil {
				return err
			}
			if len(aliases) == 0 {
				fmt.Println("No aliases.")
			}
			for _, a := range aliases {
				fmt.Printf("%s -> %s\n", a.Alias, a.Collection)
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "set <alias> <collection>",
		Short: "Create an alias, or move it to another collection",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := newClient().SetAlias(context.Background(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("%s -> %s\n", args[0], args[1])
			return nil
		},
	}, &cobra.Command{
		Use:   "delete <alias>",
		Short: "Remove an alias (the collection stays)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := newClient().DeleteAlias(context.Background(), args[0]); err != nil {
				return err
			}
			fmt.Printf("Deleted alias %s\n", args[0])
			return nil
		},
	})
	return cmd
}
