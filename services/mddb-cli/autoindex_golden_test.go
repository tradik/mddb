package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Without --semantic, autoindex must behave and print exactly as 2.19.2 did
// (CODE-011): the files under testdata/autoindex-golden were written by the
// 2.19.2 code and are compared byte for byte, the run's duration and the
// temporary directory aside. MDDB_UPDATE_GOLDEN=1 rewrites them — only ever
// for a change meant to alter the output.

var (
	goldenDuration = regexp.MustCompile(`in \d+ms|"durationMs": \d+`)
)

func normalizeGolden(out, dir string) string {
	out = strings.ReplaceAll(out, dir, "<ROOT>")
	// JSON doubles each backslash of a Windows path.
	out = strings.ReplaceAll(out, strings.ReplaceAll(dir, `\`, `\\`), "<ROOT>")
	return goldenDuration.ReplaceAllStringFunc(out, func(m string) string {
		if strings.HasPrefix(m, "in ") {
			return "in <N>ms"
		}
		return `"durationMs": <N>`
	})
}

func TestAutoindex_GoldenOutputWithoutSemantic(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"run.txt", []string{"autoindex", "--prune", "-c", "proj"}},
		{"dry-run.txt", []string{"autoindex", "--dry-run", "--prune", "-c", "proj"}},
		{"run.json", []string{"--json", "autoindex", "--prune", "-c", "proj"}},
		{"dry-run.json", []string{"--json", "autoindex", "--dry-run", "-c", "proj"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := sampleTree(t)
			fs := newFakeServer(t, autoindexRoutes())
			out, err := runCLI(t, fs.URL, append(c.args, dir)...)
			if err != nil {
				t.Fatal(err)
			}
			got := normalizeGolden(out, dir)
			path := filepath.Join("testdata", "autoindex-golden", c.name)
			if os.Getenv("MDDB_UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path) // #nosec G304 -- a fixture of this test
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("output changed without --semantic:\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}
