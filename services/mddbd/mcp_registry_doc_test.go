package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// Guards for the MCP Registry entry (server.json at the repository root,
// MKT-001) and for the one-line description every catalogue repeats
// (MKT-005). The registry proves ownership by reading the
// io.modelcontextprotocol.server.name label of the image the entry names,
// refuses a version it already holds and a description over 100 characters.
// Each of those is a release that publishes nothing, found only after the
// tag; here they fail before it. Paths are relative to services/mddbd, as in
// config_doc_test.go.

const (
	mcpRegistryEntryPath      = "../../server.json"
	mcpRegistrySchemaURL      = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"
	mcpRegistryImage          = "docker.io/tradik/mddb:"
	mcpRegistryDescriptionMax = 100
	dockerHubShortPath        = "../../docs/DOCKER_HUB_SHORT.txt"
	listingPath               = "../../docs/LISTING.md"
)

// mcpRegistryLabelFiles carry the label the registry compares with the
// entry's name: both Dockerfiles, and the labels block release.yml passes to
// build-push-action, which replaces the file's labels on the published image.
var mcpRegistryLabelFiles = []string{
	"../../services/mddbd/Dockerfile",
	"../../services/mddbd/Dockerfile.release",
	"../../.github/workflows/release.yml",
}

// mcpRegistryLabel is the label's key, as `key="value"` or `key=value`.
const mcpRegistryLabel = "io.modelcontextprotocol.server.name="

// mcpRegistryEntry is the part of server.json the guards read.
type mcpRegistryEntry struct {
	Schema      string `json:"$schema"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Packages    []struct {
		RegistryType string `json:"registryType"`
		Identifier   string `json:"identifier"`
		Transport    struct {
			Type string `json:"type"`
		} `json:"transport"`
		EnvironmentVariables []struct {
			Name string `json:"name"`
		} `json:"environmentVariables"`
	} `json:"packages"`
}

// readMCPRegistryEntry parses server.json.
func readMCPRegistryEntry(t *testing.T) mcpRegistryEntry {
	t.Helper()
	var e mcpRegistryEntry
	if err := json.Unmarshal([]byte(readRepoFile(t, mcpRegistryEntryPath)), &e); err != nil {
		t.Fatalf("%s: %v", mcpRegistryEntryPath, err)
	}
	if e.Schema != mcpRegistrySchemaURL {
		t.Errorf("%s: $schema is %q, the entry is written against %s", mcpRegistryEntryPath, e.Schema, mcpRegistrySchemaURL)
	}
	return e
}

// imageLabelValues returns every value the file gives the registry label.
func imageLabelValues(text string) []string {
	var values []string
	for _, line := range strings.Split(text, "\n") {
		i := strings.Index(line, mcpRegistryLabel)
		if i < 0 {
			continue
		}
		v := strings.TrimSpace(line[i+len(mcpRegistryLabel):])
		values = append(values, strings.Trim(v, `"`))
	}
	return values
}

// TestMCPRegistryNameMatchesImageLabel: the registry reads the label from the
// published image and rejects the entry when it differs from the name.
func TestMCPRegistryNameMatchesImageLabel(t *testing.T) {
	e := readMCPRegistryEntry(t)
	if e.Name == "" {
		t.Fatalf("%s: no name", mcpRegistryEntryPath)
	}
	for _, path := range mcpRegistryLabelFiles {
		values := imageLabelValues(readRepoFile(t, path))
		if len(values) == 0 {
			t.Errorf("%s: no %s label; the registry cannot verify the image", path, strings.TrimSuffix(mcpRegistryLabel, "="))
		}
		for _, v := range values {
			if v != e.Name {
				t.Errorf("%s: label says %q, server.json names %q", path, v, e.Name)
			}
		}
	}
}

// TestMCPRegistryVersionIsTheRelease: the registry keeps one record per
// version, so the entry must move with VERSION (scripts/bump-version.sh
// rewrites it), and the OCI identifier must name the image tag release.yml
// pushes for the same release.
func TestMCPRegistryVersionIsTheRelease(t *testing.T) {
	e := readMCPRegistryEntry(t)
	if e.Version != VERSION {
		t.Errorf("%s: version %q, the server is %s (run scripts/bump-version.sh)", mcpRegistryEntryPath, e.Version, VERSION)
	}
	if len(e.Packages) != 1 {
		t.Fatalf("%s: %d packages, the entry has one OCI package", mcpRegistryEntryPath, len(e.Packages))
	}
	p := e.Packages[0]
	if p.RegistryType != "oci" {
		t.Errorf("%s: registryType %q, want oci", mcpRegistryEntryPath, p.RegistryType)
	}
	if want := mcpRegistryImage + VERSION; p.Identifier != want {
		t.Errorf("%s: identifier %q, want %q", mcpRegistryEntryPath, p.Identifier, want)
	}
	if p.Transport.Type != "stdio" {
		t.Errorf("%s: transport %q, the image serves MCP on stdio when MDDB_MCP_STDIO=true", mcpRegistryEntryPath, p.Transport.Type)
	}
}

// TestMCPRegistryDescriptionIsTheListingOneLiner: the description fits the
// schema's 100 characters, states the tool count .ssg.yaml renders, and is the
// one-liner docs/LISTING.md and the Docker Hub short description carry, so
// every catalogue says the same thing.
func TestMCPRegistryDescriptionIsTheListingOneLiner(t *testing.T) {
	e := readMCPRegistryEntry(t)
	if n := utf8.RuneCountInString(e.Description); n == 0 || n > mcpRegistryDescriptionMax {
		t.Errorf("%s: description is %d characters, the schema allows 1..%d", mcpRegistryEntryPath, n, mcpRegistryDescriptionMax)
	}
	tools := ssgVar(t, readRepoFile(t, "../../.ssg.yaml"), "mcpTools")
	if !strings.Contains(e.Description, tools+" MCP tools") {
		t.Errorf("%s: description %q does not state %q", mcpRegistryEntryPath, e.Description, tools+" MCP tools")
	}
	if short := strings.TrimSpace(readRepoFile(t, dockerHubShortPath)); short != e.Description {
		t.Errorf("%s says %q, server.json says %q; both copy the one-liner in %s", dockerHubShortPath, short, e.Description, listingPath)
	}
	if !strings.Contains(readRepoFile(t, listingPath), e.Description) {
		t.Errorf("%s does not carry the one-liner server.json uses: %q", listingPath, e.Description)
	}
}

// TestMCPRegistryEnvironmentVariablesAreSettings: every variable the entry
// tells a host to set is one the server reads (config_allowlist.go).
func TestMCPRegistryEnvironmentVariablesAreSettings(t *testing.T) {
	e := readMCPRegistryEntry(t)
	for _, p := range e.Packages {
		for _, v := range p.EnvironmentVariables {
			if _, ok := configKeys[v.Name]; !ok {
				t.Errorf("%s: environment variable %q is not a setting mddbd reads", mcpRegistryEntryPath, v.Name)
			}
		}
	}
}

func TestImageLabelValues(t *testing.T) {
	got := imageLabelValues("FROM x\nLABEL io.modelcontextprotocol.server.name=\"a/b\"\n  io.modelcontextprotocol.server.name=c/d\nEXPOSE 1\n")
	if len(got) != 2 || got[0] != "a/b" || got[1] != "c/d" {
		t.Errorf("imageLabelValues = %q, want [a/b c/d]", got)
	}
}
