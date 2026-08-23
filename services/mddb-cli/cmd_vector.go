package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// Vector index maintenance.

func newVectorReindexCmd() *cobra.Command {
	vectorReindexCmd := &cobra.Command{
		Use:   "vector-reindex [collection]",
		Short: "Re-embed documents in a collection",
		Long:  `Re-generate embedding vectors for all documents in a collection.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection := args[0]
			force, _ := cmd.Flags().GetBool("force")

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"force":      force,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/vector-reindex", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var result map[string]interface{}
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				embedded := int(asFloat(result["embedded"]))
				skipped := int(asFloat(result["skipped"]))
				failed := int(asFloat(result["failed"]))

				fmt.Printf("Reindex completed for collection: %s\n", collection)
				fmt.Printf("  Embedded: %d\n", embedded)
				fmt.Printf("  Skipped:  %d\n", skipped)
				fmt.Printf("  Failed:   %d\n", failed)

				if errs, ok := result["errors"].([]interface{}); ok && len(errs) > 0 {
					fmt.Printf("\n  Errors:\n")
					for _, e := range errs {
						fmt.Printf("    - %s\n", e)
					}
				}
			}

			return nil
		},
	}
	vectorReindexCmd.Flags().Bool("force", false, "Force re-embed all documents (ignore content hash)")
	return vectorReindexCmd
}

func newVectorStatsCmd() *cobra.Command {
	vectorStatsCmd := &cobra.Command{
		Use:   "vector-stats",
		Short: "Show vector/embedding statistics",
		Long:  `Display embedding provider info and per-collection embedding statistics.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resp, err := client.Do(context.Background(), "GET", "/v1/vector-stats", nil)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var result map[string]interface{}
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}

				enabled, _ := result["enabled"].(bool)
				fmt.Printf("Vector Search Statistics\n")
				fmt.Printf("═══════════════════════════════════════\n\n")

				if !enabled {
					fmt.Println("Embedding provider: disabled")
					fmt.Println("Set MDDB_EMBEDDING_PROVIDER to enable vector search.")
					return nil
				}

				fmt.Printf("Provider:   %s\n", result["provider"])
				fmt.Printf("Model:      %s\n", result["model"])
				fmt.Printf("Dimensions: %v\n", result["dimensions"])
				fmt.Printf("Index Ready: %v\n\n", result["index_ready"])

				if collections, ok := result["collections"].(map[string]interface{}); ok && len(collections) > 0 {
					fmt.Printf("Collections:\n")
					fmt.Printf("─────────────────────────────────────────\n")
					fmt.Printf("%-20s %12s %12s %10s\n", "Name", "Documents", "Embedded", "Coverage")
					fmt.Printf("─────────────────────────────────────────\n")
					for name, v := range collections {
						coll := asMap(v)
						total := int(asFloat(coll["total_documents"]))
						embedded := int(asFloat(coll["embedded_documents"]))
						coverage := 0.0
						if total > 0 {
							coverage = float64(embedded) / float64(total) * 100
						}
						fmt.Printf("%-20s %12d %12d %9.0f%%\n", name, total, embedded, coverage)
					}
				} else {
					fmt.Println("No collections with embeddings.")
				}
			}

			return nil
		},
	}
	return vectorStatsCmd
}
