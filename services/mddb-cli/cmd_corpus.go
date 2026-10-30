package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// mddb-cli corpus (AGT-005): portable, read-only collections. A pack is built
// once from a collection — documents, full-text index, vectors — and installed
// on any server without re-embedding or reindexing. See docs/CORPUS-PACKS.md.

func newCorpusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "corpus",
		Short: "Build, install, list and remove corpus packs",
		Long: `A corpus pack is a collection shipped as one file (.mddbpack): its documents,
full-text index and vectors, with a manifest naming its sources and licences.
An installed pack is a read-only collection; writes to it are refused.

Pack only what may be redistributed: build refuses without --license.`,
	}
	cmd.AddCommand(newCorpusBuildCmd(), newCorpusInstallCmd(), newCorpusListCmd(), newCorpusRemoveCmd())
	return cmd
}

// corpusSource is one origin of a pack's documents, as the server takes it.
type corpusSource struct {
	Repo    string `json:"repo"`
	Commit  string `json:"commit,omitempty"`
	License string `json:"license"`
}

// parseCorpusSource reads --source LICENSE=REPO[@COMMIT].
func parseCorpusSource(s string) (corpusSource, error) {
	license, repo, ok := strings.Cut(s, "=")
	if !ok || strings.TrimSpace(license) == "" || strings.TrimSpace(repo) == "" {
		return corpusSource{}, fmt.Errorf("--source %q: want LICENSE=REPO[@COMMIT], e.g. Apache-2.0=https://github.com/org/repo@v1.2", s)
	}
	src := corpusSource{Repo: repo, License: license}
	// The commit follows the last '@', unless that '@' belongs to the repo
	// (git@host:org/repo).
	if i := strings.LastIndex(repo, "@"); i > 0 && !strings.ContainsAny(repo[i+1:], "/:") {
		src.Repo, src.Commit = repo[:i], repo[i+1:]
	}
	return src, nil
}

// defaultPackFile is the file a build writes without -o, the name the
// server suggests too: <collection>[-<version>].mddbpack.
func defaultPackFile(collection, version string) string {
	if version == "" {
		return collection + ".mddbpack"
	}
	return collection + "-" + version + ".mddbpack"
}

func newCorpusBuildCmd() *cobra.Command {
	var output, license, version, description string
	var sources []string
	cmd := &cobra.Command{
		Use:   "build <collection>",
		Short: "Build a pack of a collection",
		Long: `Ask the server for a consistent snapshot of the collection and save it as a
pack. The pack installs under the collection's name.

  mddb-cli corpus build k8s-docs --license CC-BY-4.0 --version 1.31 \
    --source CC-BY-4.0=https://github.com/kubernetes/website@release-1.31 -o k8s-docs-1.31.mddbpack`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if strings.TrimSpace(license) == "" {
				return errors.New("build refuses without --license: pack only what may be redistributed, and say under which terms")
			}
			body := map[string]any{"collection": args[0], "version": version, "description": description, "license": license}
			var parsed []corpusSource
			for _, s := range sources {
				src, err := parseCorpusSource(s)
				if err != nil {
					return err
				}
				parsed = append(parsed, src)
			}
			body["sources"] = parsed
			if output == "" {
				output = defaultPackFile(args[0], version)
			}
			raw, _ := json.Marshal(body) // strings and a slice of them: always encodes
			rc, err := newClient().DoStream(context.Background(), "POST", "/v1/corpus/build", "application/json", bytes.NewReader(raw))
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			n, sum, err := saveStream(output, rc)
			if err != nil {
				return err
			}
			fmt.Printf("✓ Pack written: %s (%d bytes, sha256 %s)\n", output, n, sum)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Pack file to write (default <collection>[-<version>].mddbpack)")
	cmd.Flags().StringVar(&license, "license", "", "Licence of the pack as a whole, e.g. CC-BY-4.0 (required)")
	cmd.Flags().StringVar(&version, "version", "", "Pack version, e.g. 1.31")
	cmd.Flags().StringVar(&description, "description", "", "What the pack holds")
	cmd.Flags().StringArrayVar(&sources, "source", nil, "A source of the documents: LICENSE=REPO[@COMMIT] (repeatable)")
	return cmd
}

func newCorpusListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the packs installed on the server",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			raw, err := newClient().Do(context.Background(), "GET", "/v1/corpus", nil)
			if err != nil {
				return err
			}
			if outputJSON {
				fmt.Println(string(raw))
				return nil
			}
			var resp struct {
				Packs []struct {
					Collection string         `json:"collection"`
					Corpus     map[string]any `json:"corpus"`
				} `json:"packs"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			if len(resp.Packs) == 0 {
				fmt.Println("No corpus packs installed.")
			}
			for _, p := range resp.Packs {
				vectors := "vectors"
				if p.Corpus["vectorsDisabled"] == true {
					vectors = "full text only"
				}
				fmt.Printf("%s\t%s\t%.0f docs\t%s\t%s\n", p.Collection, asString(p.Corpus["version"]),
					asFloat(p.Corpus["docCount"]), asString(p.Corpus["license"]), vectors)
			}
			return nil
		},
	}
}

func newCorpusRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed pack: its collection, file and config",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			raw, err := newClient().Do(context.Background(), "POST", "/v1/corpus/remove", map[string]string{"name": args[0]})
			if err != nil {
				return err
			}
			var resp map[string]any
			_ = json.Unmarshal(raw, &resp)
			fmt.Printf("✓ Removed pack %s (%.0f documents)\n", args[0], asFloat(resp["deleted"]))
			return nil
		},
	}
}
