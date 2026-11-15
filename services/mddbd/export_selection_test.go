package main

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// FE-014: /v1/export takes an optional `keys` list so the panel can export
// the documents selected in its list. Without keys nothing changes.

// exportedKeys reads the key of every NDJSON line, in order.
func exportedKeys(t *testing.T, body string) []string {
	t.Helper()
	var keys []string
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line == "" {
			continue
		}
		var d struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("line is not JSON: %v\n%s", err, line)
		}
		keys = append(keys, d.Key)
	}
	return keys
}

func TestExportKeysSelectsTheListedDocumentsInOrderOnce(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha", "beta", "gamma")

	rec := exportRequest(t, srv, ExportRequest{Collection: "docs", Format: "ndjson", Keys: []DocRef{
		{Key: "gamma", Lang: "en"},
		{Key: "missing", Lang: "en"}, // skipped, not an error
		{Key: "alpha", Lang: "en"},
		{Key: "gamma", Lang: "en"}, // listed twice, exported once
		{Key: "beta", Lang: "de"},  // another language: no such document
	}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := exportedKeys(t, rec.Body.String()), []string{"gamma", "alpha"}; !reflect.DeepEqual(got, want) {
		t.Errorf("exported %v, want %v", got, want)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="docs.ndjson"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestExportKeysAsZipHoldsOnlyTheSelection(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha", "beta")

	rec := exportRequest(t, srv, ExportRequest{Collection: "docs", Format: "zip", Keys: []DocRef{{Key: "beta", Lang: "en"}}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || !strings.HasPrefix(zr.File[0].Name, "beta.") {
		names := make([]string, len(zr.File))
		for i, f := range zr.File {
			names[i] = f.Name
		}
		t.Errorf("archive holds %v, want only beta", names)
	}
}

// Keys and filterMeta together: the listed documents that also match.
func TestExportKeysAreNarrowedByTheFilter(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha", "beta")
	keys := []DocRef{{Key: "beta", Lang: "en"}, {Key: "alpha", Lang: "en"}}

	match, err := client.collectExportDocs(&MCPExportRequest{Collection: "docs", Keys: keys,
		FilterMeta: map[string][]string{"kind": {"prose"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(match) != 2 || match[0].Key != "beta" || match[1].Key != "alpha" {
		t.Errorf("a matching filter changed the selection: %+v", match)
	}

	miss, err := client.collectExportDocs(&MCPExportRequest{Collection: "docs", Keys: keys,
		FilterMeta: map[string][]string{"kind": {"poetry"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(miss) != 0 {
		t.Errorf("a filter matching nothing still exported %d documents", len(miss))
	}
}

// An entry without a key or a language names no document: both formats
// refuse the request with 400 before anything is written.
func TestExportRefusesAKeyWithoutKeyOrLang(t *testing.T) {
	_, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha")

	for _, format := range []string{"ndjson", "zip"} {
		rec := exportRequest(t, srv, ExportRequest{Collection: "docs", Format: format,
			Keys: []DocRef{{Key: "alpha", Lang: "en"}, {Key: "alpha"}}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", format, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "keys[1]") {
			t.Errorf("%s: the error does not name the entry: %s", format, rec.Body.String())
		}
	}
}

func TestValidateDocRefs(t *testing.T) {
	if err := validateDocRefs(nil); err != nil {
		t.Errorf("no keys: %v", err)
	}
	if err := validateDocRefs([]DocRef{{Key: "k", Lang: "en"}}); err != nil {
		t.Errorf("a full entry: %v", err)
	}
	if err := validateDocRefs([]DocRef{{Lang: "en"}}); err == nil || !strings.Contains(err.Error(), "keys[0]") {
		t.Errorf("a missing key: %v", err)
	}
}

func TestMCPGetDocRefs(t *testing.T) {
	if got := mcpGetDocRefs(map[string]interface{}{}, "keys"); got != nil {
		t.Errorf("absent = %v, want nil", got)
	}
	if got := mcpGetDocRefs(map[string]interface{}{"keys": "alpha"}, "keys"); got != nil {
		t.Errorf("not an array = %v, want nil", got)
	}
	got := mcpGetDocRefs(map[string]interface{}{"keys": []interface{}{
		map[string]interface{}{"key": "alpha", "lang": "en"},
		"beta",
	}}, "keys")
	want := []DocRef{{Key: "alpha", Lang: "en"}, {}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The export_documents tool hands its keys to the client, and an entry that
// is not a {key, lang} object fails the call rather than being dropped.
func TestExportDocumentsToolPassesKeys(t *testing.T) {
	client, srv, cleanup := directClientServer(t)
	defer cleanup()
	seedDocs(t, srv, "docs", "alpha")
	ts := &MCPToolServer{client: client, globalMode: ModeRW}

	if _, err := ts.mcpCallTool(context.Background(), "export_documents", map[string]interface{}{
		"collection": "docs",
		"keys":       []interface{}{map[string]interface{}{"key": "alpha", "lang": "en"}},
	}); err != nil {
		t.Errorf("a valid key list: %v", err)
	}

	_, err := ts.mcpCallTool(context.Background(), "export_documents", map[string]interface{}{
		"collection": "docs",
		"keys":       []interface{}{"alpha"},
	})
	if err == nil || !strings.Contains(err.Error(), "keys[0]") {
		t.Errorf("an entry that is not an object: err = %v", err)
	}
}

// The tool advertises keys, so an MCP client can discover it.
func TestExportDocumentsToolSchemaListsKeys(t *testing.T) {
	for _, tool := range mcpBuiltinTools() {
		if tool.Name != "export_documents" {
			continue
		}
		props := tool.InputSchema["properties"].(map[string]interface{})
		if _, ok := props["keys"]; !ok {
			t.Error("export_documents does not describe keys")
		}
		return
	}
	t.Fatal("export_documents is not a built-in tool")
}
