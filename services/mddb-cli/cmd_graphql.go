package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

// GraphQL query and playground.

func newGraphQLCmd() *cobra.Command {
	graphqlCmd := &cobra.Command{
		Use:   "graphql [query]",
		Short: "Execute a raw GraphQL query",
		Long: `Execute a GraphQL query against the MDDB server.
The query can be a query or mutation. Use quotes for complex queries.

Examples:
  mddb-cli graphql '{ stats { totalDocuments } }'
  mddb-cli graphql 'mutation { login(username: "admin", password: "secret") { token } }'
  mddb-cli graphql 'query { document(collection: "blog", key: "post", lang: "en") { contentMd } }'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			variables, _ := cmd.Flags().GetString("variables")

			client := newClient()

			body := map[string]interface{}{
				"query": query,
			}

			if variables != "" {
				var vars map[string]interface{}
				if err := json.Unmarshal([]byte(variables), &vars); err != nil {
					return fmt.Errorf("invalid variables JSON: %w", err)
				}
				body["variables"] = vars
			}

			resp, err := client.Do(context.Background(), "POST", "/graphql", body)
			if err != nil {
				return err
			}

			if outputJSON {
				fmt.Println(string(resp))
			} else {
				// Pretty print the JSON response
				var result map[string]interface{}
				if err := json.Unmarshal(resp, &result); err != nil {
					return fmt.Errorf("parse response: %w", err)
				}

				prettyJSON, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return fmt.Errorf("format response: %w", err)
				}
				fmt.Println(string(prettyJSON))
			}

			return nil
		},
	}
	graphqlCmd.Flags().StringP("variables", "V", "", "GraphQL variables as JSON string")
	return graphqlCmd
}

func newPlaygroundCmd() *cobra.Command {
	playgroundCmd := &cobra.Command{
		Use:   "playground",
		Short: "Open GraphQL Playground in browser",
		Long: `Open the GraphQL Playground in your default web browser.
The Playground provides an interactive GraphQL IDE for exploring the schema and running queries.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			playgroundURL := serverURL + "/playground"

			fmt.Printf("Opening GraphQL Playground at: %s\n", playgroundURL)

			// Try to open browser based on OS
			var openCmd string
			switch {
			case fileExists("/usr/bin/open"):
				openCmd = "open"
			case fileExists("/usr/bin/xdg-open"):
				openCmd = "xdg-open"
			case fileExists("/usr/bin/start"):
				openCmd = "start"
			default:
				fmt.Printf("\n⚠️  Could not detect browser command.\n")
				fmt.Printf("Please open manually: %s\n", playgroundURL)
				return nil
			}

			// #nosec G204 -- The command is a safe system launcher executable
			if err := exec.Command(openCmd, playgroundURL).Start(); err != nil {
				fmt.Printf("\n⚠️  Failed to open browser: %v\n", err)
				fmt.Printf("Please open manually: %s\n", playgroundURL)
				return nil
			}

			fmt.Println("✓ Browser opened")
			return nil
		},
	}

	return playgroundCmd
}
