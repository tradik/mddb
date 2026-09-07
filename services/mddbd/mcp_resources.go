package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// mcpResourceCatalogue is every resource a client can read by name, and the
// single source of truth for both surfaces that list them: the MCP handler and
// the /resources endpoint, which each carried their own copy of this list.
func mcpResourceCatalogue() []MCPResource {
	return []MCPResource{
		{
			URI:         "mddb://health",
			Name:        "MDDB Health",
			Description: "Health status of MDDB server",
			MimeType:    "application/json",
		},
		{
			URI:         "mddb://stats",
			Name:        "MDDB Statistics",
			Description: "Server and database statistics",
			MimeType:    "application/json",
		},
	}
}

// mcpResourceTemplates is every resource addressed by a pattern.
func mcpResourceTemplates() []MCPResourceTemplate {
	return []MCPResourceTemplate{
		{
			URITemplate: "mddb://{collection}/{key}?lang={lang}",
			Name:        "MDDB Document",
			Description: "Get a document by collection, key, and language",
			MimeType:    "text/markdown",
		},
		{
			URITemplate: "mddb-search://{collection}?q={query}",
			Name:        "MDDB Search",
			Description: "Search documents in a collection",
			MimeType:    "application/json",
		},
	}
}

// readResource reads resource based on URI.
func (s *MCPToolServer) readResource(ctx context.Context, uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("invalid uri: %w", err)
	}

	switch parsed.Scheme {
	case "mddb":
		return s.readMDDBResource(ctx, parsed)
	case "mddb-search":
		return s.readSearchResource(ctx, parsed)
	default:
		return "", fmt.Errorf("unsupported uri scheme: %s", parsed.Scheme)
	}
}

// mcpResourcePath is everything after the scheme of a resource URI.
//
// url.Parse puts the first segment after "//" in Host rather than in Path, so
// reading Path alone saw an empty string for `mddb://health` and just `key`
// for `mddb://docs/key`. Every resource this server advertises is written in
// that form, which made all of them unreadable at the URI they were advertised
// under: `resources/list` named four resources and `resources/read` refused
// each one. Joining the two parts back together reads the URI as it was
// written, and the three-slash spelling that used to be the only one that
// worked (`mddb:///health`) still does.
func mcpResourcePath(uri *url.URL) string {
	return strings.Trim(strings.Trim(uri.Host, "/")+"/"+strings.Trim(uri.Path, "/"), "/")
}

func (s *MCPToolServer) readMDDBResource(ctx context.Context, uri *url.URL) (string, error) {
	path := mcpResourcePath(uri)

	if path == "health" {
		health, err := s.client.Health(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(health)
		return string(data), nil
	}

	if path == "stats" {
		stats, err := s.client.Stats(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(stats)
		return string(data), nil
	}

	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid document uri: expected mddb://{collection}/{key}")
	}

	collection := parts[0]
	key := parts[1]
	lang := uri.Query().Get("lang")
	if lang == "" {
		lang = "en_US"
	}

	envVars := make(map[string]string)
	for k, v := range uri.Query() {
		if strings.HasPrefix(k, "env.") {
			envKey := strings.TrimPrefix(k, "env.")
			if len(v) > 0 {
				envVars[envKey] = v[0]
			}
		}
	}

	doc, err := s.client.Get(ctx, &MCPGetRequest{
		Collection: collection,
		Key:        key,
		Lang:       lang,
		Env:        envVars,
	})
	if err != nil {
		return "", err
	}

	return doc.ContentMD, nil
}

func (s *MCPToolServer) readSearchResource(ctx context.Context, uri *url.URL) (string, error) {
	collection := mcpResourcePath(uri)
	if collection == "" {
		return "", fmt.Errorf("collection required in search uri")
	}

	query := uri.Query()
	req := &MCPSearchRequest{
		Collection:     collection,
		FilterMeta:     make(map[string][]string),
		IncludeContent: true,
	}

	for k, v := range query {
		if strings.HasPrefix(k, "meta.") {
			metaKey := strings.TrimPrefix(k, "meta.")
			req.FilterMeta[metaKey] = v
		}
	}

	if sortVal := query.Get("sort"); sortVal != "" {
		req.Sort = sortVal
	}
	if limit := query.Get("limit"); limit != "" {
		if _, err := fmt.Sscanf(limit, "%d", &req.Limit); err != nil {
			req.Limit = 0
		}
	}
	if offset := query.Get("offset"); offset != "" {
		if _, err := fmt.Sscanf(offset, "%d", &req.Offset); err != nil {
			req.Offset = 0
		}
	}

	resp, err := s.client.Search(ctx, req)
	if err != nil {
		return "", err
	}

	data, _ := json.Marshal(resp)
	return string(data), nil
}
