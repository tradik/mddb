package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// What the commands over cron automations share — `backup schedule` (OPS-023)
// and `sync` (INT-022): the S3 flags, listing the rules of one action, and
// removing a rule only when it carries that action.

// s3ConfigFromFlags reads the S3 flags a backup schedule and a synced source
// share; the secret comes from secretEnv when --secret-key is not given.
func s3ConfigFromFlags(cmd *cobra.Command, secretEnv string) map[string]interface{} {
	f := cmd.Flags()
	str := func(name string) string { v, _ := f.GetString(name); return strings.TrimSpace(v) }
	secret := str("secret-key")
	if secret == "" {
		secret = os.Getenv(secretEnv)
	}
	useTLS, _ := f.GetBool("tls")
	return map[string]interface{}{
		"endpoint": str("endpoint"), "region": str("region"),
		"accessKey": str("access-key"), "secretKey": secret, "useTLS": useTLS,
	}
}

// listCronRules lists the automation rules matching q that carry action.
func listCronRules(q url.Values, action string) ([]map[string]interface{}, error) {
	resp, err := newClient().Do(context.Background(), "GET", "/v1/automation?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Rules []map[string]interface{} `json:"rules"`
	}
	if err := json.Unmarshal(resp, &list); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	out := []map[string]interface{}{}
	for _, r := range list.Rules {
		if r[action] != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// removeCronRule deletes rule id after checking it carries action, so the
// id of a webhook or another cron cannot be removed through the command.
func removeCronRule(id, action, what, done string) error {
	client := newClient()
	rulePath := "/v1/automation/" + url.PathEscape(id)
	resp, err := client.Do(context.Background(), "GET", rulePath, nil)
	if err != nil {
		return err
	}
	var rule map[string]interface{}
	if err := json.Unmarshal(resp, &rule); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if rule[action] == nil {
		return fmt.Errorf("%s is not %s (type %s)", id, what, asString(rule["type"]))
	}
	if resp, err = client.Do(context.Background(), "DELETE", rulePath, nil); err != nil {
		return err
	}
	if outputJSON {
		fmt.Println(string(resp))
		return nil
	}
	fmt.Printf("✓ %s: %s\n", done, id)
	return nil
}
