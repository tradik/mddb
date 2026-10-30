package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

// AGT-005: mddb-cli corpus build, install, list and remove.

func TestCorpusBuildWritesThePack(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/corpus/build": "PACK BYTES"})
	t.Chdir(t.TempDir())
	out, err := runCLI(t, fs.URL, "corpus", "build", "docs", "--license", "MIT", "--version", "1.0",
		"--source", "MIT=https://github.com/org/repo@abc123", "--source", "MIT=git@github.com:org/repo")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "docs-1.0.mddbpack")
	if got, _ := os.ReadFile("docs-1.0.mddbpack"); string(got) != "PACK BYTES" {
		t.Fatalf("pack file holds %q", got)
	}
	body := fs.lastCall(t).Body
	assertBodyField(t, body, "license", "MIT")
	assertBodyField(t, body, "sources", []map[string]string{
		{"repo": "https://github.com/org/repo", "commit": "abc123", "license": "MIT"},
		{"repo": "git@github.com:org/repo", "license": "MIT"},
	})
	if _, err := runCLI(t, fs.URL, "corpus", "build", "docs", "--license", "MIT"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("docs.mddbpack"); err != nil {
		t.Fatal("no default file name without a version")
	}
}

func TestCorpusBuildRefusals(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/corpus/build": failure{http.StatusNotFound, `{"error":"no documents"}`},
	})
	if _, err := runCLI(t, fs.URL, "corpus", "build", "docs"); err == nil || !strings.Contains(err.Error(), "--license") {
		t.Fatalf("built without a licence: %v", err)
	}
	if _, err := runCLI(t, fs.URL, "corpus", "build", "docs", "--license", "MIT", "--source", "nolicence"); err == nil {
		t.Fatal("a malformed source was sent")
	}
	if len(fs.calls()) != 0 {
		t.Fatal("a refused build reached the server")
	}
	if _, err := runCLI(t, fs.URL, "corpus", "build", "docs", "--license", "MIT"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("the server's refusal was lost: %v", err)
	}
	ok := newFakeServer(t, map[string]interface{}{"/v1/corpus/build": "x"})
	if _, err := runCLI(t, ok.URL, "corpus", "build", "docs", "--license", "MIT", "-o", filepath.Join(t.TempDir(), "no", "x")); err == nil {
		t.Fatal("wrote into a directory that does not exist")
	}
}

func TestCorpusListAndRemove(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/corpus": `{"packs":[{"collection":"docs","corpus":{"version":"1.0","docCount":3,"license":"MIT"}},` +
			`{"collection":"k8s","corpus":{"docCount":9,"license":"CC-BY-4.0","vectorsDisabled":true}}],"total":2}`,
		"/v1/corpus/remove": `{"status":"removed","name":"docs","deleted":3}`,
	})
	out, err := runCLI(t, fs.URL, "corpus", "list")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "docs\t1.0\t3 docs\tMIT\tvectors")
	mustContain(t, out, "k8s\t\t9 docs\tCC-BY-4.0\tfull text only")
	if out, _ := runCLI(t, fs.URL, "-j", "corpus", "list"); !strings.Contains(out, `"total":2`) {
		t.Fatalf("--json: %s", out)
	}
	out, err = runCLI(t, fs.URL, "corpus", "remove", "docs")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Removed pack docs (3 documents)")
	assertBodyField(t, fs.lastCall(t).Body, "name", "docs")

	empty := newFakeServer(t, map[string]interface{}{"/v1/corpus": `{"packs":[]}`})
	if out, _ := runCLI(t, empty.URL, "corpus", "list"); !strings.Contains(out, "No corpus packs installed.") {
		t.Fatalf("empty list: %s", out)
	}
	broken := newFakeServer(t, map[string]interface{}{
		"/v1/corpus":        "not json",
		"/v1/corpus/remove": failure{http.StatusNotFound, `{"error":"no pack"}`},
	})
	if _, err := runCLI(t, broken.URL, "corpus", "list"); err == nil {
		t.Fatal("an unreadable list was accepted")
	}
	if _, err := runCLI(t, broken.URL, "corpus", "remove", "docs"); err == nil {
		t.Fatal("a failed remove was reported as done")
	}
	if _, err := runCLI(t, "http://127.0.0.1:1", "corpus", "list"); err == nil {
		t.Fatal("an unreachable server was not reported")
	}
}

// installServer accepts an upload and answers with what it received.
func installServer(t *testing.T, status int, answer string) (*httptest.Server, *string) {
	t.Helper()
	got := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("pack")
		if err == nil {
			b, _ := io.ReadAll(f)
			*got = string(b)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func writePack(t *testing.T, content string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docs.mddbpack")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func TestCorpusInstallUploadsAFile(t *testing.T) {
	srv, got := installServer(t, http.StatusOK,
		`{"name":"docs","version":"1.0","docCount":3,"license":"MIT","warnings":["vectors made by another model"]}`)
	path, sum := writePack(t, "PACK")
	out, err := runCLI(t, srv.URL, "corpus", "install", path, "--sha256", strings.ToUpper(sum))
	if err != nil {
		t.Fatal(err)
	}
	if *got != "PACK" {
		t.Fatalf("the server received %q", *got)
	}
	mustContain(t, out, "Installed pack docs 1.0: 3 documents, licence MIT")
	mustContain(t, out, "! vectors made by another model")
	if out, _ := runCLI(t, srv.URL, "-j", "corpus", "install", path); !strings.Contains(out, `"docCount":3`) {
		t.Fatalf("--json: %s", out)
	}
}

func TestCorpusInstallRefusals(t *testing.T) {
	srv, got := installServer(t, http.StatusOK, "not json")
	path, _ := writePack(t, "PACK")
	if _, err := runCLI(t, srv.URL, "corpus", "install", path, "--sha256", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("a wrong checksum was accepted: %v", err)
	}
	if *got != "" {
		t.Fatal("a pack with a wrong checksum was uploaded")
	}
	if _, err := runCLI(t, srv.URL, "corpus", "install", path); err == nil {
		t.Fatal("an unreadable answer was accepted")
	}
	missing := filepath.Join(t.TempDir(), "none.mddbpack")
	for _, args := range [][]string{{missing}, {missing, "--sha256", "00"}, {t.TempDir(), "--sha256", "00"}, {t.TempDir()}} {
		if _, err := runCLI(t, srv.URL, append([]string{"corpus", "install"}, args...)...); err == nil {
			t.Fatalf("install %v succeeded", args)
		}
	}
	refusing, _ := installServer(t, http.StatusConflict, `{"error":"taken"}`)
	if _, err := runCLI(t, refusing.URL, "corpus", "install", path); err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("the server's refusal was lost: %v", err)
	}
}

func TestCorpusInstallFromAURL(t *testing.T) {
	pack := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs.mddbpack":
			_, _ = io.WriteString(w, "PACK")
		case "/plain":
			http.Redirect(w, r, "http://example.org/x", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pack.Close()
	orig := corpusHTTPClient
	client := pack.Client()
	client.CheckRedirect = orig.CheckRedirect
	corpusHTTPClient = client
	t.Cleanup(func() { corpusHTTPClient = orig })

	srv, got := installServer(t, http.StatusOK, `{"name":"docs","docCount":1,"license":"MIT","warnings":[]}`)
	sum := sha256.Sum256([]byte("PACK"))
	if _, err := runCLI(t, srv.URL, "corpus", "install", pack.URL+"/docs.mddbpack", "--sha256", hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if *got != "PACK" {
		t.Fatalf("uploaded %q", *got)
	}
	for _, url := range []string{"http://example.org/x.mddbpack", pack.URL + "/missing", pack.URL + "/plain", "https://%zz"} {
		if _, err := runCLI(t, srv.URL, "corpus", "install", url); err == nil {
			t.Fatalf("installed from %s", url)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "none"))
	if _, err := runCLI(t, srv.URL, "corpus", "install", pack.URL+"/docs.mddbpack"); err == nil {
		t.Fatal("downloaded without a temporary directory")
	}
}

func TestSaveStreamLeavesNothingOnFailure(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := saveStream(filepath.Join(dir, "p.mddbpack"), iotest.ErrReader(io.ErrUnexpectedEOF)); err == nil {
		t.Fatal("a broken stream was saved")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}
