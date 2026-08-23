package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// Authentication: JWT login and API keys.

func newLoginCmd() *cobra.Command {
	loginCmd := &cobra.Command{
		Use:   "login [username] [password]",
		Short: "Login and get JWT token",
		Long:  `Authenticate with MDDB server using username and password, receive JWT token.`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			username, password := args[0], args[1]

			client := newClient()
			body := map[string]interface{}{
				"username": username,
				"password": password,
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/auth/login", body)
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var result map[string]interface{}
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}

				tokenStr, _ := result["token"].(string)
				expiresAt, _ := result["expiresAt"].(float64)

				fmt.Printf("✓ Login successful\n")
				fmt.Printf("Token: %s\n", tokenStr)
				fmt.Printf("Expires: %v\n", time.Unix(int64(expiresAt), 0).Format(time.RFC3339))
				fmt.Printf("\nUse with: mddb-cli --token %s <command>\n", tokenStr)
			}
			return nil
		},
	}
	return loginCmd
}

func newAPIKeyCmd() *cobra.Command {
	apiKeyCmd := &cobra.Command{
		Use:   "api-key",
		Short: "Manage API keys",
		Long:  `Create, list, and delete API keys for programmatic access.`,
	}

	apiKeyCreateCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new API key",
		Long:  `Create a new API key. Requires JWT authentication (--token).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			description, _ := cmd.Flags().GetString("description")
			expiresAt, _ := cmd.Flags().GetInt64("expires-at")

			if token == "" {
				return fmt.Errorf("authentication required: use --token flag or mddb-cli login first")
			}

			client := newClient()
			body := map[string]interface{}{
				"description": description,
			}
			if expiresAt > 0 {
				body["expiresAt"] = expiresAt
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/auth/api-key", body)
			if err != nil {
				return fmt.Errorf("failed to create API key: %w", err)
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var result map[string]interface{}
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}

				key, _ := result["key"].(string)
				desc, _ := result["description"].(string)
				createdAt, _ := result["createdAt"].(float64)

				fmt.Printf("✓ API Key created successfully\n")
				fmt.Printf("Key:         %s\n", key)
				fmt.Printf("Description: %s\n", desc)
				fmt.Printf("Created:     %v\n", time.Unix(int64(createdAt), 0).Format(time.RFC3339))

				if exp, ok := result["expiresAt"].(float64); ok && exp > 0 {
					fmt.Printf("Expires:     %v\n", time.Unix(int64(exp), 0).Format(time.RFC3339))
				} else {
					fmt.Printf("Expires:     Never\n")
				}

				fmt.Printf("\n⚠️  IMPORTANT: Save this key now! You won't be able to see it again.\n")
				fmt.Printf("Use with: mddb-cli --api-key %s <command>\n", key)
			}
			return nil
		},
	}
	apiKeyCreateCmd.Flags().StringP("description", "d", "", "API key description/label")
	apiKeyCreateCmd.Flags().Int64P("expires-at", "e", 0, "Expiry timestamp (Unix epoch, 0 = never)")

	apiKeyListCmd := &cobra.Command{
		Use:   "list",
		Short: "List your API keys",
		Long:  `List all API keys for the authenticated user. Requires JWT authentication (--token).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return fmt.Errorf("authentication required: use --token flag or mddb-cli login first")
			}

			client := newClient()
			resp, err := client.Do(context.Background(), "GET", "/v1/auth/api-keys", nil)
			if err != nil {
				return fmt.Errorf("failed to list API keys: %w", err)
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var result map[string]interface{}
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}

				keys, _ := result["keys"].([]interface{})
				if len(keys) == 0 {
					fmt.Println("No API keys found.")
					fmt.Println("Create one with: mddb-cli api-key create --token <token>")
				} else {
					fmt.Printf("Your API Keys (%d total)\n", len(keys))
					fmt.Printf("═══════════════════════════════════════\n\n")
					for i, k := range keys {
						item := asMap(k)
						keyHash, _ := item["keyHash"].(string)
						desc, _ := item["description"].(string)
						createdAt, _ := item["createdAt"].(float64)
						expiresAt, _ := item["expiresAt"].(float64)

						fmt.Printf("%d. Key Hash: %s\n", i+1, shorten(keyHash, 16))
						if desc != "" {
							fmt.Printf("   Description: %s\n", desc)
						}
						fmt.Printf("   Created: %v\n", time.Unix(int64(createdAt), 0).Format(time.RFC3339))
						if expiresAt > 0 {
							fmt.Printf("   Expires: %v\n", time.Unix(int64(expiresAt), 0).Format(time.RFC3339))
						} else {
							fmt.Printf("   Expires: Never\n")
						}
						fmt.Printf("   Delete with: mddb-cli api-key delete %s --token <token>\n", keyHash)
						fmt.Println()
					}
				}
			}
			return nil
		},
	}

	apiKeyDeleteCmd := &cobra.Command{
		Use:   "delete [key-hash]",
		Short: "Delete an API key",
		Long:  `Delete an API key by its hash. Requires JWT authentication (--token).`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keyHash := args[0]

			if token == "" {
				return fmt.Errorf("authentication required: use --token flag or mddb-cli login first")
			}

			client := newClient()
			resp, err := client.Do(context.Background(), "DELETE", "/v1/auth/api-keys/"+keyHash, nil)
			if err != nil {
				return fmt.Errorf("failed to delete API key: %w", err)
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				fmt.Printf("✓ API key deleted: %s\n", shorten(keyHash, 16))
			}
			return nil
		},
	}

	apiKeyCmd.AddCommand(apiKeyCreateCmd, apiKeyListCmd, apiKeyDeleteCmd)
	return apiKeyCmd
}
