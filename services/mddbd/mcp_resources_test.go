package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// Every resource this server advertises is written `scheme://first/second`,
// and url.Parse puts `first` in Host rather than in Path. Reading Path alone
// meant `resources/list` named four resources and `resources/read` refused all
// four: `mddb://health` arrived as an empty path, and `mddb://docs/key` as a
// one-segment path that failed the collection/key split.
func TestResourceURIsAreReadableAtTheAddressTheyAreAdvertisedUnder(t *testing.T) {
	cases := []struct {
		uri  string
		want string
	}{
		{"mddb://health", "health"},
		{"mddb://stats", "stats"},
		{"mddb://docs/quickstart", "docs/quickstart"},
		{"mddb://Docs/Key-01", "Docs/Key-01"},
		{"mddb://docs/key?lang=pl_PL", "docs/key"},
		// The three-slash spelling was the only one that ever worked, and it
		// has to keep working: clients written against the bug still send it.
		{"mddb:///health", "health"},
		{"mddb:///docs/quickstart", "docs/quickstart"},
		{"mddb-search://docs", "docs"},
	}

	for _, tc := range cases {
		parsed, err := url.Parse(tc.uri)
		if err != nil {
			t.Fatalf("%s: %v", tc.uri, err)
		}
		if got := mcpResourcePath(parsed); got != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.uri, got, tc.want)
		}
	}
}

// The advertised list and the reader have to agree, which is the property that
// went missing: this walks what resources/list returns and reads every entry.
// A list a client cannot act on is worse than an empty one.
func TestEveryAdvertisedResourceCanBeRead(t *testing.T) {
	srv, cleanup := newHandlerTestServer(t)
	defer cleanup()
	h := NewMCPHandlerWithConfig(NewDirectClient(srv), nil, MCPServerInfo{}, "", ModeRW, "")
	ts := &MCPToolServer{client: h.client, globalMode: ModeRW}

	resources, _ := h.handleResourcesList(nil)["resources"].([]MCPResource)
	if len(resources) == 0 {
		t.Fatal("resources/list returned nothing")
	}

	for _, resource := range resources {
		if _, err := url.Parse(resource.URI); err != nil {
			t.Errorf("%s is not a URI: %v", resource.URI, err)
			continue
		}
		if _, err := ts.readResource(context.Background(), resource.URI); err != nil {
			t.Errorf("%s is advertised and cannot be read: %v", resource.URI, err)
		}
	}
}

// A pattern is not an address. Both templates were listed as resources, where
// a client is entitled to read every entry, and neither is even a parseable
// URI — the braces are invalid in a host. They belong to their own method.
func TestTemplatesAreListedAsTemplatesAndNotAsResources(t *testing.T) {
	h := newModernTestHandler()

	resources, _ := h.handleResourcesList(nil)["resources"].([]MCPResource)
	for _, resource := range resources {
		if strings.Contains(resource.URI, "{") {
			t.Errorf("%s is a template listed as a resource", resource.URI)
		}
	}

	result := modernResult(t, h, modernRequest("resources/templates/list", nil))
	templates, _ := result["resourceTemplates"].([]MCPResourceTemplate)
	if len(templates) != 2 {
		t.Fatalf("resources/templates/list returned %d templates, want 2", len(templates))
	}
	for _, template := range templates {
		if !strings.Contains(template.URITemplate, "{") {
			t.Errorf("%s is not a template", template.URITemplate)
		}
	}
	if result["cacheScope"] != mcpCachePublic {
		t.Errorf("cacheScope = %v, want the template list to be cacheable like the others", result["cacheScope"])
	}
}

// The bug this guards is not in the path helper but in what a client actually
// gets: a document read at the URI the templates advertise, and a search at
// the one beside it. Neither could be reached before — the collection ended up
// in the URI's host and the reader only ever looked at its path.
func TestADocumentAndASearchAreReachableThroughTheirURIs(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "quickstart")
	ts := &MCPToolServer{client: client, globalMode: ModeRW}

	document, err := ts.readResource(context.Background(), "mddb://docs/quickstart?lang=en")
	if err != nil {
		t.Fatalf("mddb://docs/quickstart could not be read: %v", err)
	}
	if !strings.Contains(document, "quickstart") {
		t.Errorf("read returned %q, want the seeded document", document)
	}

	results, err := ts.readResource(context.Background(), "mddb-search://docs?q=original")
	if err != nil {
		t.Fatalf("mddb-search://docs could not be read: %v", err)
	}
	if !strings.Contains(results, "quickstart") {
		t.Errorf("search returned %q, want the seeded document", results)
	}

	// A URI naming neither is still refused, and says what the shape should be.
	if _, err := ts.readResource(context.Background(), "mddb://docs"); err == nil {
		t.Error("a collection with no key was read as though it were a document")
	}
	if _, err := ts.readResource(context.Background(), "mddb-search://"); err == nil {
		t.Error("a search with no collection was accepted")
	}
	if _, err := ts.readResource(context.Background(), "ftp://elsewhere"); err == nil {
		t.Error("an unsupported scheme was accepted")
	}
}
