package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/spf13/cobra"
)

// Scheduled backups (OPS-023): `backup schedule add|list|remove`, a thin layer
// over the server's automations. A schedule is a cron rule whose action is a
// backup; everything here is a /v1/automation call.

// secretKeyEnv supplies --secret-key when the flag is not given, so the S3
// secret need not be typed where shell history keeps it.
const secretKeyEnv = "MDDB_BACKUP_SECRET_KEY" // #nosec G101 -- the name of an environment variable, not a credential

func newBackupScheduleCmd() *cobra.Command {
	scheduleCmd := &cobra.Command{
		Use:   "schedule",
		Short: "Manage scheduled backups",
		Long: `Scheduled backups are cron automations whose action is a backup, kept to
the newest --keep on a directory inside MDDB_BACKUP_DIR or in an
S3-compatible bucket. They run when the server has MDDB_CRONS=true.`,
	}
	scheduleCmd.AddCommand(newBackupScheduleAddCmd(), newBackupScheduleListCmd(), newBackupScheduleRemoveCmd())
	return scheduleCmd
}

func newBackupScheduleAddCmd() *cobra.Command {
	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Schedule a backup",
		Example: `  mddb-cli backup schedule add --name nightly --cron "0 3 * * *" --keep 7
  mddb-cli backup schedule add --name offsite --cron "0 4 * * *" --target s3 \
    --endpoint s3.eu-west-1.amazonaws.com --tls --bucket mddb-backups \
    --prefix prod/ --access-key AKIA... --keep 30   # secret from MDDB_BACKUP_SECRET_KEY`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := scheduleRuleFromFlags(cmd)
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
			fmt.Printf("✓ Backup schedule created: %s\n", asString(created["id"]))
			fmt.Printf("  %s  %s\n", asString(created["schedule"]), scheduleTarget(asMap(created["backup"])))
			return nil
		},
	}
	f := addCmd.Flags()
	f.String("name", "", "Schedule name (required)")
	f.String("cron", "", `Cron expression, 5 fields or 6 with seconds first (required), e.g. "0 3 * * *"`)
	f.String("target", "local", "Where backups go: local or s3")
	f.String("path", "", "local: directory inside MDDB_BACKUP_DIR")
	f.String("bucket", "", "s3: bucket")
	f.String("prefix", "", `Start of every backup's name (local) or key (s3); default "mddb-"`)
	f.Int("keep", 0, "How many backups to keep on the target; 0 keeps all")
	f.String("collections", "", "Comma-separated collections (per-collection layout only); default all")
	f.String("endpoint", "", "s3: endpoint host[:port]")
	f.String("region", "", "s3: region")
	f.String("access-key", "", "s3: access key")
	f.String("secret-key", "", "s3: secret key (default $"+secretKeyEnv+")")
	f.Bool("tls", false, "s3: connect over HTTPS")
	f.Bool("disabled", false, "Create the schedule disabled")
	return addCmd
}

// scheduleRuleFromFlags builds the automation rule add sends.
func scheduleRuleFromFlags(cmd *cobra.Command) (map[string]interface{}, error) {
	f := cmd.Flags()
	str := func(name string) string { v, _ := f.GetString(name); return strings.TrimSpace(v) }
	name, schedule := str("name"), str("cron")
	if name == "" || schedule == "" {
		return nil, fmt.Errorf("--name and --cron are required")
	}
	keep, _ := f.GetInt("keep")
	disabled, _ := f.GetBool("disabled")
	backup := map[string]interface{}{"target": str("target"), "keep": keep}
	for flag, field := range map[string]string{"path": "path", "bucket": "bucket", "prefix": "prefix"} {
		if v := str(flag); v != "" {
			backup[field] = v
		}
	}
	if c := str("collections"); c != "" {
		backup["collections"] = strings.Split(c, ",")
	}
	if str("target") == "s3" {
		backup["storageConfig"] = s3ConfigFromFlags(cmd, secretKeyEnv)
	}
	return map[string]interface{}{
		"type": "cron", "name": name, "schedule": schedule, "enabled": !disabled, "backup": backup,
	}, nil
}

func newBackupScheduleListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List scheduled backups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			schedules, err := listCronRules(url.Values{"type": {"cron"}}, "backup")
			if err != nil {
				return err
			}
			if outputJSON {
				out, _ := json.Marshal(schedules)
				fmt.Println(string(out))
				return nil
			}
			if len(schedules) == 0 {
				fmt.Println("No scheduled backups.")
				return nil
			}
			fmt.Printf("%-18s %-16s %-14s %-40s %5s %-8s %s\n", "ID", "NAME", "SCHEDULE", "TARGET", "KEEP", "ENABLED", "LAST RUN")
			for _, r := range schedules {
				b := asMap(r["backup"])
				enabled, _ := r["enabled"].(bool)
				fmt.Printf("%-18s %-16s %-14s %-40s %5d %-8t %s\n", asString(r["id"]), asString(r["name"]),
					asString(r["schedule"]), scheduleTarget(b), int(asFloat(b["keep"])), enabled, formatUnix(r["lastRun"]))
			}
			return nil
		},
	}
}

// scheduleTarget renders where a schedule's backups go.
func scheduleTarget(b map[string]interface{}) string {
	prefix := asString(b["prefix"])
	if asString(b["target"]) == "s3" {
		return "s3://" + asString(b["bucket"]) + "/" + prefix
	}
	return "local:" + path.Join(asString(b["path"]), prefix)
}

func newBackupScheduleRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [id]",
		Short: "Remove a scheduled backup (the backups it took stay)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return removeCronRule(args[0], "backup", "a scheduled backup", "Backup schedule removed")
		},
	}
}
