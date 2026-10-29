package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// mddb-cli reindex-swap (GO-044): blue/green for a collection. Build a new
// collection from the one an alias points at — with the new embedding model
// or chunking configured on it — wait until it is indexed, then move the
// alias. The old collection stays, for a rollback with `alias set`.

// swapBatch is how many documents go in one ingest request, and
// swapMaxLine the longest exported document (variables for tests).
var (
	swapBatch   = 100
	swapMaxLine = 64 << 20
)

// swapPoll is how often the indexing status is asked while waiting.
var swapPoll = 2 * time.Second

func newReindexSwapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reindex-swap <alias> --into <collection>",
		Short: "Rebuild an aliased collection into a new one, then move the alias",
		Long: `Copies every document of the collection <alias> points at into --into, which
should already carry the new configuration (embedding model, chunking); without
one, the old collection's configuration is copied. Waits until --into is fully
indexed, then moves the alias in one step. The old collection is kept.`,
		Args: cobra.ExactArgs(1),
		RunE: runReindexSwap,
	}
	cmd.Flags().String("into", "", "The collection to build and switch to (required)")
	cmd.Flags().Duration("timeout", 30*time.Minute, "How long to wait for the new collection to be indexed")
	_ = cmd.MarkFlagRequired("into")
	return cmd
}

func runReindexSwap(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	alias := args[0]
	into, _ := cmd.Flags().GetString("into")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	client := newClient()

	from, err := aliasTarget(ctx, client, alias)
	if err != nil {
		return err
	}
	if from == into {
		return fmt.Errorf("%s already points at %s", alias, into)
	}
	if err := copyConfigIfMissing(ctx, client, from, into); err != nil {
		return err
	}
	n, err := copyDocuments(ctx, client, from, into)
	if err != nil {
		return err
	}
	fmt.Printf("Copied %d documents from %s to %s\n", n, from, into)
	if err := waitIndexed(ctx, client, into, timeout); err != nil {
		return err
	}
	if err := client.SetAlias(ctx, alias, into); err != nil {
		return err
	}
	fmt.Printf("%s -> %s (was %s; it is kept, roll back with: mddb-cli alias set %s %s)\n", alias, into, from, alias, from)
	return nil
}

// aliasTarget is the collection an alias points at.
func aliasTarget(ctx context.Context, client *mddb.Client, alias string) (string, error) {
	aliases, err := client.Aliases(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range aliases {
		if a.Alias == alias {
			return a.Collection, nil
		}
	}
	return "", fmt.Errorf("%q is not an alias; create it with: mddb-cli alias set %s <collection>", alias, alias)
}

// copyConfigIfMissing gives into the old collection's settings unless it has
// its own. Masked credentials (S3, WordPress) are not readable and are not
// copied: configure such a collection before the swap.
func copyConfigIfMissing(ctx context.Context, client *mddb.Client, from, into string) error {
	if cur, err := client.GetCollectionConfig(ctx, into); err != nil || cur.Configured {
		return err
	}
	raw, err := client.Do(ctx, "GET", "/v1/collection-config?collection="+url.QueryEscape(from), nil)
	if err != nil {
		return err
	}
	var got struct {
		Config     map[string]any `json:"config"`
		Configured bool           `json:"configured"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || !got.Configured {
		return err
	}
	got.Config["collection"] = into
	_, err = client.Do(ctx, "PUT", "/v1/collection-config", got.Config)
	return err
}

// exportedDoc is one line of an NDJSON export.
type exportedDoc struct {
	Key       string              `json:"key"`
	Lang      string              `json:"lang"`
	Meta      map[string][]string `json:"meta"`
	ContentMD string              `json:"contentMd"`
	ExpiresAt int64               `json:"expiresAt"`
}

// copyDocuments exports from and ingests the documents into into, keeping
// what is left of each document's time to live.
func copyDocuments(ctx context.Context, client *mddb.Client, from, into string) (int, error) {
	data, err := client.Export(ctx, map[string]string{"collection": from, "format": "ndjson"})
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, min(1<<20, swapMaxLine)), swapMaxLine)
	var batch []mddb.IngestDocument
	n := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		resp, err := client.Ingest(ctx, mddb.IngestRequest{Collection: into, Documents: batch})
		if err != nil {
			return err
		}
		if resp.Failed > 0 {
			return fmt.Errorf("%d documents failed to copy into %s", resp.Failed, into)
		}
		n += len(batch)
		batch = batch[:0]
		return nil
	}
	now := time.Now().Unix()
	for sc.Scan() {
		var d exportedDoc
		if err := json.Unmarshal(sc.Bytes(), &d); err != nil {
			return n, fmt.Errorf("reading the export: %w", err)
		}
		doc := mddb.IngestDocument{Key: d.Key, Lang: d.Lang, Meta: d.Meta, ContentMD: d.ContentMD}
		if d.ExpiresAt > 0 {
			doc.TTL = max(d.ExpiresAt-now, 1)
		}
		if batch = append(batch, doc); len(batch) == swapBatch {
			if err := flush(); err != nil {
				return n, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return n, err
	}
	return n, flush()
}

// waitIndexed polls the indexing status until the collection is indexed.
func waitIndexed(ctx context.Context, client *mddb.Client, collection string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := client.IndexingStatus(ctx, collection)
		if err != nil {
			return err
		}
		if st.Indexed {
			return nil
		}
		if st.FailedEmbeddings > 0 {
			return fmt.Errorf("%s: %d embeddings failed; the alias was not moved", collection, st.FailedEmbeddings)
		}
		if time.Now().After(deadline) {
			return errors.New(collection + " is not indexed yet; the alias was not moved (raise --timeout)")
		}
		fmt.Printf("Waiting for %s: %.0f%% embedded\n", collection, 100*st.Progress)
		time.Sleep(swapPoll)
	}
}
