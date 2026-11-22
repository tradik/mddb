package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// FE-021: texts a person reads in the panel or in an MCP client carry no
// issue number ("#449") and no ticket ID ("INT-019"). Those belong in code
// comments, CHANGELOG.md and the audit, not in a feature card or a tool
// description.
var internalRefRe = regexp.MustCompile(`#\d+\b|\b[A-Z]{2,5}-\d{3}\b`)

// standardNames look like ticket IDs but name standards (SHA-256, AES-256).
var standardNames = map[string]bool{"SHA": true, "AES": true}

// assertPlainText fails when text carries an internal reference.
func assertPlainText(t *testing.T, where, text string) {
	t.Helper()
	if ref := internalRef(text); ref != "" {
		t.Errorf("%s: user-visible text carries %q: %q", where, ref, text)
	}
}

// internalRef returns the first issue number or ticket ID in text, or "".
func internalRef(text string) string {
	for _, ref := range internalRefRe.FindAllString(text, -1) {
		if prefix, _, _ := strings.Cut(ref, "-"); !standardNames[prefix] {
			return ref
		}
	}
	return ""
}

func TestInternalRefPattern(t *testing.T) {
	for _, bad := range []string{"see #449", "(INT-019)", "GO-046 done", "RAG-011"} {
		if internalRef(bad) == "" {
			t.Errorf("pattern misses %q", bad)
		}
	}
	for _, good := range []string{"Geo search", "SHA-256 digests", "AES-256", "e.g. #3B82F6", "/v1/vector_stores/{id}/search"} {
		if internalRef(good) != "" {
			t.Errorf("pattern flags plain text %q", good)
		}
	}
}

// Every module has a title, a one-sentence summary and a technical
// description, none of them with an internal reference.
func TestFeatureTextsArePlain(t *testing.T) {
	for _, d := range featureRegistry {
		if d.Title == "" || d.Summary == "" || d.Description == "" {
			t.Errorf("%s: title, summary and description are all required", d.Name)
		}
		if !strings.HasSuffix(d.Summary, ".") || strings.Count(d.Summary, ". ") > 0 {
			t.Errorf("%s: summary must be one sentence ending in a period: %q", d.Name, d.Summary)
		}
		if strings.Contains(d.Title, ":") {
			t.Errorf("%s: title is a name, not a sentence: %q", d.Name, d.Title)
		}
		for field, text := range map[string]string{"title": d.Title, "summary": d.Summary, "description": d.Description} {
			assertPlainText(t, d.Name+" "+field, text)
		}
	}
}

// The API carries the three texts, so the panel and MCP clients get them.
func TestFeatureStateCarriesTexts(t *testing.T) {
	st, ok := (*FeatureManager)(nil).State(FeatureGeo)
	if !ok || st.Title != "Geo search" || st.Summary == "" || st.Description == "" {
		t.Fatalf("state = %+v", st)
	}
	p := featureToProto(st)
	if p.GetTitle() != st.Title || p.GetSummary() != st.Summary || p.GetDescription() != st.Description {
		t.Errorf("proto = %+v", p)
	}
}

// The endpoint catalogue the panel's API Endpoints page shows, and every
// built-in MCP tool with its parameters, read as plain text.
func TestEndpointAndToolTextsArePlain(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rec := httptest.NewRecorder()
	s.handleEndpoints(rec, httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("endpoints: %d", rec.Code)
	}
	assertPlainJSON(t, "/v1/endpoints", rec.Body.Bytes())

	tools, err := json.Marshal(mcpBuiltinTools())
	if err != nil {
		t.Fatal(err)
	}
	assertPlainJSON(t, "MCP tools", tools)
}

// assertPlainJSON walks a JSON document and checks every "description".
func assertPlainJSON(t *testing.T, where string, body []byte) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				if text, isText := child.(string); isText && k == "description" {
					assertPlainText(t, where+path, text)
					continue
				}
				walk(path+"."+k, child)
			}
		case []any:
			for _, child := range x {
				walk(path, child)
			}
		}
	}
	walk("", doc)
}
