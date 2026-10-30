package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The frontmatter fixtures are shared: services/mddbd and services/mddb-cli
// run the same cases through their parseFrontmatter (#373), so /v1/upload,
// /v1/ingest, `mddb-cli add` and scripts/load-md-folder.sh agree on a file.
const frontmatterCasesPath = "../../testdata/frontmatter/cases.json"

// frontmatterCase is one input and what parseFrontmatter must return for it.
type frontmatterCase struct {
	Name  string              `json:"name"`
	Input string              `json:"input"`
	Meta  map[string][]string `json:"meta"`
	Body  string              `json:"body"`
}

func TestParseFrontmatterSharedCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Clean(frontmatterCasesPath))
	if err != nil {
		t.Fatalf("read shared fixtures: %v", err)
	}
	var cases []frontmatterCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parse shared fixtures: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no shared fixtures")
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			meta, body := parseFrontmatter(tc.Input)
			if !reflect.DeepEqual(meta, tc.Meta) {
				t.Errorf("meta = %#v, want %#v", meta, tc.Meta)
			}
			if body != tc.Body {
				t.Errorf("body = %q, want %q", body, tc.Body)
			}
		})
	}
}
