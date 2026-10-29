package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// mddb-cli reference (AGT-003): add, list, update, remove and search the
// reference repositories an agent learns from.

func newReferenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reference",
		Short: "Index other projects' code to learn from (add, list, update, remove, search)",
	}
	cmd.AddCommand(newReferenceAddCmd(), newReferenceListCmd(), newReferenceUpdateCmd(),
		newReferenceRemoveCmd(), newReferenceSearchCmd())
	return cmd
}

func newReferenceAddCmd() *cobra.Command {
	var ref, name, lang string
	cmd := &cobra.Command{
		Use:   "add <repository>",
		Short: "Clone a repository (shallow) and index it as a reference collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url, err := normalizeRepoURL(args[0])
			if err != nil {
				return err
			}
			if name == "" {
				name = referenceName(url)
			}
			dir, err := referenceDir(name)
			if err != nil {
				return err
			}
			if _, err := os.Stat(dir); err == nil {
				return fmt.Errorf("reference %s already exists in %s: use `mddb-cli reference update %s`", name, dir, name)
			}
			if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
				return err
			}
			if err := gitClone(url, ref, dir); err != nil {
				return fmt.Errorf("clone %s: %w", url, err)
			}
			st := referenceState{URL: url, Ref: ref, Collection: name, License: detectLicense(dir)}
			if err := writeReferenceState(dir, st); err != nil {
				return err
			}
			return indexReference(context.Background(), newClient(), dir, st, lang)
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "Branch or tag to clone (default: the remote's default branch)")
	cmd.Flags().StringVar(&name, "name", "", "Collection name (default: ref-<owner>-<repo>)")
	cmd.Flags().StringVar(&lang, "lang", "en", "Language code stored on every document")
	return cmd
}

// indexReference marks the collection as reference code — with the
// repository, commit and licence in its description — and indexes the clone,
// pruning files the repository no longer has.
func indexReference(ctx context.Context, client *mddb.Client, dir string, st referenceState, lang string) error {
	commit := ""
	if g := detectGit(dir); g != nil {
		commit = g.Commit
	}
	desc := fmt.Sprintf("Reference: %s @ %s (licence: %s)", st.URL, shortCommit(commit), st.License)
	if err := updateCollectionConfig(ctx, client, st.Collection, func(req *mddb.SetCollectionConfigRequest) bool {
		if req.Code == nil {
			req.Code = &mddb.CodeConfig{}
		}
		req.Code.Role, req.Description = "reference", desc
		return true
	}); err != nil {
		return fmt.Errorf("mark %s as reference: %w", st.Collection, err)
	}
	return runAutoindex(ctx, client, autoindexOptions{root: dir, collection: st.Collection, lang: lang,
		maxSize: defaultMaxFileSize, prune: true, exclude: []string{referenceStateFile},
		extraMeta: map[string][]string{"license": {st.License}, "sourceRepository": {st.URL}}})
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	if c == "" {
		return "unknown"
	}
	return c
}

func newReferenceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List reference collections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := newClient().Do(context.Background(), "GET", "/v1/collection-configs", nil)
			if err != nil {
				return err
			}
			var all struct {
				Configs []mddb.CollectionConfigResponse `json:"configs"`
			}
			if err := json.Unmarshal(raw, &all); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			type row struct{ Collection, Description string }
			var rows []row
			for _, c := range all.Configs {
				if c.Config.Code != nil && c.Config.Code.Role == "reference" {
					rows = append(rows, row{c.Collection, c.Config.Description})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Collection < rows[j].Collection })
			if outputJSON {
				data, _ := json.MarshalIndent(rows, "", "  ")
				fmt.Println(string(data))
				return nil
			}
			if len(rows) == 0 {
				fmt.Println("No reference collections. Add one with: mddb-cli reference add <repository>")
			}
			for _, r := range rows {
				fmt.Printf("%s\t%s\n", r.Collection, r.Description)
			}
			return nil
		},
	}
}

func newReferenceUpdateCmd() *cobra.Command {
	var lang string
	cmd := &cobra.Command{
		Use:   "update [name]",
		Short: "Pull the latest commit of a reference (or all of them) and re-index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := args
			if len(names) == 0 {
				root, err := referencesRoot()
				if err != nil {
					return err
				}
				entries, err := os.ReadDir(root)
				if err != nil {
					return fmt.Errorf("no references in %s: %w", root, err)
				}
				for _, e := range entries {
					if e.IsDir() {
						names = append(names, e.Name())
					}
				}
			}
			for _, name := range names {
				if err := updateReference(name, lang); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&lang, "lang", "en", "Language code stored on every document")
	return cmd
}

func updateReference(name, lang string) error {
	dir, err := referenceDir(name)
	if err != nil {
		return err
	}
	st, err := readReferenceState(dir)
	if err != nil {
		return fmt.Errorf("not a reference clone (%w)", err)
	}
	if err := gitRefresh(dir, st.Ref); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	st.License = detectLicense(dir)
	if err := writeReferenceState(dir, st); err != nil {
		return err
	}
	return indexReference(context.Background(), newClient(), dir, st, lang)
}

func newReferenceRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a reference collection and its clone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := referenceDir(args[0])
			if err != nil {
				return err
			}
			collection := args[0]
			if st, err := readReferenceState(dir); err == nil {
				collection = st.Collection
			}
			if _, err := newClient().Do(context.Background(), "POST", "/v1/delete-collection",
				map[string]string{"collection": collection}); err != nil {
				return err
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			fmt.Printf("✓ Removed reference %s\n", collection)
			return nil
		},
	}
}

func newReferenceSearchCmd() *cobra.Command {
	var topK int
	var collections []string
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search every reference collection (hybrid), answered as path:lines passages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := collections
			if len(targets) == 0 {
				targets = []string{"role:reference"}
			}
			raw, err := newClient().Do(context.Background(), "POST", "/v1/cross-search", map[string]any{
				"query": args[0], "targetCollections": targets, "searchType": "hybrid", "format": "passages", "topK": topK,
			})
			if err != nil {
				return err
			}
			if outputJSON {
				fmt.Println(string(raw))
				return nil
			}
			var resp struct {
				Passages []struct {
					Collection, Path, Symbol, Content string
					Lines                             struct{ Start, End int }
				} `json:"passages"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			for _, p := range resp.Passages {
				fmt.Printf("%s %s:%d-%d %s\n", p.Collection, p.Path, p.Lines.Start, p.Lines.End, p.Symbol)
				lines := strings.Split(p.Content, "\n")
				for _, line := range lines[:min(3, len(lines))] {
					fmt.Println("    " + line)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 10, "Passages to return")
	cmd.Flags().StringArrayVar(&collections, "collection", nil, "Search only this reference collection (repeatable)")
	return cmd
}
