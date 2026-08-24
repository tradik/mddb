package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Search commands: metadata filtering, full-text and vector.

func newSearchCmd() *cobra.Command {
	searchCmd := &cobra.Command{
		Use:   "search [collection]",
		Short: "Search documents",
		Long:  `Search documents in a collection with optional filters.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection := args[0]
			metaStr, _ := cmd.Flags().GetString("filter")
			sort, _ := cmd.Flags().GetString("sort")
			asc, _ := cmd.Flags().GetBool("asc")
			limit, _ := cmd.Flags().GetInt("limit")
			offset, _ := cmd.Flags().GetInt("offset")

			filterMeta := parseMetaFlag(metaStr)

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"filterMeta": filterMeta,
				"sort":       sort,
				"asc":        asc,
				"limit":      limit,
				"offset":     offset,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/search", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var docs []map[string]interface{}
				if err := json.Unmarshal(resp, &docs); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Printf("Found %d documents:\n\n", len(docs))
				for i, doc := range docs {
					fmt.Printf("%d. %s (%s)\n", i+1, doc["key"], doc["lang"])
					fmt.Printf("   ID: %s\n", doc["id"])
					fmt.Printf("   Updated: %v\n", formatUnix(doc["updatedAt"]))
					if meta, ok := doc["meta"].(map[string]interface{}); ok && len(meta) > 0 {
						fmt.Print("   Meta: ")
						metaParts := []string{}
						for k, v := range meta {
							metaParts = append(metaParts, fmt.Sprintf("%s=%v", k, v))
						}
						fmt.Println(strings.Join(metaParts, ", "))
					}
					fmt.Println()
				}
			}

			return nil
		},
	}
	searchCmd.Flags().StringP("filter", "f", "", "Filter by metadata: key=val1|val2,key2=val")
	searchCmd.Flags().StringP("sort", "S", "updatedAt", "Sort field: addedAt, updatedAt, key")
	searchCmd.Flags().BoolP("asc", "a", false, "Sort ascending (default: descending)")
	searchCmd.Flags().IntP("limit", "l", 50, "Limit results")
	searchCmd.Flags().IntP("offset", "o", 0, "Offset results")
	return searchCmd
}

func newFTSCmd() *cobra.Command {
	ftsCmd := &cobra.Command{
		Use:   "fts [collection]",
		Short: "Full-text search",
		Long:  `Search documents by text content using full-text search.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection := args[0]
			query, _ := cmd.Flags().GetString("query")
			limit, _ := cmd.Flags().GetInt("limit")

			if query == "" {
				return fmt.Errorf("--query flag is required")
			}

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"query":      query,
				"limit":      limit,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/fts", body)
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

				results, _ := result["results"].([]interface{})
				total, _ := result["total"].(float64)

				fmt.Printf("Full-Text Search Results (%d matches)\n", int(total))
				fmt.Printf("Query: %q\n", query)
				fmt.Printf("═══════════════════════════════════════\n\n")

				if len(results) == 0 {
					fmt.Println("No results found.")
				} else {
					for i, r := range results {
						item := asMap(r)
						doc := asMap(item["document"])
						score := asFloat(item["score"])
						terms, _ := item["matchedTerms"].([]interface{})

						fmt.Printf("%d. %.0f%%  %s (%s)\n", i+1, score*100, doc["key"], doc["lang"])
						if len(terms) > 0 {
							termStrs := make([]string, len(terms))
							for j, t := range terms {
								termStrs[j] = asString(t)
							}
							fmt.Printf("   Matched: %s\n", strings.Join(termStrs, ", "))
						}
						fmt.Println()
					}
				}
			}
			return nil
		},
	}
	ftsCmd.Flags().StringP("query", "q", "", "Search query (required)")
	ftsCmd.Flags().IntP("limit", "l", 50, "Max results")
	return ftsCmd
}

func newVectorSearchCmd() *cobra.Command {
	vectorSearchCmd := &cobra.Command{
		Use:   "vector-search [collection]",
		Short: "Semantic search using AI embeddings",
		Long:  `Search documents by meaning using vector similarity. Requires embedding provider configured on server.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection := args[0]
			query, _ := cmd.Flags().GetString("query")
			topK, _ := cmd.Flags().GetInt("top-k")
			threshold, _ := cmd.Flags().GetFloat64("threshold")
			metaStr, _ := cmd.Flags().GetString("filter")
			includeContent, _ := cmd.Flags().GetBool("include-content")

			if query == "" {
				return fmt.Errorf("--query flag is required")
			}

			filterMeta := parseMetaFlag(metaStr)

			client := newClient()
			body := map[string]interface{}{
				"collection":     collection,
				"query":          query,
				"topK":           topK,
				"threshold":      threshold,
				"includeContent": includeContent,
			}
			if len(filterMeta) > 0 {
				body["filterMeta"] = filterMeta
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/vector-search", body)
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

				results, _ := result["results"].([]interface{})
				model, _ := result["model"].(string)
				dims, _ := result["dimensions"].(float64)

				fmt.Printf("Vector Search Results (model: %s, dims: %d)\n", model, int(dims))
				fmt.Printf("Query: %q\n", query)
				fmt.Printf("═══════════════════════════════════════\n\n")

				if len(results) == 0 {
					fmt.Println("No results found.")
				} else {
					for _, r := range results {
						item := asMap(r)
						doc := asMap(item["document"])
						score := asFloat(item["score"])
						rank := int(asFloat(item["rank"]))

						fmt.Printf("#%d  %.0f%%  %s (%s)\n", rank, score*100, doc["key"], doc["lang"])
						if meta, ok := doc["meta"].(map[string]interface{}); ok && len(meta) > 0 {
							metaParts := []string{}
							for k, v := range meta {
								metaParts = append(metaParts, fmt.Sprintf("%s=%v", k, v))
							}
							fmt.Printf("     Meta: %s\n", strings.Join(metaParts, ", "))
						}
						if includeContent {
							if content := asString(doc["contentMd"]); content != "" {
								preview := shorten(content, 200)
								fmt.Printf("     Content: %s\n", strings.ReplaceAll(preview, "\n", " "))
							}
						}
						fmt.Println()
					}
				}
			}

			return nil
		},
	}
	vectorSearchCmd.Flags().StringP("query", "q", "", "Search query (required)")
	vectorSearchCmd.Flags().IntP("top-k", "k", 5, "Number of results")
	vectorSearchCmd.Flags().Float64P("threshold", "t", 0.0, "Minimum similarity score (0.0-1.0)")
	vectorSearchCmd.Flags().StringP("filter", "f", "", "Pre-filter by metadata: key=val1|val2,key2=val")
	vectorSearchCmd.Flags().BoolP("include-content", "c", false, "Include document content in results")
	return vectorSearchCmd
}
