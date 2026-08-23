package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Commands that read and write single documents.

func newAddCmd() *cobra.Command {
	addCmd := &cobra.Command{
		Use:   "add [collection] [key] [lang]",
		Short: "Add or update a document",
		Long: `Add or update a markdown document in the database.
Reads content from stdin or file.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, key, lang := args[0], args[1], args[2]

			contentFile, _ := cmd.Flags().GetString("file")
			metaStr, _ := cmd.Flags().GetString("meta")

			var content string
			if contentFile != "" {
				// #nosec G304 -- User supplied filename intended to be read literally
				data, err := os.ReadFile(filepath.Clean(contentFile))
				if err != nil {
					return err
				}
				content = string(data)
			} else {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				content = string(data)
			}

			meta := parseMetaFlag(metaStr)

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"key":        key,
				"lang":       lang,
				"meta":       meta,
				"contentMd":  content,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/add", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var doc map[string]interface{}
				if err := json.Unmarshal(resp, &doc); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Printf("✓ Document added: %s\n", doc["id"])
				fmt.Printf("  Added: %v\n", formatUnix(doc["addedAt"]))
				fmt.Printf("  Updated: %v\n", formatUnix(doc["updatedAt"]))
			}

			return nil
		},
	}
	addCmd.Flags().StringP("file", "f", "", "Read content from file instead of stdin")
	addCmd.Flags().StringP("meta", "m", "", "Metadata in format: key=val1|val2,key2=val")
	return addCmd
}

func newGetCmd() *cobra.Command {
	getCmd := &cobra.Command{
		Use:   "get [collection] [key] [lang]",
		Short: "Get a document",
		Long:  `Retrieve a document from the database.`,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, key, lang := args[0], args[1], args[2]
			envStr, _ := cmd.Flags().GetString("env")
			contentOnly, _ := cmd.Flags().GetBool("content-only")

			env := parseEnvFlag(envStr)

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"key":        key,
				"lang":       lang,
				"env":        env,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/get", body)
			if err != nil {
				return err
			}

			if contentOnly {
				var doc map[string]interface{}
				if err := json.Unmarshal(resp, &doc); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Print(doc["contentMd"])
			} else if outputJSON {
				fmt.Println(string(resp))
			} else {
				var doc map[string]interface{}
				if err := json.Unmarshal(resp, &doc); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Printf("ID: %s\n", doc["id"])
				fmt.Printf("Key: %s\n", doc["key"])
				fmt.Printf("Lang: %s\n", doc["lang"])
				fmt.Printf("Added: %v\n", formatUnix(doc["addedAt"]))
				fmt.Printf("Updated: %v\n", formatUnix(doc["updatedAt"]))
				if meta, ok := doc["meta"].(map[string]interface{}); ok && len(meta) > 0 {
					fmt.Println("Meta:")
					for k, v := range meta {
						fmt.Printf("  %s: %v\n", k, v)
					}
				}
				fmt.Println("\nContent:")
				fmt.Println(strings.Repeat("-", 80))
				fmt.Println(doc["contentMd"])
			}

			return nil
		},
	}
	getCmd.Flags().StringP("env", "e", "", "Environment variables for templating: key=val,key2=val2")
	getCmd.Flags().BoolP("content-only", "c", false, "Output only content (no metadata)")
	return getCmd
}

func newImportURLCmd() *cobra.Command {
	importURLCmd := &cobra.Command{
		Use:   "import-url [collection] [url] [lang]",
		Short: "Import a document from URL",
		Long:  `Import a markdown document from a URL. YAML frontmatter is parsed as metadata.`,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, url, lang := args[0], args[1], args[2]
			key, _ := cmd.Flags().GetString("key")
			metaStr, _ := cmd.Flags().GetString("meta")
			ttl, _ := cmd.Flags().GetInt64("ttl")

			meta := parseMetaFlag(metaStr)

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"url":        url,
				"lang":       lang,
			}
			if key != "" {
				body["key"] = key
			}
			if len(meta) > 0 {
				body["meta"] = meta
			}
			if ttl > 0 {
				body["ttl"] = ttl
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/import-url", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var doc map[string]interface{}
				if err := json.Unmarshal(resp, &doc); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Printf("Document imported from URL: %s\n", doc["id"])
			}
			return nil
		},
	}
	importURLCmd.Flags().String("key", "", "Document key (auto-derived from URL if empty)")
	importURLCmd.Flags().StringP("meta", "m", "", "Metadata: key=val1|val2,key2=val")
	importURLCmd.Flags().Int64("ttl", 0, "TTL in seconds (0 = no expiry)")
	return importURLCmd
}

func newSetTTLCmd() *cobra.Command {
	setTTLCmd := &cobra.Command{
		Use:   "set-ttl [collection] [key] [lang]",
		Short: "Set TTL on a document",
		Long:  `Set or remove time-to-live on a document. Use --ttl=0 to remove TTL.`,
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, key, lang := args[0], args[1], args[2]
			ttl, _ := cmd.Flags().GetInt64("ttl")

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"key":        key,
				"lang":       lang,
				"ttl":        ttl,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/set-ttl", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				if ttl > 0 {
					fmt.Printf("TTL set to %d seconds for %s/%s/%s\n", ttl, collection, key, lang)
				} else {
					fmt.Printf("TTL removed for %s/%s/%s\n", collection, key, lang)
				}
			}
			return nil
		},
	}
	setTTLCmd.Flags().Int64("ttl", 0, "TTL in seconds (0 = remove)")
	return setTTLCmd
}
