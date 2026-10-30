package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/spf13/cobra"
	mddb "github.com/tradik/mddb/clients/go/mddb/v2"
)

// mddb-cli diagnose (OPS-025): save the server's diagnose bundle — a tar.gz
// without user data, to attach to an issue — or, with --inspect, show what it
// would contain without writing anything.

const (
	// diagnoseTimeout bounds the default bundle; --integrity scans every
	// database file and gets diagnoseIntegrityTimeout.
	diagnoseTimeout          = 2 * time.Minute
	diagnoseIntegrityTimeout = 30 * time.Minute
)

// bundleManifest is the part of MANIFEST.json the CLI reads.
type bundleManifest struct {
	Version   string `json:"version"`
	CreatedAt string `json:"createdAt"`
	Files     []struct {
		Name      string `json:"name"`
		Size      int    `json:"size"`
		SHA256    string `json:"sha256"`
		Redaction string `json:"redaction"`
	} `json:"files"`
	Omitted []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"omitted"`
	Never []string `json:"neverIncluded"`
}

func newDiagnoseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diagnose",
		Short: "Save a diagnose bundle (no user data) to attach to an issue",
		Long: `Download a tar.gz describing the server — build, configuration through an
allowlist, stats, metrics, query-shape counters, recent error classes — without
document content, metadata values, queries, keys, actors, IP addresses or paths.
Collection names are hashed unless --keep-names is given. MANIFEST.json lists
every file, its size, its SHA-256 and the redaction applied; --inspect prints it
without writing anything. Needs an admin login or key.`,
		Args: cobra.NoArgs,
		RunE: runDiagnose,
	}
	cmd.Flags().StringP("out", "o", "", "File to write (default mddb-diagnose-<host>-<time>.tar.gz)")
	cmd.Flags().Bool("profiles", false, "Include runtime counters (goroutines, heap, GC)")
	cmd.Flags().Bool("integrity", false, "Include bbolt's consistency check of every database file (slow on a large database)")
	cmd.Flags().Bool("keep-names", false, "Keep collection names instead of hashing them")
	cmd.Flags().Bool("inspect", false, "Print the bundle's manifest and write nothing")
	return cmd
}

func runDiagnose(cmd *cobra.Command, _ []string) error {
	var opts mddb.DiagnoseOptions
	opts.Profiles, _ = cmd.Flags().GetBool("profiles")
	opts.Integrity, _ = cmd.Flags().GetBool("integrity")
	opts.KeepNames, _ = cmd.Flags().GetBool("keep-names")
	inspect, _ := cmd.Flags().GetBool("inspect")
	out, _ := cmd.Flags().GetString("out")

	timeout := diagnoseTimeout
	if opts.Integrity {
		timeout = diagnoseIntegrityTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	bundle, err := newClient().Diagnose(ctx, opts)
	if err != nil {
		return err
	}
	man, err := readManifest(bundle)
	if err != nil {
		return err
	}
	if inspect {
		return printManifest(man)
	}
	if out == "" {
		out = fmt.Sprintf("mddb-diagnose-%s-%s.tar.gz", bundleHost(serverURL), time.Now().UTC().Format("20060102-150405"))
	}
	if err := os.WriteFile(filepath.Clean(out), bundle, 0o600); err != nil {
		return err
	}
	if outputJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"file": out, "bytes": len(bundle), "files": len(man.Files)})
	}
	fmt.Printf("✓ Diagnose bundle written: %s (%d bytes, %d files)\n", out, len(bundle), len(man.Files))
	fmt.Println("  Review it with `mddb-cli diagnose --inspect`, or open MANIFEST.json, before attaching it to an issue.")
	return nil
}

// readManifest unpacks the bundle, checks every file against the size and
// SHA-256 the manifest gives, and returns the manifest.
func readManifest(bundle []byte) (*bundleManifest, error) {
	zr, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return nil, fmt.Errorf("not a diagnose bundle: %w", err)
	}
	tr := tar.NewReader(zr)
	sums := map[string]string{}
	var man *bundleManifest
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not a diagnose bundle: %w", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("not a diagnose bundle: %w", err)
		}
		if hdr.Name == "MANIFEST.json" {
			man = &bundleManifest{}
			if err := json.Unmarshal(body, man); err != nil {
				return nil, fmt.Errorf("unreadable MANIFEST.json: %w", err)
			}
			continue
		}
		sum := sha256.Sum256(body)
		sums[hdr.Name] = hex.EncodeToString(sum[:])
	}
	if man == nil {
		return nil, errors.New("not a diagnose bundle: no MANIFEST.json")
	}
	for _, f := range man.Files {
		if sums[f.Name] != f.SHA256 {
			return nil, fmt.Errorf("%s does not match its manifest entry: the bundle is damaged", f.Name)
		}
	}
	return man, nil
}

// printManifest shows what the bundle carries and what it never does.
func printManifest(man *bundleManifest) error {
	if outputJSON {
		return json.NewEncoder(os.Stdout).Encode(man)
	}
	fmt.Printf("Diagnose bundle of MDDB %s, made %s\n\n", man.Version, man.CreatedAt)
	for _, f := range man.Files {
		fmt.Printf("  %-20s %8d bytes  sha256 %s\n      %s\n", f.Name, f.Size, shorten(f.SHA256, 16), f.Redaction)
	}
	if len(man.Omitted) > 0 {
		fmt.Println("\nNot included:")
		for _, o := range man.Omitted {
			fmt.Printf("  %-20s %s\n", o.Name, o.Reason)
		}
	}
	fmt.Println("\nNever included:")
	for _, n := range man.Never {
		fmt.Printf("  - %s\n", n)
	}
	return nil
}

// hostChars is what a host may keep in a file name.
var hostChars = regexp.MustCompile(`[^A-Za-z0-9.-]+`)

// bundleHost is the server's host for the default file name.
func bundleHost(server string) string {
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" {
		return "server"
	}
	return hostChars.ReplaceAllString(u.Hostname(), "_")
}
