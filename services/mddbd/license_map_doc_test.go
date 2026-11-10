package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// LIC-001: the repository has two licences. LICENSING.md says which directory
// is under which; this file checks that every place a licence is declared
// agrees with that map, so a manifest cannot drift back to BSD-3-Clause or a
// client library be relabelled BUSL by accident.

// licensingDocPath is the map; licenseMapRepoRoot is the repository root.
const (
	licensingDocPath   = "../../LICENSING.md"
	licenseMapRepoRoot = "../.."
)

// licenseMapRowRe matches a row of the map table: the directory in
// backticks, the SPDX identifier, the LICENSE file in backticks.
var licenseMapRowRe = regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([A-Za-z0-9.-]+) \\| `([^`]+)` \\|")

// licenseMapEntry is one row of LICENSING.md.
type licenseMapEntry struct {
	dir, spdx, file string
}

// licenseMapFirstLine is what each LICENSE file must start with; a BUSL
// file opens with the licence text's own copyright line and names its
// licensor further down.
var licenseMapFirstLine = map[string]string{
	"BUSL-1.1":     "License text copyright (c) 2020 MariaDB Corporation Ab",
	"BSD-3-Clause": "BSD 3-Clause License",
}

// licenseManifestRes extracts the declared licence from each manifest kind,
// by file name. Files whose kind is missing here are not manifests.
var licenseManifestRes = map[string]*regexp.Regexp{
	"package.json":   regexp.MustCompile(`"license":\s*"([^"]+)"`),
	"composer.json":  regexp.MustCompile(`"license":\s*"([^"]+)"`),
	"pyproject.toml": regexp.MustCompile(`(?m)^license\s*=\s*(?:\{\s*text\s*=\s*)?"([^"]+)"`),
	"Cargo.toml":     regexp.MustCompile(`(?m)^license\s*=\s*"([^"]+)"`),
	"metadata.yaml":  regexp.MustCompile(`(?m)^\s*license:\s*(\S+)`),
	"mddb-sync.php":  regexp.MustCompile(`\*\s*License:\s+(\S+)`),
	"readme.txt":     regexp.MustCompile(`(?m)^License:\s*(\S+)`),
	"openapi.yaml":   regexp.MustCompile(`(?m)^  license:\n    name:\s*(\S+)`),
}

// licenseManifestOptional are manifests without a licence field by design:
// test tooling that is never published.
var licenseManifestOptional = map[string]bool{"test/es-compat/pyproject.toml": true}

// licenseWalkSkip are directories the manifest walk does not enter.
var licenseWalkSkip = map[string]bool{".git": true, ".claude": true, "node_modules": true, "dist": true,
	"target": true, "vendor": true, "audit": true, "scratchpad": true, ".venv": true, "build": true}

// readLicenseMap parses LICENSING.md into its rows, root first.
func readLicenseMap(t *testing.T) []licenseMapEntry {
	t.Helper()
	data, err := os.ReadFile(licensingDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", licensingDocPath, err)
	}
	var out []licenseMapEntry
	for _, m := range licenseMapRowRe.FindAllStringSubmatch(string(data), -1) {
		out = append(out, licenseMapEntry{dir: strings.TrimSuffix(m[1], "/"), spdx: m[2], file: m[3]})
	}
	if len(out) < 2 || out[0].dir != "" || out[0].spdx != "BUSL-1.1" {
		t.Fatalf("LICENSING.md: expected a table whose first row is the root under BUSL-1.1, got %+v", out)
	}
	return out
}

// licenseFor answers the licence of a repository-relative path: the row with
// the longest directory prefix wins, the root row catches the rest.
func licenseFor(entries []licenseMapEntry, rel string) string {
	best := entries[0]
	for _, e := range entries[1:] {
		if strings.HasPrefix(rel, e.dir+"/") && len(e.dir) > len(best.dir) {
			best = e
		}
	}
	return best.spdx
}

// TestLicenseFilesMatchMap checks that every LICENSE file the map names
// exists, is the licence the row says, and that the BUSL parameters name
// Tradik Limited as licensor with Apache-2.0 as the change licence.
func TestLicenseFilesMatchMap(t *testing.T) {
	for _, e := range readLicenseMap(t) {
		data, err := os.ReadFile(filepath.Join(licenseMapRepoRoot, e.file))
		if err != nil {
			t.Errorf("%s: %v", e.file, err)
			continue
		}
		text := string(data)
		if want := licenseMapFirstLine[e.spdx]; want == "" || !strings.HasPrefix(text, want) {
			t.Errorf("%s: should open with %q for %s", e.file, want, e.spdx)
		}
		if e.spdx == "BUSL-1.1" {
			for _, want := range []string{"Licensor:             Tradik Limited", "Change License:       Apache License, Version 2.0",
				"Business Source License 1.1", "Additional Use Grant:"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s: missing %q", e.file, want)
				}
			}
		}
	}
}

// TestManifestsDeclareDirectoryLicense walks the repository and compares the
// licence each package manifest declares with the directory's licence.
func TestManifestsDeclareDirectoryLicense(t *testing.T) {
	entries := readLicenseMap(t)
	seen := 0
	err := filepath.WalkDir(licenseMapRepoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if licenseWalkSkip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		re := licenseManifestRes[d.Name()]
		if re == nil {
			return nil
		}
		rel, _ := filepath.Rel(licenseMapRepoRoot, path)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m := re.FindStringSubmatch(string(data))
		if m == nil {
			if !licenseManifestOptional[rel] {
				t.Errorf("%s: no licence field", rel)
			}
			return nil
		}
		seen++
		if want := licenseFor(entries, rel); m[1] != want {
			t.Errorf("%s declares %s, LICENSING.md gives its directory %s", rel, m[1], want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 15 {
		t.Errorf("found %d manifests with a licence field, expected at least 15", seen)
	}
}

// TestReleasePackagingDeclaresLicense checks the release workflow (deb and
// rpm metadata, Homebrew formulas, OCI labels) and the Airbyte image, which
// carry a licence outside any manifest.
func TestReleasePackagingDeclaresLicense(t *testing.T) {
	entries := readLicenseMap(t)
	checks := []struct {
		file    string
		re      *regexp.Regexp
		atLeast int
	}{
		{".github/workflows/release.yml", regexp.MustCompile(`(?m)^\s+License:\s+(\S+)`), 2},
		{".github/workflows/release.yml", regexp.MustCompile(`(?m)^\s+license "([^"]+)"`), 2},
		{".github/workflows/release.yml", regexp.MustCompile(`org\.opencontainers\.image\.licenses=(\S+)`), 2},
		{".github/workflows/airbyte-destination.yml", regexp.MustCompile(`org\.opencontainers\.image\.licenses=(\S+)`), 1},
		{"integrations/airbyte-destination/Dockerfile", regexp.MustCompile(`org\.opencontainers\.image\.licenses="([^"]+)"`), 1},
	}
	for _, c := range checks {
		data, err := os.ReadFile(filepath.Join(licenseMapRepoRoot, c.file))
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		// The Airbyte image is built from integrations/, the rest from the root.
		want := licenseFor(entries, "integrations/airbyte-destination/Dockerfile")
		if !strings.Contains(c.file, "airbyte") {
			want = entries[0].spdx
		}
		found := c.re.FindAllStringSubmatch(string(data), -1)
		if len(found) < c.atLeast {
			t.Errorf("%s: %d matches of %s, expected at least %d", c.file, len(found), c.re, c.atLeast)
		}
		for _, m := range found {
			if m[1] != want {
				t.Errorf("%s declares %s, expected %s", c.file, m[1], want)
			}
		}
	}
}

// TestNoOpenSourceClaimAboutMDDB keeps the words "open source" out of the
// texts that describe MDDB itself, since 2.19.0 the product is
// source-available under BUSL-1.1. Pages that use the words about other
// software (embedding models, Airbyte) are not listed.
func TestNoOpenSourceClaimAboutMDDB(t *testing.T) {
	re := regexp.MustCompile(`(?i)open[- ]source`)
	files := []string{"README.md", "LICENSING.md", "docs/DOCKER_HUB.md", "docs/DOCKER_HUB_SHORT.txt", "docs/LISTING.md",
		"services/ssg-template/base.html", "services/ssg-template/index.html", ".ssg.yaml", "server.json"}
	var bad []string
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(licenseMapRepoRoot, f))
		if err != nil {
			continue // optional: LISTING.md, DOCKER_HUB_SHORT.txt and server.json arrive with MKT-001/005
		}
		if re.Match(data) {
			bad = append(bad, f)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("these files call MDDB open source; it is source-available (LICENSING.md): %v", bad)
	}
}
