package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// progress writes the note a finished row leaves for the next session:
// progress/<date>-<slug>, with the row's issues and release and the commit.
// A second note for the same row on the same day replaces the first.
func (s planStore) progress(ctx context.Context, row mddb.Document, note string, now time.Time) (*mddb.Document, error) {
	meta := map[string][]string{"kind": {kindProgress}}
	for _, k := range []string{"issues", "release", "commit"} {
		if v := row.Meta[k]; len(v) > 0 {
			meta[k] = v
		}
	}
	key := "progress/" + now.UTC().Format("2006-01-02") + "-" + rowSlug(row)
	return s.client.Add(ctx, mddb.AddRequest{Collection: s.collection, Key: key, Lang: s.lang, Meta: meta, ContentMD: note})
}

// importTable adds the rows of the first table in text after the rows the
// backlog has, and reports how many were added and how many were there.
func (s planStore) importTable(ctx context.Context, text string) (added, existing int, err error) {
	t, err := parseTable(text)
	if err != nil {
		return 0, 0, err
	}
	order, err := s.nextOrder(ctx)
	if err != nil {
		return 0, 0, err
	}
	rows, err := importRows(t, order)
	if err != nil {
		return 0, 0, err
	}
	for _, r := range rows {
		_, isNew, err := s.add(ctx, r, false)
		if err != nil {
			return added, existing, fmt.Errorf("%s: %w", r.slug, err)
		}
		if isNew {
			added++
		} else {
			existing++
		}
	}
	return added, existing, nil
}

// printPlanJSON prints v as the --json output of a plan command.
func printPlanJSON(v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// planPromptWidth is how much of a prompt one line of plan list shows.
const planPromptWidth = 60

// printPlanRows prints rows as a table, one line each.
func printPlanRows(collection string, docs []mddb.Document) {
	if len(docs) == 0 {
		fmt.Printf("No rows in %s\n", collection)
		return
	}
	fmt.Printf("%-5s %-6s %-4s %-32s %s\n", "Order", "Status", "Size", "Slug", "Prompt")
	for _, d := range docs {
		prompt := strings.Join(strings.Fields(d.ContentMD), " ")
		fmt.Printf("%-5s %-6s %-4s %-32s %s\n", metaFirst(d, "order"), metaFirst(d, "status"),
			orDash(metaFirst(d, "size")), rowSlug(d), shorten(prompt, planPromptWidth))
	}
}
