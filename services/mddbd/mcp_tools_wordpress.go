package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mddb/internal/httpclient"
	"net/http"
	"net/url"
	"strings"
	"time"

	json "mddb/internal/jsonx"
)

// Outbound half of the MCP → WordPress publishing bridge. The tools below
// POST to the REST routes the mddb-sync WordPress plugin registers
// (integrations/wordpress-plugin, "Remote publishing" toggle). The target
// site + publish key come from the collection's config (set via
// set_collection_config with a wordpress {url, api_key} object) or from
// explicit site_url/api_key arguments.
const (
	wordpressRestBase    = "/wp-json/mddb-sync/v1"
	wordpressHTTPTimeout = 30 * time.Second
	wordpressMaxResponse = 1 << 20 // bound untrusted response bodies to 1 MiB
	wordpressMaxSnippet  = 500
)

// validateWordPressTarget enforces https:// on the publishing endpoint so the
// publish key never travels in cleartext; http:// is permitted only for local
// development hosts — the same rule the WP plugin applies to its MDDB URL.
func validateWordPressTarget(t *WordPressTargetConfig) error {
	if t == nil {
		return nil
	}
	if t.URL == "" {
		return errors.New("wordpress.url is required when a wordpress target is set")
	}
	u, err := url.Parse(t.URL)
	if err != nil {
		return fmt.Errorf("invalid wordpress.url: %w", err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
		return errors.New("wordpress.url must use https:// (http is allowed only for localhost, 127.0.0.1 or ::1)")
	default:
		return errors.New("wordpress.url must be an absolute http(s) URL")
	}
}

// wordpressTargetFor resolves the outbound target: explicit site_url/api_key
// arguments win; otherwise the collection's stored config supplies it.
func (s *MCPToolServer) wordpressTargetFor(ctx context.Context, args map[string]interface{}) (*WordPressTargetConfig, error) {
	if siteURL := mcpGetString(args, "site_url"); siteURL != "" {
		target := &WordPressTargetConfig{URL: siteURL, APIKey: mcpGetString(args, "api_key")}
		if err := validateWordPressTarget(target); err != nil {
			return nil, err
		}
		return target, nil
	}

	collection := mcpGetString(args, "collection")
	if collection == "" {
		return nil, errors.New("collection is required (or pass site_url + api_key directly)")
	}
	resp, err := s.client.GetCollectionConfig(ctx, collection)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Config == nil || resp.Config.WordPress == nil || resp.Config.WordPress.URL == "" {
		return nil, fmt.Errorf(
			"collection %q has no WordPress publishing target — call set_collection_config with a wordpress {url, api_key} object first",
			collection,
		)
	}
	return resp.Config.WordPress, nil
}

func (s *MCPToolServer) toolWordPressPublish(ctx context.Context, args map[string]interface{}) (string, error) {
	target, err := s.wordpressTargetFor(ctx, args)
	if err != nil {
		return "", err
	}

	payload := map[string]interface{}{}
	stringFields := map[string]string{
		"post_type":        "type",
		"title":            "title",
		"slug":             "slug",
		"content_markdown": "contentMarkdown",
		"content_html":     "contentHtml",
		"excerpt":          "excerpt",
		"status":           "status",
		"date":             "date",
		"lang":             "lang",
	}
	for arg, field := range stringFields {
		if v := mcpGetString(args, arg); v != "" {
			payload[field] = v
		}
	}
	intFields := map[string]string{
		"post_id":        "id",
		"author":         "author",
		"translation_of": "translationOf",
	}
	for arg, field := range intFields {
		if v := mcpGetInt(args, arg); v > 0 {
			payload[field] = v
		}
	}
	if tags := mcpGetStringSlice(args, "tags"); tags != nil {
		payload["tags"] = tags
	}
	if categories := mcpGetStringSlice(args, "categories"); categories != nil {
		payload["categories"] = categories
	}
	if meta, ok := args["meta"].(map[string]interface{}); ok {
		payload["meta"] = meta
	}
	if taxonomies, ok := args["taxonomies"].(map[string]interface{}); ok {
		payload["taxonomies"] = taxonomies
	}

	if _, hasTitle := payload["title"]; !hasTitle {
		if _, hasID := payload["id"]; !hasID {
			return "", errors.New("pass title (create) or post_id (update)")
		}
	}

	result, err := wordpressPost(ctx, target, "/publish", payload)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("WordPress publish result:\n%s", result), nil
}

func (s *MCPToolServer) toolWordPressSetStatus(ctx context.Context, args map[string]interface{}) (string, error) {
	target, err := s.wordpressTargetFor(ctx, args)
	if err != nil {
		return "", err
	}

	status := mcpGetString(args, "status")
	if status == "" {
		return "", errors.New("status is required (publish, draft, pending, private, future, trash)")
	}
	payload := map[string]interface{}{"status": status}
	if id := mcpGetInt(args, "post_id"); id > 0 {
		payload["id"] = id
	}
	if slug := mcpGetString(args, "slug"); slug != "" {
		payload["slug"] = slug
	}
	if postType := mcpGetString(args, "post_type"); postType != "" {
		payload["type"] = postType
	}
	if date := mcpGetString(args, "date"); date != "" {
		payload["date"] = date
	}
	if _, hasID := payload["id"]; !hasID {
		if _, hasSlug := payload["slug"]; !hasSlug {
			return "", errors.New("pass post_id or slug (+ post_type) to identify the post")
		}
	}

	result, err := wordpressPost(ctx, target, "/status", payload)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("WordPress status result:\n%s", result), nil
}

// wordpressPost sends one JSON request to the mddb-sync REST namespace and
// returns the raw response body (bounded) on 2xx.
func wordpressPost(ctx context.Context, target *WordPressTargetConfig, path string, payload map[string]interface{}) (string, error) {
	// Checked here, at the point of use, not only where a target is stored:
	// a configuration written before this validation existed would otherwise
	// still be dialled (SEC-013, found by CodeQL go/request-forgery once the
	// Go analysis started working at all).
	//
	// validateWordPressTarget above enforces the scheme and nothing else, so
	// https://169.254.169.254/ passed it and was then dialled by a bare
	// http.Client carrying no SSRF guard. Loopback still needs no opt-in — a
	// WordPress on the same machine is the documented development setup —
	// while anything else private needs MDDB_OUTBOUND_ALLOW_PRIVATE or the
	// host allowlist, exactly as the embedding providers do.
	safeURL, err := httpclient.ValidateServiceURL(target.URL)
	if err != nil {
		return "", fmt.Errorf("wordpress.url: %w", err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimRight(safeURL, "/") + wordpressRestBase + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if target.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+target.APIKey)
	}

	client := &http.Client{Timeout: wordpressHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("wordpress request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, wordpressMaxResponse))
	if err != nil {
		return "", fmt.Errorf("wordpress response read failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("wordpress returned HTTP %d: %s", resp.StatusCode, wordpressSnippet(data))
	}
	return string(data), nil
}

// wordpressSnippet collapses an untrusted response body to one bounded line
// so it can be embedded in error messages without flooding or forging logs.
func wordpressSnippet(data []byte) string {
	s := strings.Join(strings.Fields(string(data)), " ")
	if len(s) > wordpressMaxSnippet {
		s = s[:wordpressMaxSnippet] + "…"
	}
	return s
}
