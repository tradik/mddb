package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateWordPressTarget(t *testing.T) {
	cases := []struct {
		name    string
		target  *WordPressTargetConfig
		wantErr bool
	}{
		{"nil target is fine", nil, false},
		{"https accepted", &WordPressTargetConfig{URL: "https://blog.example.com"}, false},
		{"http localhost accepted", &WordPressTargetConfig{URL: "http://localhost:8080"}, false},
		{"http loopback accepted", &WordPressTargetConfig{URL: "http://127.0.0.1"}, false},
		{"http remote rejected", &WordPressTargetConfig{URL: "http://blog.example.com"}, true},
		{"missing url rejected", &WordPressTargetConfig{APIKey: "k"}, true},
		{"relative url rejected", &WordPressTargetConfig{URL: "blog.example.com"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWordPressTarget(tc.target)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateWordPressTarget(%+v) error = %v, wantErr %v", tc.target, err, tc.wantErr)
			}
		})
	}
}

// wordpressTestServer records the last request the tool sent and answers with
// the given status/body.
func wordpressTestServer(t *testing.T, status int, response string) (*httptest.Server, *http.Request, *map[string]interface{}) {
	t.Helper()
	var lastReq http.Request
	body := map[string]interface{}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReq = *r.Clone(context.Background())
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &lastReq, &body
}

// The httptest server URL is http://127.0.0.1:<port>, which passes the
// localhost-only http exemption — no TLS setup needed in tests.
func TestToolWordPressPublishSendsMappedPayload(t *testing.T) {
	srv, lastReq, body := wordpressTestServer(t, http.StatusOK, `{"id":123,"created":true}`)

	s := &MCPToolServer{}
	out, err := s.toolWordPressPublish(context.Background(), map[string]interface{}{
		"site_url":         srv.URL,
		"api_key":          "vk_test",
		"post_type":        "post",
		"title":            "Hello",
		"content_markdown": "# Hi",
		"status":           "publish",
		"tags":             []interface{}{"a", "b"},
		"categories":       []interface{}{"News"},
		"meta":             map[string]interface{}{"seoTitle": "Hello SEO"},
		"taxonomies":       map[string]interface{}{"series": []interface{}{"Alpha"}},
		"lang":             "pl_PL",
		"translation_of":   float64(8),
		"author":           float64(3),
	})
	if err != nil {
		t.Fatalf("toolWordPressPublish: %v", err)
	}
	if !strings.Contains(out, `"id":123`) {
		t.Errorf("result should include the response body, got %q", out)
	}

	if got := lastReq.URL.Path; got != "/wp-json/mddb-sync/v1/publish" {
		t.Errorf("path = %q, want /wp-json/mddb-sync/v1/publish", got)
	}
	if got := lastReq.Header.Get("Authorization"); got != "Bearer vk_test" {
		t.Errorf("Authorization = %q, want Bearer vk_test", got)
	}

	b := *body
	if b["type"] != "post" || b["title"] != "Hello" || b["contentMarkdown"] != "# Hi" || b["status"] != "publish" {
		t.Errorf("payload string fields mismatched: %+v", b)
	}
	if b["lang"] != "pl_PL" || b["translationOf"] != float64(8) || b["author"] != float64(3) {
		t.Errorf("payload lang/translation/author mismatched: %+v", b)
	}
	if tags, ok := b["tags"].([]interface{}); !ok || len(tags) != 2 {
		t.Errorf("payload tags mismatched: %+v", b["tags"])
	}
	if meta, ok := b["meta"].(map[string]interface{}); !ok || meta["seoTitle"] != "Hello SEO" {
		t.Errorf("payload meta mismatched: %+v", b["meta"])
	}
}

func TestToolWordPressPublishRequiresTitleOrID(t *testing.T) {
	s := &MCPToolServer{}
	_, err := s.toolWordPressPublish(context.Background(), map[string]interface{}{
		"site_url": "http://localhost:1",
		"status":   "publish",
	})
	if err == nil || !strings.Contains(err.Error(), "title") {
		t.Errorf("expected title/post_id error, got %v", err)
	}
}

func TestToolWordPressPublishSurfacesHTTPErrors(t *testing.T) {
	srv, _, _ := wordpressTestServer(t, http.StatusUnauthorized, `{"code":"mddb_publish_unauthorized"}`)

	s := &MCPToolServer{}
	_, err := s.toolWordPressPublish(context.Background(), map[string]interface{}{
		"site_url": srv.URL,
		"title":    "X",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("expected HTTP 401 error, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "mddb_publish_unauthorized") {
		t.Errorf("error should carry the response snippet, got %v", err)
	}
}

func TestToolWordPressPublishRejectsInsecureOverride(t *testing.T) {
	s := &MCPToolServer{}
	_, err := s.toolWordPressPublish(context.Background(), map[string]interface{}{
		"site_url": "http://blog.example.com",
		"title":    "X",
	})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("expected https enforcement error, got %v", err)
	}
}

func TestToolWordPressPublishRequiresCollectionOrSiteURL(t *testing.T) {
	s := &MCPToolServer{}
	_, err := s.toolWordPressPublish(context.Background(), map[string]interface{}{"title": "X"})
	if err == nil || !strings.Contains(err.Error(), "collection") {
		t.Errorf("expected collection-required error, got %v", err)
	}
}

func TestToolWordPressSetStatus(t *testing.T) {
	srv, lastReq, body := wordpressTestServer(t, http.StatusOK, `{"id":5,"status":"draft"}`)

	s := &MCPToolServer{}
	out, err := s.toolWordPressSetStatus(context.Background(), map[string]interface{}{
		"site_url": srv.URL,
		"post_id":  float64(5),
		"status":   "draft",
	})
	if err != nil {
		t.Fatalf("toolWordPressSetStatus: %v", err)
	}
	if !strings.Contains(out, `"status":"draft"`) {
		t.Errorf("result should include the response body, got %q", out)
	}
	if got := lastReq.URL.Path; got != "/wp-json/mddb-sync/v1/status" {
		t.Errorf("path = %q, want /wp-json/mddb-sync/v1/status", got)
	}
	b := *body
	if b["id"] != float64(5) || b["status"] != "draft" {
		t.Errorf("payload mismatched: %+v", b)
	}
}

func TestToolWordPressSetStatusValidation(t *testing.T) {
	s := &MCPToolServer{}

	_, err := s.toolWordPressSetStatus(context.Background(), map[string]interface{}{
		"site_url": "http://localhost:1",
		"post_id":  float64(5),
	})
	if err == nil || !strings.Contains(err.Error(), "status is required") {
		t.Errorf("expected status-required error, got %v", err)
	}

	_, err = s.toolWordPressSetStatus(context.Background(), map[string]interface{}{
		"site_url": "http://localhost:1",
		"status":   "draft",
	})
	if err == nil || !strings.Contains(err.Error(), "post_id or slug") {
		t.Errorf("expected identifier-required error, got %v", err)
	}
}

func TestWordPressSnippetBoundsAndFlattens(t *testing.T) {
	long := strings.Repeat("secret line\n", 200)
	out := wordpressSnippet([]byte(long))
	if strings.Contains(out, "\n") {
		t.Error("snippet must be single-line")
	}
	if len(out) > wordpressMaxSnippet+len("…") {
		t.Errorf("snippet must be bounded, got %d bytes", len(out))
	}
}

// TestWordPressToolsAreNotReadOnly guards the annotation entries: both tools
// mutate remote state and must be blocked in read-only mode.
func TestWordPressToolsAreNotReadOnly(t *testing.T) {
	ts := &MCPToolServer{}
	for _, name := range []string{"wordpress_publish", "wordpress_set_status"} {
		if ts.isToolReadOnly(name) {
			t.Errorf("%s must not be classified read-only (it writes to WordPress)", name)
		}
	}
}

// SEC-013 / CodeQL go/request-forgery. validateWordPressTarget enforces the
// scheme and nothing else, so an https:// URL pointing at a cloud metadata
// endpoint or a cluster address passed it — and was then dialled by a bare
// http.Client that carries no SSRF guard. The check moved to the point of use
// so a target stored before it existed is covered too.
func TestWordPressRefusesAPrivateTarget(t *testing.T) {
	// TestMain enables the opt-in for the whole binary, because most tests
	// dial httptest servers on 127.0.0.1. Turn it back off to assert blocking,
	// the same way internal/httpclient's own tests do.
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "")

	blocked := []string{
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.5",
		"https://192.168.1.1",
		"https://172.16.0.2:8080",
	}

	for _, raw := range blocked {
		_, err := wordpressPost(context.Background(),
			&WordPressTargetConfig{URL: raw}, "/publish", map[string]interface{}{})
		if err == nil {
			t.Errorf("%s was dialled", raw)
			continue
		}
		if !strings.Contains(err.Error(), "wordpress.url") {
			t.Errorf("%s: refusal does not name the field: %v", raw, err)
		}
	}
}

func TestWordPressAllowsLoopbackWithoutOptIn(t *testing.T) {
	// A WordPress on the same machine is the documented development setup, so
	// it must not need an environment variable. Nothing is listening, so the
	// call fails at the dial — which is the point: it got past validation.
	_, err := wordpressPost(context.Background(),
		&WordPressTargetConfig{URL: "http://127.0.0.1:1"}, "/publish", map[string]interface{}{})

	if err == nil {
		t.Fatal("expected a connection failure")
	}
	if strings.Contains(err.Error(), "wordpress.url") {
		t.Errorf("loopback was refused by validation: %v", err)
	}
}

func TestWordPressHonoursTheOutboundOptIn(t *testing.T) {
	t.Setenv("MDDB_OUTBOUND_ALLOW_PRIVATE", "true")

	_, err := wordpressPost(context.Background(),
		&WordPressTargetConfig{URL: "https://10.0.0.5:1"}, "/publish", map[string]interface{}{})

	if err != nil && strings.Contains(err.Error(), "wordpress.url") {
		t.Errorf("the opt-in did not take: %v", err)
	}
}
