package main

import (
	"github.com/spf13/cobra"
)

// Global flags. They are package-level because cobra binds them once per
// command tree; newRootCmd resets them so a second tree — which only tests
// build — starts from the documented defaults rather than the previous run's.
var (
	serverURL  string
	outputJSON bool
	verbose    bool
	apiKey     string // API key for authentication
	token      string // JWT token for authentication
)

// defaultServerURL is where the CLI looks for a server when told nothing else.
const defaultServerURL = "http://localhost:11023"

// newRootCmd builds the whole command tree.
func newRootCmd() *cobra.Command {
	serverURL, outputJSON, verbose, apiKey, token = defaultServerURL, false, false, "", ""

	rootCmd := &cobra.Command{
		Use:   "mddb-cli",
		Short: "MDDB command-line client",
		Long: `mddb-cli is a command-line client for MDDB (Markdown Database).
It provides an interface similar to mysql-client for managing markdown documents.`,
		Version: "1.0.0",
	}

	rootCmd.PersistentFlags().StringVarP(&serverURL, "server", "s", defaultServerURL, "MDDB server URL")
	rootCmd.PersistentFlags().BoolVarP(&outputJSON, "json", "j", false, "Output raw JSON")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Verbose output")
	rootCmd.PersistentFlags().StringVar(&apiKey, "api-key", "", "API key for authentication")
	rootCmd.PersistentFlags().StringVar(&token, "token", "", "JWT token for authentication")

	rootCmd.AddCommand(
		newAddCmd(), newGetCmd(), newImportURLCmd(), newSetTTLCmd(),
		newSearchCmd(), newFTSCmd(), newVectorSearchCmd(),
		newVectorReindexCmd(), newVectorStatsCmd(),
		newExportCmd(), newBackupCmd(), newRestoreCmd(), newTruncateCmd(), newStatsCmd(),
		newWebhookCmd(), newSchemaCmd(), newValidateCmd(),
		newLoginCmd(), newAPIKeyCmd(),
		newGraphQLCmd(), newPlaygroundCmd(),
	)

	return rootCmd
}
