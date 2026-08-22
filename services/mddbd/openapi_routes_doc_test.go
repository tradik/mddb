package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// DOC-010: /v1/mcp/keys and /v1/mcp/keys/disable were served and documented in
// docs/MCP.md with curl examples, yet absent from docs/openapi.yaml — so every
// generated client silently lacked MCP key management. This guard compares the
// routes the server registers against the paths the spec declares, and fails on
// anything served but undocumented.

var (
	// mux.HandleFunc("/v1/...", ...) — the literal route patterns in routes.go.
	routeRe = regexp.MustCompile(`mux\.HandleFunc\("(/v1/[^"]*)"`)
	// Top-level "  /path:" entries under openapi.yaml's paths: section.
	specPathRe = regexp.MustCompile(`(?m)^  (/v1/[^:]*):`)
)

// routesExemptFromSpec are served but deliberately absent from the spec.
// Keep this list short and justified — it is the escape hatch the guard is
// meant to make expensive.
var routesExemptFromSpec = map[string]bool{}

func TestServedRoutesAreDocumentedInOpenAPI(t *testing.T) {
	// routes.go, not main.go: the registrations were extracted there in
	// TEST-002 so they could be tested. This guard failed loudly on the move
	// rather than passing on zero routes, which is what it was built for.
	main, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	spec, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	// Duplicate path keys are the failure this guard nearly missed: YAML
	// keeps the last definition silently, so a second copy of a path looks
	// fine to a parser while the two bodies drift apart.
	documented := map[string]bool{}
	seen := map[string]int{}
	for _, m := range specPathRe.FindAllStringSubmatch(string(spec), -1) {
		path := strings.TrimSuffix(m[1], "/")
		documented[path] = true
		seen[path]++
	}
	var duplicated []string
	for path, n := range seen {
		if n > 1 {
			duplicated = append(duplicated, path)
		}
	}
	sort.Strings(duplicated)
	if len(duplicated) > 0 {
		t.Errorf("openapi.yaml defines these paths more than once (YAML keeps only the last):\n  %s",
			strings.Join(duplicated, "\n  "))
	}
	if len(documented) == 0 {
		t.Fatal("found no /v1 paths in openapi.yaml — the guard regex may be stale")
	}

	served := map[string]bool{}
	for _, m := range routeRe.FindAllStringSubmatch(string(main), -1) {
		served[strings.TrimSuffix(m[1], "/")] = true
	}
	if len(served) == 0 {
		t.Fatal("found no mux.HandleFunc /v1 routes in routes.go — the guard regex may be stale")
	}

	var missing []string
	for route := range served {
		if documented[route] || routesExemptFromSpec[route] {
			continue
		}
		// A route registered as a prefix ("/v1/docs/") covers spec paths that
		// add parameters ("/v1/docs/{collection}/{key}"), so accept any
		// documented path that starts with it.
		covered := false
		for path := range documented {
			if strings.HasPrefix(path, route+"/") {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, route)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("routes served by main.go but missing from docs/openapi.yaml:\n  %s\n"+
			"Add them to the spec, or justify an entry in routesExemptFromSpec.",
			strings.Join(missing, "\n  "))
	}
}

// The MCP key routes are the reason this guard exists; assert them by name so
// a future refactor of the generic check cannot quietly drop the case.
func TestMCPKeyRoutesDocumented(t *testing.T) {
	spec, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	for _, want := range []string{"  /v1/mcp/keys:", "  /v1/mcp/keys/disable:"} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("openapi.yaml is missing %q", strings.TrimSpace(want))
		}
	}
	for _, op := range []string{"listMCPKeys", "createMCPKey", "deleteMCPKey", "disableMCPKey"} {
		if !strings.Contains(string(spec), op) {
			t.Errorf("openapi.yaml is missing operationId %q", op)
		}
	}
	// The handlers gate every one of these on PermAdmin, so the spec has to
	// say so — a generated client that omits the token gets a 403 it was
	// never told to expect.
	keysSection := string(spec)[strings.Index(string(spec), "  /v1/mcp/keys:"):]
	keysSection = keysSection[:strings.Index(keysSection, "\n  /v1/vector-projection:")]
	if got := strings.Count(keysSection, "security:"); got != 4 {
		t.Errorf("the four MCP key operations should each declare security:, found %d", got)
	}
}
