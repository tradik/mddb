package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// Collection schemas and metadata validation.

func newSchemaCmd() *cobra.Command {
	schemaCmd := &cobra.Command{
		Use:   "schema",
		Short: "Manage collection schemas",
		Long:  `Set, get, delete, and list JSON schemas for collection validation.`,
	}

	schemaSetCmd := &cobra.Command{
		Use:   "set",
		Short: "Set schema for a collection",
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, _ := cmd.Flags().GetString("collection")
			schema, _ := cmd.Flags().GetString("schema")

			if collection == "" || schema == "" {
				return fmt.Errorf("--collection and --schema flags are required")
			}

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"schema":     schema,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/schema/set", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				fmt.Printf("Schema set for collection: %s\n", collection)
			}
			return nil
		},
	}
	schemaSetCmd.Flags().StringP("collection", "c", "", "Collection name (required)")
	// No "-s" shorthand: the root command already uses it for --server, and
	// pflag panics when a subcommand redefines an inherited shorthand — every
	// invocation of "schema set", including --help, crashed with a stack
	// trace, so nothing can depend on the old spelling.
	schemaSetCmd.Flags().String("schema", "", "JSON schema string (required)")

	schemaGetCmd := &cobra.Command{
		Use:   "get",
		Short: "Get schema for a collection",
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, _ := cmd.Flags().GetString("collection")

			if collection == "" {
				return fmt.Errorf("--collection flag is required")
			}

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/schema/get", body)
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
				if schema, ok := result["schema"].(string); ok {
					fmt.Printf("Schema for collection %q:\n%s\n", collection, schema)
				} else {
					// Pretty-print the whole response
					pretty, _ := json.MarshalIndent(result, "", "  ")
					fmt.Printf("Schema for collection %q:\n%s\n", collection, string(pretty))
				}
			}
			return nil
		},
	}
	schemaGetCmd.Flags().StringP("collection", "c", "", "Collection name (required)")

	schemaDeleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete schema for a collection",
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, _ := cmd.Flags().GetString("collection")

			if collection == "" {
				return fmt.Errorf("--collection flag is required")
			}

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/schema/delete", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				fmt.Printf("Schema deleted for collection: %s\n", collection)
			}
			return nil
		},
	}
	schemaDeleteCmd.Flags().StringP("collection", "c", "", "Collection name (required)")

	schemaListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all schemas",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			body := map[string]interface{}{}

			resp, err := client.Do(context.Background(), "POST", "/v1/schema/list", body)
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

				if schemas, ok := result["schemas"].(map[string]interface{}); ok && len(schemas) > 0 {
					fmt.Printf("Schemas:\n")
					fmt.Printf("─────────────────────────────────────────\n")
					for name, schema := range schemas {
						fmt.Printf("%-20s %v\n", name, schema)
					}
				} else if schemas, ok := result["schemas"].([]interface{}); ok && len(schemas) > 0 {
					fmt.Printf("Schemas:\n")
					fmt.Printf("─────────────────────────────────────────\n")
					for _, s := range schemas {
						item := asMap(s)
						fmt.Printf("%-20s %v\n", item["collection"], item["schema"])
					}
				} else {
					fmt.Println("No schemas found.")
				}
			}
			return nil
		},
	}

	schemaCmd.AddCommand(schemaSetCmd, schemaGetCmd, schemaDeleteCmd, schemaListCmd)
	return schemaCmd
}

func newValidateCmd() *cobra.Command {
	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate document metadata against schema",
		Long:  `Validate document metadata against the JSON schema defined for a collection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, _ := cmd.Flags().GetString("collection")
			metaStr, _ := cmd.Flags().GetString("meta")

			if collection == "" || metaStr == "" {
				return fmt.Errorf("--collection and --meta flags are required")
			}

			var meta interface{}
			if err := json.Unmarshal([]byte(metaStr), &meta); err != nil {
				return fmt.Errorf("invalid JSON for --meta: %w", err)
			}

			client := newClient()
			body := map[string]interface{}{
				"collection": collection,
				"meta":       meta,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/validate", body)
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

				if valid, ok := result["valid"].(bool); ok && valid {
					fmt.Printf("Metadata is valid for collection: %s\n", collection)
				} else {
					fmt.Printf("Validation failed for collection: %s\n", collection)
					if errors, ok := result["errors"].([]interface{}); ok {
						for _, e := range errors {
							fmt.Printf("  - %v\n", e)
						}
					}
					if msg, ok := result["error"].(string); ok {
						fmt.Printf("  Error: %s\n", msg)
					}
				}
			}
			return nil
		},
	}
	validateCmd.Flags().StringP("collection", "c", "", "Collection name (required)")
	validateCmd.Flags().StringP("meta", "m", "", "Metadata as JSON string (required)")
	return validateCmd
}
