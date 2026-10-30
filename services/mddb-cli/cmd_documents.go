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
Reads content from stdin or file.

A frontmatter block (between --- lines at the top) is read as /v1/upload reads
it: its keys become meta (comma, [flow] and "- item" block lists become lists),
the document is stored without the block, and --meta wins for a key both name.
--no-frontmatter stores the content as it is.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, key, lang := args[0], args[1], args[2]

			contentFile, _ := cmd.Flags().GetString("file")
			metaStr, _ := cmd.Flags().GetString("meta")
			noFrontmatter, _ := cmd.Flags().GetBool("no-frontmatter")

			content, err := readAddContent(contentFile)
			if err != nil {
				return err
			}
			meta := parseMetaFlag(metaStr)
			if !noFrontmatter {
				content, meta = withFrontmatter(content, meta)
			}

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
	addCmd.Flags().Bool("no-frontmatter", false, "Store the content as it is; do not read frontmatter as meta")
	return addCmd
}

// readAddContent is the document add sends: the file when one is named,
// stdin otherwise.
func readAddContent(contentFile string) (string, error) {
	if contentFile == "" {
		data, err := io.ReadAll(os.Stdin)
		return string(data), err
	}
	// #nosec G304 -- User supplied filename intended to be read literally
	data, err := os.ReadFile(filepath.Clean(contentFile))
	return string(data), err
}

// withFrontmatter takes the frontmatter off content (#373) and returns the
// body with the meta to send: the frontmatter's keys, overridden by the
// --meta ones, as /v1/upload merges them. Content without a frontmatter
// block comes back unchanged.
func withFrontmatter(content string, meta map[string][]string) (string, map[string][]string) {
	fmMeta, body := parseFrontmatter(content)
	if fmMeta == nil {
		return content, meta
	}
	for k, v := range meta {
		fmMeta[k] = v
	}
	return body, fmMeta
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

			return printGetResponse(resp, contentOnly)
		},
	}
	getCmd.Flags().StringP("env", "e", "", "Environment variables for templating: key=val,key2=val2")
	getCmd.Flags().BoolP("content-only", "c", false, "Output only content (no metadata)")
	return getCmd
}

// printGetResponse prints what get returned: the content alone with
// --content-only, the raw JSON with --json, the document otherwise.
func printGetResponse(resp []byte, contentOnly bool) error {
	if outputJSON && !contentOnly {
		fmt.Println(string(resp))
		return nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(resp, &doc); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if contentOnly {
		fmt.Print(doc["contentMd"])
		return nil
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
	return nil
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

			return printImportURLResponse(resp)
		},
	}
	importURLCmd.Flags().String("key", "", "Document key (auto-derived from URL if empty)")
	importURLCmd.Flags().StringP("meta", "m", "", "Metadata: key=val1|val2,key2=val")
	importURLCmd.Flags().Int64("ttl", 0, "TTL in seconds (0 = no expiry)")
	return importURLCmd
}

// printImportURLResponse prints the imported document's id, or the raw
// JSON with --json.
func printImportURLResponse(resp []byte) error {
	if outputJSON {
		fmt.Println(string(resp))
		return nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(resp, &doc); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	fmt.Printf("Document imported from URL: %s\n", doc["id"])
	return nil
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
