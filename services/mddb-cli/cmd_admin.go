package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// Database-wide operations: export, backup, restore, truncate, stats.

func newExportCmd() *cobra.Command {
	exportCmd := &cobra.Command{
		Use:   "export [collection]",
		Short: "Export documents",
		Long:  `Export documents from a collection as NDJSON or ZIP.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection := args[0]
			format, _ := cmd.Flags().GetString("format")
			output, _ := cmd.Flags().GetString("output")
			metaStr, _ := cmd.Flags().GetString("filter")

			filterMeta := parseMetaFlag(metaStr)

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"filterMeta": filterMeta,
				"format":     format,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/export", body)
			if err != nil {
				return err
			}

			if output != "" {
				err = os.WriteFile(filepath.Clean(output), resp, 0600)
				if err != nil {
					return err
				}
				fmt.Printf("✓ Exported to: %s\n", output)
			} else {
				fmt.Print(string(resp))
			}

			return nil
		},
	}
	exportCmd.Flags().StringP("format", "F", "ndjson", "Export format: ndjson, zip")
	exportCmd.Flags().StringP("output", "o", "", "Output file (default: stdout)")
	exportCmd.Flags().StringP("filter", "f", "", "Filter by metadata: key=val1|val2,key2=val")
	return exportCmd
}

func newBackupCmd() *cobra.Command {
	backupCmd := &cobra.Command{
		Use:   "backup [filename]",
		Short: "Create database backup",
		Long:  `Create a backup of the database.`,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filename := fmt.Sprintf("backup-%d.db", time.Now().Unix())
			if len(args) > 0 {
				filename = args[0]
			}

			client := newClient()
			resp, err := client.Do(context.Background(), "GET", fmt.Sprintf("/v1/backup?to=%s", filename), nil)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var result map[string]string
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Printf("✓ Backup created: %s\n", result["backup"])
			}

			return nil
		},
	}
	return backupCmd
}

func newRestoreCmd() *cobra.Command {
	restoreCmd := &cobra.Command{
		Use:   "restore [filename]",
		Short: "Restore database from backup",
		Long:  `Restore the database from a backup file.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filename := args[0]

			client := newClient()
			body := map[string]string{"from": filename}
			resp, err := client.Do(context.Background(), "POST", "/v1/restore", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var result map[string]string
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Printf("✓ Restored from: %s\n", result["restored"])
			}

			return nil
		},
	}
	return restoreCmd
}

func newTruncateCmd() *cobra.Command {
	truncateCmd := &cobra.Command{
		Use:   "truncate [collection]",
		Short: "Truncate revision history",
		Long:  `Remove old revisions from a collection.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection := args[0]
			keepRevs, _ := cmd.Flags().GetInt("keep")
			dropCache, _ := cmd.Flags().GetBool("drop-cache")

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"keepRevs":   keepRevs,
				"dropCache":  dropCache,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/truncate", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				fmt.Printf("✓ Truncated revisions in collection: %s\n", collection)
				fmt.Printf("  Kept last %d revisions per document\n", keepRevs)
			}

			return nil
		},
	}
	truncateCmd.Flags().IntP("keep", "k", 5, "Number of revisions to keep")
	truncateCmd.Flags().BoolP("drop-cache", "d", true, "Drop cache")
	return truncateCmd
}

func newStatsCmd() *cobra.Command {
	statsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show server statistics",
		Long:  `Display database statistics including collection counts, revisions, and size.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resp, err := client.Do(context.Background(), "GET", "/v1/stats", nil)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var stats map[string]interface{}
				if err := json.Unmarshal(resp, &stats); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}

				fmt.Printf("MDDB Server Statistics\n")
				fmt.Printf("═══════════════════════════════════════\n\n")
				fmt.Printf("Database Path: %s\n", stats["databasePath"])
				fmt.Printf("Database Size: %.2f MB\n", asFloat(stats["databaseSize"])/1024/1024)
				fmt.Printf("Access Mode:   %s\n\n", stats["mode"])

				fmt.Printf("Global Totals:\n")
				fmt.Printf("  Documents:     %d\n", int(asFloat(stats["totalDocuments"])))
				fmt.Printf("  Revisions:     %d\n", int(asFloat(stats["totalRevisions"])))
				fmt.Printf("  Meta Indices:  %d\n\n", int(asFloat(stats["totalMetaIndices"])))

				if collections, ok := stats["collections"].([]interface{}); ok && len(collections) > 0 {
					fmt.Printf("Collections:\n")
					fmt.Printf("─────────────────────────────────────────\n")
					fmt.Printf("%-20s %10s %10s %10s\n", "Name", "Docs", "Revs", "Indices")
					fmt.Printf("─────────────────────────────────────────\n")
					for _, c := range collections {
						coll := asMap(c)
						fmt.Printf("%-20s %10d %10d %10d\n",
							coll["name"],
							int(asFloat(coll["documentCount"])),
							int(asFloat(coll["revisionCount"])),
							int(asFloat(coll["metaIndexCount"])))
					}
				} else {
					fmt.Printf("No collections found.\n")
				}
			}

			return nil
		},
	}
	return statsCmd
}
