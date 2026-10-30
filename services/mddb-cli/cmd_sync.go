package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// Synced sources (INT-022, #377): `sync create|list|run|delete`, a thin layer
// over the server's automations. A synced source is a cron rule whose action
// is a sync; everything here is a /v1/automation call.

// syncSecretKeyEnv supplies --secret-key when the flag is not given.
const syncSecretKeyEnv = "MDDB_SYNC_SECRET_KEY" // #nosec G101 -- the name of an environment variable, not a credential

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manage synced sources (sitemap, URL list, S3 prefix)",
		Long: `A synced source keeps a collection in step with a sitemap, a list of URLs
or the objects under an S3 prefix, on a cron schedule. Sources run when the
server has MDDB_CRONS=true; "sync run" runs one now.`,
	}
	cmd.AddCommand(newSyncCreateCmd(), newSyncListCmd(), newSyncRunCmd(), newSyncDeleteCmd())
	return cmd
}

func newSyncCreateCmd() *cobra.Command {
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a synced source",
		Example: `  mddb-cli sync create --name docs-site --cron "0 * * * *" --source sitemap \
    --url https://docs.example.com/sitemap.xml --collection docs --prune
  mddb-cli sync create --name kb --cron "*/30 * * * *" --source s3 --collection kb \
    --endpoint s3.example.com --tls --bucket kb --prefix articles/ --access-key AKIA...`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := syncRuleFromFlags(cmd)
			if err != nil {
				return err
			}
			resp, err := newClient().Do(context.Background(), "POST", "/v1/automation", rule)
			if err != nil {
				return err
			}
			if outputJSON {
				fmt.Println(string(resp))
				return nil
			}
			var created map[string]interface{}
			if err := json.Unmarshal(resp, &created); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			fmt.Printf("✓ Synced source created: %s\n", asString(created["id"]))
			fmt.Printf("  %s  %s → %s\n", asString(created["schedule"]), syncSource(asMap(created["sync"])),
				asString(asMap(created["sync"])["collection"]))
			return nil
		},
	}
	f := createCmd.Flags()
	f.String("name", "", "Rule name (required)")
	f.String("cron", "", `Cron expression, 5 fields or 6 with seconds first (required)`)
	f.String("source", "", "sitemap, urls or s3 (required)")
	f.String("collection", "", "Collection the documents go to (required)")
	f.String("lang", "en", "Language of the documents")
	f.String("url", "", "sitemap: the sitemap or sitemap index URL")
	f.String("urls", "", "urls: comma-separated page URLs")
	f.String("bucket", "", "s3: bucket")
	f.String("prefix", "", "s3: key prefix")
	f.String("endpoint", "", "s3: endpoint host[:port]")
	f.String("region", "", "s3: region")
	f.String("access-key", "", "s3: access key")
	f.String("secret-key", "", "s3: secret key (default $"+syncSecretKeyEnv+")")
	f.Bool("tls", false, "s3: connect over HTTPS")
	f.Bool("prune", false, "Delete the documents whose source is gone")
	f.Int("max-documents", 0, "Sources per run (0 = server default 1000)")
	f.Int64("max-bytes", 0, "Bytes per document (0 = server default 10 MiB)")
	f.Bool("disabled", false, "Create the rule disabled")
	return createCmd
}

// syncRuleFromFlags builds the automation rule create sends.
func syncRuleFromFlags(cmd *cobra.Command) (map[string]interface{}, error) {
	f := cmd.Flags()
	str := func(name string) string { v, _ := f.GetString(name); return strings.TrimSpace(v) }
	name, schedule, source, collection := str("name"), str("cron"), str("source"), str("collection")
	if name == "" || schedule == "" || source == "" || collection == "" {
		return nil, fmt.Errorf("--name, --cron, --source and --collection are required")
	}
	prune, _ := f.GetBool("prune")
	maxDocs, _ := f.GetInt("max-documents")
	maxBytes, _ := f.GetInt64("max-bytes")
	disabled, _ := f.GetBool("disabled")
	sync := map[string]interface{}{"source": source, "collection": collection, "lang": str("lang"),
		"prune": prune, "maxDocuments": maxDocs, "maxBytes": maxBytes}
	for flag, field := range map[string]string{"url": "url", "bucket": "bucket", "prefix": "prefix"} {
		if v := str(flag); v != "" {
			sync[field] = v
		}
	}
	if u := str("urls"); u != "" {
		sync["urls"] = strings.Split(u, ",")
	}
	if source == "s3" {
		sync["storageConfig"] = s3ConfigFromFlags(cmd, syncSecretKeyEnv)
	}
	return map[string]interface{}{
		"type": "cron", "name": name, "schedule": schedule, "enabled": !disabled, "sync": sync,
	}, nil
}

func newSyncListCmd() *cobra.Command {
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List synced sources and their last run",
		Example: `  mddb-cli sync list
  mddb-cli sync list --collection docs   # the sources feeding docs`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			collection, _ := cmd.Flags().GetString("collection")
			q := url.Values{"type": {"cron"}}
			if collection != "" {
				q.Set("collection", collection)
			}
			rules, err := listCronRules(q, "sync")
			if err != nil {
				return err
			}
			if outputJSON {
				out, _ := json.Marshal(rules)
				fmt.Println(string(out))
				return nil
			}
			if len(rules) == 0 {
				fmt.Println("No synced sources.")
				return nil
			}
			fmt.Printf("%-18s %-16s %-14s %-40s %-16s %-26s %s\n", "ID", "NAME", "SCHEDULE", "SOURCE", "COLLECTION", "LAST RUN", "LAST RESULT")
			for _, r := range rules {
				s := asMap(r["sync"])
				fmt.Printf("%-18s %-16s %-14s %-40s %-16s %-26s %s\n", asString(r["id"]), asString(r["name"]),
					asString(r["schedule"]), syncSource(s), asString(s["collection"]), formatUnix(r["lastRun"]),
					syncResult(asMap(r["lastResult"])))
			}
			return nil
		},
	}
	listCmd.Flags().String("collection", "", "Only the sources feeding this collection")
	return listCmd
}

// syncSource renders where a sync reads from; the server never sends the
// S3 secret.
func syncSource(s map[string]interface{}) string {
	switch asString(s["source"]) {
	case "sitemap":
		return asString(s["url"])
	case "urls":
		urls, _ := s["urls"].([]interface{})
		return fmt.Sprintf("%d urls", len(urls))
	}
	return "s3://" + asString(s["bucket"]) + "/" + asString(s["prefix"])
}

// syncResult renders a rule's last result: its status and counts, and the
// error when it failed.
func syncResult(r map[string]interface{}) string {
	if r == nil {
		return "-"
	}
	out := fmt.Sprintf("%s +%d ~%d =%d -%d !%d", asString(r["status"]), int(asFloat(r["added"])),
		int(asFloat(r["updated"])), int(asFloat(r["unchanged"])), int(asFloat(r["deleted"])), int(asFloat(r["failed"])))
	if e := asString(r["error"]); e != "" {
		out += ": " + e
	}
	return out
}

func newSyncRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run [id]",
		Short: "Run a synced source now",
		Long: `Runs the rule now, as its schedule would: logged in the automation log,
announced on the jobs stream (GET /v1/jobs/stream) and firing source.synced.
Fails while the previous run of the rule is still going.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := newClient().Do(context.Background(), "POST", "/v1/automation/"+url.PathEscape(args[0])+"/run", nil)
			if err != nil {
				return err
			}
			if outputJSON {
				fmt.Println(string(resp))
				return nil
			}
			var run map[string]interface{}
			if err := json.Unmarshal(resp, &run); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			fmt.Printf("✓ Run started: %s (job %s)\n", args[0], asString(run["jobId"]))
			return nil
		},
	}
}

func newSyncDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [id]",
		Short: "Delete a synced source (the documents it wrote stay)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return removeCronRule(args[0], "sync", "a synced source", "Synced source deleted")
		},
	}
}
