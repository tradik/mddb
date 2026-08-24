package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Webhook registration.

func newWebhookCmd() *cobra.Command {
	webhookCmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage webhooks",
		Long:  `Register, list, and delete webhook subscriptions.`,
	}

	webhookRegisterCmd := &cobra.Command{
		Use:   "register",
		Short: "Register a webhook",
		RunE: func(cmd *cobra.Command, args []string) error {
			url, _ := cmd.Flags().GetString("url")
			eventsStr, _ := cmd.Flags().GetString("events")
			collection, _ := cmd.Flags().GetString("collection")

			if url == "" || eventsStr == "" {
				return fmt.Errorf("--url and --events flags are required")
			}

			events := strings.Split(eventsStr, ",")

			client := newClient()
			body := map[string]interface{}{
				"url":    url,
				"events": events,
			}
			if collection != "" {
				body["collection"] = collection
			}

			resp, err := client.Do(context.Background(), "POST", "/v1/webhooks", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var wh map[string]interface{}
				if err := json.Unmarshal(resp, &wh); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				fmt.Printf("Webhook registered: %s\n", wh["id"])
				fmt.Printf("  URL: %s\n", wh["url"])
				fmt.Printf("  Events: %v\n", wh["events"])
			}
			return nil
		},
	}
	webhookRegisterCmd.Flags().String("url", "", "Webhook endpoint URL (required)")
	webhookRegisterCmd.Flags().String("events", "", "Events: doc.added,doc.updated,doc.deleted (required)")
	webhookRegisterCmd.Flags().String("collection", "", "Filter to collection (optional)")

	webhookListCmd := &cobra.Command{
		Use:   "list",
		Short: "List webhooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newClient()
			resp, err := client.Do(context.Background(), "GET", "/v1/webhooks", nil)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				var hooks []map[string]interface{}
				if err := json.Unmarshal(resp, &hooks); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}
				if len(hooks) == 0 {
					fmt.Println("No webhooks registered.")
				} else {
					fmt.Printf("%-20s %-40s %-30s %s\n", "ID", "URL", "Events", "Collection")
					fmt.Printf("%-20s %-40s %-30s %s\n", "──────────────────", "────────────────────────────────────────", "──────────────────────────────", "──────────")
					for _, h := range hooks {
						coll := ""
						if c, ok := h["collection"].(string); ok {
							coll = c
						}
						fmt.Printf("%-20s %-40s %-30v %s\n", h["id"], h["url"], h["events"], coll)
					}
				}
			}
			return nil
		},
	}

	webhookDeleteCmd := &cobra.Command{
		Use:   "delete [id]",
		Short: "Delete a webhook",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			client := newClient()
			body := map[string]interface{}{"id": id}
			resp, err := client.Do(context.Background(), "POST", "/v1/webhooks/delete", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				fmt.Printf("Webhook deleted: %s\n", id)
			}
			return nil
		},
	}

	webhookCmd.AddCommand(webhookRegisterCmd, webhookListCmd, webhookDeleteCmd)
	return webhookCmd
}
