package main

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mddb/internal/fts"
)

// Guards for the numbers the site (services/ssg-template, .ssg.yaml) and the
// docs state about the code. Each one had drifted: the site said 7 vector index
// algorithms after sq4 made 8, 18 stemming languages where 17 are registered,
// "13 formats" for 20, and a ~28 MB binary that measures 32 MiB. The pattern
// is mcp_tool_count_doc_test.go's: count from the code, then read every file
// that states the count. Paths are relative to services/mddbd.

// readRepoFile reads one of the hardcoded repository files a guard checks.
func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a hardcoded repository doc path, not user input
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// assertCountClaims fails when a file states no count matching re, or a count
// other than want. Every capture group of re is a candidate; the non-empty one
// is the number.
func assertCountClaims(t *testing.T, re *regexp.Regexp, want int, what string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		matches := re.FindAllStringSubmatch(readRepoFile(t, path), -1)
		if len(matches) == 0 {
			t.Errorf("%s: states no %s count — the guard regex may be stale", path, what)
			continue
		}
		for _, m := range matches {
			if got := strings.Join(m[1:], ""); got != strconv.Itoa(want) {
				t.Errorf("%s: says %s %s in %q, the code has %d", path, got, what, m[0], want)
			}
		}
	}
}

// TestVectorAlgorithmCountDocsInSync: the algorithms are the searchers a
// server builds (newVectorSearchers), less "quantized", which is the storage
// form behind sq/sq4, not an algorithm a request names.
func TestVectorAlgorithmCountDocsInSync(t *testing.T) {
	searchers := newVectorSearchers(nil, nil, 0)
	want := 0
	for name := range searchers {
		if name == "quantized" {
			continue
		}
		if !validVectorAlgorithm(name) {
			t.Errorf("searcher %q is not accepted by validVectorAlgorithm", name)
		}
		want++
	}
	re := regexp.MustCompile(`(\d+) (?:vector )?index (?:algorithms|types)`)
	assertCountClaims(t, re, want, "vector index algorithms",
		"../../README.md",
		"../../docs/README.md",
		"../../services/ssg-template/index.html",
		"../../services/ssg-template/index-section-features.html",
	)
}

// TestFTSLanguageCountDocsInSync: the languages are what RegisterDefaultLanguages
// registers. A stemmer without a registration (Tamil, Irish) is not one.
func TestFTSLanguageCountDocsInSync(t *testing.T) {
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	re := regexp.MustCompile(`(\d+)(?: |-)lang(?:uages|uage|s)\b`)
	assertCountClaims(t, re, len(reg.Languages()), "stemming languages",
		"../../README.md",
		"../../docs/README.md",
		"../../docs/SEARCH.md",
		"../../docs/API.md",
		"../../docs/ARCHITECTURE.md",
		"../../services/ssg-template/index.html",
		"../../services/ssg-template/index-section-features.html",
	)
}

// TestUploadFormatCountDocsInSync: the formats are the ones an upload error
// lists (supportedFormatsHint), which is what a user can send.
func TestUploadFormatCountDocsInSync(t *testing.T) {
	want := len(strings.Split(supportedFormatsHint, ", "))
	re := regexp.MustCompile(`(?i)(\d+) file formats`)
	assertCountClaims(t, re, want, "file formats",
		"../../docs/README.md",
		"../../services/ssg-template/index.html",
		"../../services/ssg-template/index-section-features.html",
	)
}

// ssgVar returns the value of a `variables:` entry in .ssg.yaml.
func ssgVar(t *testing.T, yaml, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(name) + `: "([^"]*)"`).FindStringSubmatch(yaml)
	if m == nil {
		t.Fatalf(".ssg.yaml: no variable %s", name)
	}
	return m[1]
}

// mbOf reads the number out of a size claim such as "~32MB".
func mbOf(t *testing.T, claim string) float64 {
	t.Helper()
	m := regexp.MustCompile(`^~(\d+(?:\.\d+)?)MB$`).FindStringSubmatch(claim)
	if m == nil {
		t.Fatalf("size claim %q is not of the form ~NMB", claim)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("size claim %q: %v", claim, err)
	}
	return v
}

// TestSiteSizeClaimsInSync keeps every size the site and docs state equal to
// the one .ssg.yaml variable that renders it on the landing page, and that
// variable within 10% of the measurement recorded next to it. It builds
// nothing: the measurement is the byte count in the comment, which whoever
// re-measures (the command is in the same comment) updates.
func TestSiteSizeClaimsInSync(t *testing.T) {
	yaml := readRepoFile(t, "../../.ssg.yaml")
	binary := mbOf(t, ssgVar(t, yaml, "mddbBinarySize"))
	docker := mbOf(t, ssgVar(t, yaml, "mddbDockerSize"))
	mbOf(t, ssgVar(t, yaml, "mddbDownloadSize"))

	measured := regexp.MustCompile(`: ([\d,]+) bytes = `).FindStringSubmatch(yaml)
	if measured == nil {
		t.Fatal(".ssg.yaml: no recorded binary measurement (\"<version>: N bytes = X MiB\") next to mddbBinarySize")
	}
	bytes, err := strconv.ParseFloat(strings.ReplaceAll(measured[1], ",", ""), 64)
	if err != nil {
		t.Fatalf(".ssg.yaml measurement %q: %v", measured[1], err)
	}
	if mib := bytes / (1 << 20); math.Abs(binary-mib)/mib > 0.10 {
		t.Errorf(".ssg.yaml: mddbBinarySize ~%gMB is more than 10%% from the measured %.1f MiB", binary, mib)
	}

	binaryRe := regexp.MustCompile(`~(\d+(?:\.\d+)?) ?MB binary|binary is ~(\d+(?:\.\d+)?) MB`)
	dockerRe := regexp.MustCompile("Docker image is ~(\\d+) ?MB|\\| Docker image \\| ~(\\d+) ?MB|`tradik/mddb:latest` \\| ~(\\d+) ?MB|image size \\(~(\\d+) ?MB")
	for _, c := range []struct {
		re    *regexp.Regexp
		want  float64
		what  string
		paths []string
	}{
		{binaryRe, binary, "binary size (MB)", []string{"../../README.md", "../../docs/README.md", "../../docs/index.md", "../../docs/COMPARISON.md"}},
		{dockerRe, docker, "Docker image size (MB)", []string{"../../README.md", "../../docs/COMPARISON.md", "../../docs/FEATURES.md"}},
	} {
		for _, path := range c.paths {
			matches := c.re.FindAllStringSubmatch(readRepoFile(t, path), -1)
			if len(matches) == 0 {
				t.Errorf("%s: states no %s — the guard regex may be stale", path, c.what)
			}
			for _, m := range matches {
				got, _ := strconv.ParseFloat(strings.Join(m[1:], ""), 64)
				if got != c.want {
					t.Errorf("%s: %s %q, .ssg.yaml says ~%gMB", path, c.what, m[0], c.want)
				}
			}
		}
	}
}

// TestLandingSizesRenderFromVariables: the landing page must not hardcode a
// size; it renders the .ssg.yaml variables, so there is one place to update.
func TestLandingSizesRenderFromVariables(t *testing.T) {
	re := regexp.MustCompile(`~\d+(?:\.\d+)? ?MB`)
	for _, path := range []string{
		"../../services/ssg-template/index.html",
		"../../services/ssg-template/index-section-hero.html",
		"../../services/ssg-template/index-section-features.html",
		"../../services/ssg-template/index-section-download.html",
	} {
		if m := re.FindString(readRepoFile(t, path)); m != "" {
			t.Errorf("%s: hardcodes %q; render {{$.Vars.mddbBinarySize}}, mddbDownloadSize or mddbDockerSize", path, m)
		}
	}
}
