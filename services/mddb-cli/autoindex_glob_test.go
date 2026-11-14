package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The include/exclude glob cases are shared: mddbd runs them through the
// git sync's matcher (#421), so a git sync and autoindex select the same
// files from one repository.
const globCasesPath = "../../testdata/globs/cases.json"

func TestMatchAnySharedCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean(globCasesPath))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string   `json:"name"`
		Globs []string `json:"globs"`
		Path  string   `json:"path"`
		Match bool     `json:"match"`
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("the shared cases: %v", err)
	}
	for _, c := range cases {
		if got := matchAny(c.Globs, c.Path); got != c.Match {
			t.Errorf("%s: matchAny(%v, %q) = %v", c.Name, c.Globs, c.Path, got)
		}
	}
}
