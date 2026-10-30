package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// docToolCountRe captures the number in every phrasing the docs use to state the
// built-in MCP tool total — "N MCP tools", "N built-in tools", "N built-in MCP
// tools", "N tools for Claude". It deliberately does NOT match the per-category
// counts in docs/MCP.md ("9 key tools", "6 tools", "4 tools"), which are not the
// total.
var docToolCountRe = regexp.MustCompile(`(\d+)\s+(?:built-in MCP tools|built-in tools|MCP tools|tools for Claude)`)

// TestMCPToolCountDocsInSync guards DOC-001: the hard-coded built-in MCP tool
// count in README.md and docs/MCP.md must equal len(mcpBuiltinTools()). When a
// tool is added or removed the count changes here and this test fails until the
// docs are updated — preventing the drift that left the docs at 67 while the
// code defined 77. Runs as part of the normal `go test ./...` CI job.
func TestMCPToolCountDocsInSync(t *testing.T) {
	want := strconv.Itoa(len(mcpBuiltinTools()))

	// Paths are relative to this package directory (services/mddbd). The docs
	// index and the architecture overview joined the guard after both were
	// found still claiming 67 — the number the guard was written to stop
	// drifting, in the two files it did not yet cover.
	for _, path := range []string{
		"../../README.md",
		"../../docs/MCP.md",
		"../../docs/README.md",
		"../../docs/ARCHITECTURE.md",
	} {
		data, err := os.ReadFile(path) // #nosec G304 -- path is one of two hardcoded repo doc files, not user input
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		matches := docToolCountRe.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Errorf("%s: found no MCP tool-count phrase — the guard regex may be stale", path)
			continue
		}
		for _, m := range matches {
			if m[1] != want {
				t.Errorf("%s: documents %q MCP tools but code defines %s (in %q). "+
					"Update the docs (and run `make mcp-tools-count`).", path, m[1], want, m[0])
			}
		}
	}
}

// landingToolCountRe matches a tool count written as a number anywhere in the
// SSG landing templates: "N MCP Tools", "N tools", "N built-in tools", the old
// JS const. The landing page renders the count from {{$.Vars.mcpTools}}, which
// TestMCPToolCountSiteInSync pins; a literal next to it is a second source that
// drifts (it said 67 and 72 here), so any literal must equal the code count.
var landingToolCountRe = regexp.MustCompile(`MDDB_MCP_TOOLS\s*=\s*(\d+)|\b(\d+) (?:built-in )?(?:MCP )?[Tt]ools\b`)

// TestMCPToolCountLandingInSync guards the landing page (services/ssg-template)
// against the same drift DOC-001 fixed in the docs.
func TestMCPToolCountLandingInSync(t *testing.T) {
	want := strconv.Itoa(len(mcpBuiltinTools()))
	paths, err := filepath.Glob("../../services/ssg-template/index*.html")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no landing templates found: %v", err)
	}
	usesVar := false
	for _, path := range paths {
		data, err := os.ReadFile(path) // #nosec G304 -- a landing template found by a fixed glob, not user input
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		usesVar = usesVar || strings.Contains(string(data), "{{$.Vars.mcpTools}}")
		for _, m := range landingToolCountRe.FindAllStringSubmatch(string(data), -1) {
			if got := m[1] + m[2]; got != want {
				t.Errorf("%s: tool count %q != code count %s (in %q); render {{$.Vars.mcpTools}}", path, got, want, m[0])
			}
		}
	}
	if !usesVar {
		t.Error("no landing template renders {{$.Vars.mcpTools}} — the guard is checking nothing")
	}
}

// siteToolCountRe matches the count where the site and the docs front matter
// state it: the SSG variable every landing section renders ({{$.Vars.mcpTools}}),
// the docs index description "(N tools)" and the comparison table "N tools over".
// These three still said 81 after the code reached 93, because only the
// landing's index.html was guarded.
var siteToolCountRe = regexp.MustCompile(`mcpTools:\s*"(\d+)"|MCP server \((\d+) tools\)|native — (\d+) tools over`)

// TestMCPToolCountSiteInSync guards the SSG variable and the two docs pages the
// other guards do not read.
func TestMCPToolCountSiteInSync(t *testing.T) {
	want := strconv.Itoa(len(mcpBuiltinTools()))
	for _, path := range []string{"../../.ssg.yaml", "../../docs/index.md", "../../docs/COMPARISON.md"} {
		data, err := os.ReadFile(path) // #nosec G304 -- one of three hardcoded repo files, not user input
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		matches := siteToolCountRe.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Errorf("%s: found no MCP tool-count reference — the guard regex may be stale", path)
			continue
		}
		for _, m := range matches {
			if got := m[1] + m[2] + m[3]; got != want {
				t.Errorf("%s: tool count %q != code count %s (in %q)", path, got, want, m[0])
			}
		}
	}
}
