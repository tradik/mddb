package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// OPS-025: mddb-cli diagnose saves the bundle, or with --inspect only shows
// its manifest; either way it checks every file against the manifest.

// tarGz packs files, in order, into a tar.gz.
func tarGz(t *testing.T, files ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testBundle is a bundle with one file; sum overrides its manifest hash.
func testBundle(t *testing.T, sum string) []byte {
	t.Helper()
	body := `{"version":"2.18.10"}`
	if sum == "" {
		s := sha256.Sum256([]byte(body))
		sum = hex.EncodeToString(s[:])
	}
	man, _ := json.Marshal(map[string]any{
		"version": "2.18.10", "createdAt": "2026-10-30T12:00:00Z",
		"files":         []map[string]any{{"name": "build.json", "size": len(body), "sha256": sum, "redaction": "none"}},
		"omitted":       []map[string]any{{"name": "runtime.json", "reason": "opt-in: profiles"}},
		"neverIncluded": []string{"document content"},
	})
	return tarGz(t, [2]string{"MANIFEST.json", string(man)}, [2]string{"build.json", body})
}

func TestDiagnoseWritesTheBundle(t *testing.T) {
	bundle := testBundle(t, "")
	srv := newFakeServer(t, map[string]interface{}{"GET /v1/admin/diagnose?integrity=true&keepNames=true&profiles=true": string(bundle)})
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	stdout, err := runCLI(t, srv.URL, "diagnose", "--out", out, "--profiles", "--integrity", "--keep-names")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out) // #nosec G304 -- a test temp file
	if err != nil || !bytes.Equal(got, bundle) {
		t.Fatalf("written %d bytes, %v", len(got), err)
	}
	mustContain(t, stdout, "Diagnose bundle written: "+out)
	mustContain(t, stdout, "1 files")
}

func TestDiagnoseDefaultNameAndJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	srv := newFakeServer(t, map[string]interface{}{"/v1/admin/diagnose": string(testBundle(t, ""))})
	stdout, err := runCLI(t, srv.URL, "diagnose", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		File  string `json:"file"`
		Files int    `json:"files"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || res.Files != 1 ||
		!strings.HasPrefix(res.File, "mddb-diagnose-127.0.0.1-") {
		t.Fatalf("%q %v", stdout, err)
	}
	if _, err := os.Stat(res.File); err != nil {
		t.Fatal(err)
	}
}

func TestDiagnoseInspectWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	srv := newFakeServer(t, map[string]interface{}{"/v1/admin/diagnose": string(testBundle(t, ""))})
	stdout, err := runCLI(t, srv.URL, "diagnose", "--inspect")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MDDB 2.18.10", "build.json", "Not included:", "runtime.json", "opt-in: profiles", "Never included:", "document content"} {
		mustContain(t, stdout, want)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("--inspect wrote %v", entries)
	}
	stdout, err = runCLI(t, srv.URL, "diagnose", "--inspect", "--json")
	if err != nil || !strings.Contains(stdout, `"neverIncluded":["document content"]`) {
		t.Fatalf("--inspect --json: %q %v", stdout, err)
	}
}

func TestDiagnoseRefusesWhatIsNotABundle(t *testing.T) {
	noManifest := tarGz(t, [2]string{"build.json", "{}"})
	badManifest := tarGz(t, [2]string{"MANIFEST.json", "{"})
	truncated := testBundle(t, "")
	var cut bytes.Buffer
	zw := gzip.NewWriter(&cut)
	_, _ = zw.Write([]byte("not a tar archive, just some bytes that end too soon"))
	_ = zw.Close()
	for name, c := range map[string]struct {
		route interface{}
		want  string
	}{
		"forbidden":      {failure{403, `{"error":"forbidden"}`}, "403"},
		"not gzip":       {"plain text", "not a diagnose bundle"},
		"not tar":        {cut.String(), "not a diagnose bundle"},
		"no manifest":    {string(noManifest), "no MANIFEST.json"},
		"bad manifest":   {string(badManifest), "unreadable MANIFEST.json"},
		"damaged":        {string(testBundle(t, "00")), "damaged"},
		"truncated file": {string(truncated[:len(truncated)-30]), "not a diagnose bundle"},
	} {
		srv := newFakeServer(t, map[string]interface{}{"/v1/admin/diagnose": c.route})
		if _, err := runCLI(t, srv.URL, "diagnose", "--inspect"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	srv := newFakeServer(t, map[string]interface{}{"/v1/admin/diagnose": string(testBundle(t, ""))})
	if _, err := runCLI(t, srv.URL, "diagnose", "--out", filepath.Join(t.TempDir(), "no", "such", "dir", "b.tgz")); err == nil {
		t.Error("an unwritable --out was not an error")
	}
}

func TestBundleHost(t *testing.T) {
	for server, want := range map[string]string{
		"http://db.example.com:11023": "db.example.com", "http://[::1]:1": "_1", "::bad": "server", "unix:///x": "server",
	} {
		if got := bundleHost(server); got != want {
			t.Errorf("bundleHost(%q) = %q, want %q", server, got, want)
		}
	}
}
