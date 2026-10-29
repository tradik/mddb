package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates files under dir; a nil value is a directory.
func writeTree(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func sampleTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string][]byte{
		"main.go":                  []byte("package main\n\nfunc main() {}\n"),
		"README.md":                []byte("# Hi\n"),
		"notes.txt":                []byte("plain\n"),
		"lib/include/net/conn.h":   []byte("int conn(void);\n"),
		"lib/src/conn.c":           []byte("#include <net/conn.h>\nint conn(void) { return 0; }\n"),
		"node_modules/x/index.js":  []byte("module.exports = 1\n"),
		"image.png":                {0x89, 'P', 'N', 'G', 0, 0, 1},
		"big.log":                  []byte(strings.Repeat("x", 2048)),
		"docs/skip-me/secret.md":   []byte("no\n"),
		"dist/bundle.js":           []byte("bundled\n"),
		"scripts/tool.py":          []byte("def run():\n    pass\n"),
		"scripts/.hidden/keep.txt": []byte("kept\n"),
	})
	return dir
}

// autoindexRoutes answers every endpoint autoindex calls.
func autoindexRoutes() map[string]interface{} {
	return map[string]interface{}{
		"/v1/code-languages":        `{"extensions":{".go":"go",".c":"c",".h":"c",".py":"python",".js":"javascript"},"languages":["c","go","javascript","python"]}`,
		"/v1/ingest":                `{"added":2,"updated":1,"skipped":3,"failed":0,"collection":"x"}`,
		"/v1/search-advisor":        `{"searchType":"hybrid"}`,
		"GET /v1/collection-config": `{"collection":"x","configured":true,"config":{"type":"default","retrieval":{"topK":5}}}`,
		"PUT /v1/collection-config": `{}`,
		"/v1/aggregate":             `{"collection":"x","totalDocs":3,"facets":{"path":[{"value":"main.go","count":1},{"value":"gone.go","count":1}]}}`,
		"/v1/delete-batch":          `{"deleted":1,"not_found":0,"failed":0,"errors":[]}`,
	}
}

func callsTo(fs *fakeServer, method, path string) []recordedRequest {
	var out []recordedRequest
	for _, c := range fs.calls() {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func TestAutoindex_DryRunWritesNothing(t *testing.T) {
	dir := sampleTree(t)
	fs := newFakeServer(t, autoindexRoutes())
	out, err := runCLI(t, fs.URL, "autoindex", "--dry-run", "--max-file-size", "1024", "--exclude", "docs/skip-me/**", "--prune", "-c", "proj-x", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`collection "proj-x"`, "+ main.go (code go)", "+ README.md (doc )", "+ notes.txt (text )",
		"- image.png: binary", "- big.log: larger than --max-file-size", "- docs/skip-me/secret.md: excluded", "x gone.go",
		"+ scripts/.hidden/keep.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output misses %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"node_modules", "dist/bundle.js"} {
		if strings.Contains(out, bad) {
			t.Errorf("default-excluded %s listed:\n%s", bad, out)
		}
	}
	for _, c := range fs.calls() {
		if c.Method != "GET" && c.Path != "/v1/aggregate" {
			t.Errorf("dry run sent %s %s", c.Method, c.Path)
		}
	}
}

func TestAutoindex_RunIngestsPrunesAndAdvises(t *testing.T) {
	dir := sampleTree(t)
	fs := newFakeServer(t, autoindexRoutes())
	out, err := runCLI(t, fs.URL, "autoindex", "--prune", "--profile", "fast", "--lang", "pl", "-c", "proj", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "✓ proj:") || !strings.Contains(out, "2 added, 1 updated, 3 unchanged, 0 failed, 1 pruned") {
		t.Fatalf("summary: %s", out)
	}
	ingest := callsTo(fs, "POST", "/v1/ingest")
	if len(ingest) != 1 {
		t.Fatalf("ingest calls: %d", len(ingest))
	}
	body := ingest[0].Body
	opts := asMap(body["options"])
	if opts["skipDuplicates"] != true || opts["profile"] != "fast" {
		t.Fatalf("options: %v", opts)
	}
	byKey := map[string]map[string]interface{}{}
	for _, d := range body["documents"].([]interface{}) {
		doc := asMap(d)
		byKey[asString(doc["key"])] = doc
		if doc["lang"] != "pl" {
			t.Fatalf("lang: %v", doc)
		}
	}
	meta := asMap(byKey["main.go"]["meta"])
	for k, want := range map[string]string{"kind": "code", "language": "go", "path": "main.go", "extension": ".go"} {
		if v := meta[k].([]interface{}); v[0] != want {
			t.Errorf("meta %s = %v, want %s", k, v, want)
		}
	}
	if _, ok := meta["defines"]; ok {
		t.Error("symbols are the server's to extract, not the client's to send")
	}
	if asMap(byKey["README.md"]["meta"])["kind"].([]interface{})[0] != "doc" {
		t.Error("markdown is kind doc")
	}
	put := callsTo(fs, "PUT", "/v1/collection-config")
	if len(put) != 1 || !strings.Contains(fmt.Sprint(put[0].Body["code"]), "lib/include") || asMap(put[0].Body["retrieval"])["topK"] != float64(5) {
		t.Fatalf("include paths written, rest of the config kept: %+v", put)
	}
	del := callsTo(fs, "POST", "/v1/delete-batch")
	if len(del) != 1 || !strings.Contains(fmt.Sprint(del[0].Body["documents"]), "gone.go") {
		t.Fatalf("prune: %+v", del)
	}
	if adv := callsTo(fs, "POST", "/v1/search-advisor"); len(adv) != 1 || adv[0].Body["apply"] != true {
		t.Fatalf("advisor: %+v", adv)
	}
}

func TestAutoindex_JSONAndBatching(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{}
	for i := 0; i < autoindexBatchDocs+5; i++ {
		files[fmt.Sprintf("f%03d.go", i)] = []byte("package x\n")
	}
	writeTree(t, dir, files)
	fs := newFakeServer(t, autoindexRoutes())
	out, err := runCLI(t, fs.URL, "--json", "autoindex", dir)
	if err != nil {
		t.Fatal(err)
	}
	var rep autoindexReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if rep.Files != autoindexBatchDocs+5 || rep.Added != 4 || rep.Collection != sanitizeCollection(filepath.Base(dir)) {
		t.Fatalf("report: %+v", rep)
	}
	if n := len(callsTo(fs, "POST", "/v1/ingest")); n != 2 {
		t.Fatalf("batches: %d", n)
	}
	if len(callsTo(fs, "PUT", "/v1/collection-config")) != 0 {
		t.Fatal("no C headers, no include paths")
	}
}

func TestAutoindex_Failures(t *testing.T) {
	dir := sampleTree(t)
	routes := map[string]interface{}{
		"/v1/code-languages":    failure{404, `{"error":"no"}`},
		"/v1/ingest":            failure{500, `{"error":"disk full"}`},
		"/v1/search-advisor":    failure{500, `{"error":"advisor down"}`},
		"/v1/collection-config": failure{500, `{"error":"config down"}`},
	}
	fs := newFakeServer(t, routes)
	out, err := runCLI(t, fs.URL, "autoindex", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"code languages unavailable", "disk full", "advisor down"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "failed") {
		t.Fatal(out)
	}
	// Without the table no kind is sent for code: the server classifies on read.
	ingest := callsTo(fs, "POST", "/v1/ingest")
	for _, d := range ingest[0].Body["documents"].([]interface{}) {
		doc := asMap(d)
		if doc["key"] == "main.go" {
			if _, has := asMap(doc["meta"])["kind"]; has {
				t.Fatal("kind sent without the server's table")
			}
		}
	}

	routes4 := autoindexRoutes()
	routes4["GET /v1/collection-config"] = failure{500, `{"error":"config down"}`}
	fs4 := newFakeServer(t, routes4)
	if out, _ := runCLI(t, fs4.URL, "autoindex", dir); !strings.Contains(out, "code.includePaths") || !strings.Contains(out, "config down") {
		t.Fatalf("config failure reported: %s", out)
	}

	if _, err := runCLI(t, fs.URL, "autoindex", filepath.Join(dir, "main.go")); err == nil {
		t.Fatal("a file is not a directory")
	}
	fs2 := newFakeServer(t, map[string]interface{}{"/v1/aggregate": failure{500, "{}"}, "/v1/code-languages": `{}`})
	if _, err := runCLI(t, fs2.URL, "autoindex", "--prune", dir); err == nil {
		t.Fatal("prune cannot run without the path list")
	}
	fs3 := newFakeServer(t, map[string]interface{}{
		"/v1/code-languages": `{}`, "/v1/ingest": `{"added":1}`, "/v1/search-advisor": `{}`,
		"/v1/aggregate": `{"facets":{"path":[{"value":"gone","count":1}]}}`, "/v1/delete-batch": failure{500, `{"error":"nope"}`},
		"/v1/collection-config": `{}`,
	})
	if out, _ := runCLI(t, fs3.URL, "autoindex", "--prune", dir); !strings.Contains(out, "prune:") {
		t.Fatalf("delete failure reported: %s", out)
	}
}

func TestAutoindex_GitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := sampleTree(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeTree(t, dir, map[string][]byte{".gitignore": []byte("notes.txt\n"), "untracked.go": []byte("package x\n")})
	git("init", "-q", "-b", "trunk")
	git("add", "main.go", ".gitignore")
	git("commit", "-q", "-m", "init")
	git("remote", "add", "origin", "git@github.com:acme/Widget-Shop.git")

	fs := newFakeServer(t, autoindexRoutes())
	out, err := runCLI(t, fs.URL, "--json", "autoindex", "--dry-run", dir)
	if err != nil {
		t.Fatal(err)
	}
	var rep autoindexReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if rep.Collection != "widget-shop" || rep.Git == nil || rep.Git.Branch != "trunk" || len(rep.Git.Commit) != 40 {
		t.Fatalf("git info: %+v", rep)
	}
	paths := fmt.Sprint(rep.Plan)
	if strings.Contains(paths, "notes.txt") || !strings.Contains(paths, "untracked.go") || !strings.Contains(paths, "node_modules") {
		// In a repository git decides: ignored files are out, untracked ones in,
		// and the default directory list does not apply.
		t.Fatalf("plan: %s", paths)
	}

	fs2 := newFakeServer(t, autoindexRoutes())
	if _, err := runCLI(t, fs2.URL, "autoindex", "-c", "w", dir); err != nil {
		t.Fatal(err)
	}
	doc := asMap(callsTo(fs2, "POST", "/v1/ingest")[0].Body["documents"].([]interface{})[0])
	meta := asMap(doc["meta"])
	if meta["gitBranch"].([]interface{})[0] != "trunk" || len(meta["gitCommit"].([]interface{})[0].(string)) != 40 {
		t.Fatalf("git meta: %v", meta)
	}
}

func TestAutoindex_Helpers(t *testing.T) {
	cases := map[string]string{"My Repo!": "my-repo", "mddb.git": "mddb", "": "default", "___": "___",
		strings.Repeat("a", 80): strings.Repeat("a", 64)}
	for in, want := range cases {
		if got := sanitizeCollection(in); got != want {
			t.Errorf("sanitizeCollection(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"https://github.com/tradik/mddb/": "mddb", "git@host:a/b.git": "b", "plain": "plain"} {
		if got := repoName(in); got != want {
			t.Errorf("repoName(%q) = %q", in, got)
		}
	}
	if !matchAny([]string{"*.go"}, "a/b.go") || !matchAny([]string{"a/**"}, "a/b/c") || matchAny([]string{"x"}, "y") {
		t.Fatal("matchAny")
	}
	if _, binary := sniffFile(filepath.Join(t.TempDir(), "missing")); !binary {
		t.Fatal("an unreadable file is treated as binary")
	}
	c := classifier{include: []string{"*.go"}, maxSize: 10}
	if _, skip := c.classify(t.TempDir(), "a.md"); skip == nil || skip.Reason != "not included" {
		t.Fatalf("include: %+v", skip)
	}
	if _, skip := (classifier{maxSize: 10}).classify(t.TempDir(), "missing.go"); skip == nil || skip.Reason != "unreadable" {
		t.Fatalf("missing: %+v", skip)
	}
	link := t.TempDir()
	_ = os.Symlink("/etc/hostname", filepath.Join(link, "l"))
	if _, skip := (classifier{maxSize: 10}).classify(link, "l"); skip == nil || skip.Reason != "not a regular file" {
		t.Fatalf("symlink: %+v", skip)
	}
	if detectGit(t.TempDir()) != nil {
		t.Fatal("a plain directory is not a repository")
	}
	if _, err := gitFiles(t.TempDir()); err == nil {
		t.Fatal("ls-files outside a repository")
	}
	if err := setIncludePaths(context.Background(), newClient(), "x", nil); err != nil {
		t.Fatal("no dirs, no call")
	}
}

func TestAutoindex_BatchBytesAndReadErrors(t *testing.T) {
	dir := t.TempDir()
	chunk := []byte(strings.Repeat("a", 1500<<10)) // 1.5 MiB each: three fill more than one 4 MiB batch
	writeTree(t, dir, map[string][]byte{"a.txt": chunk, "b.txt": chunk, "c.txt": chunk})
	fs := newFakeServer(t, autoindexRoutes())
	if _, err := runCLI(t, fs.URL, "autoindex", "--max-file-size", "2000000", dir); err != nil {
		t.Fatal(err)
	}
	if n := len(callsTo(fs, "POST", "/v1/ingest")); n != 2 {
		t.Fatalf("batches by size: %d", n)
	}

	// A file that vanished between planning and sending is counted, not fatal.
	rep := &autoindexReport{Collection: "x"}
	serverURL = fs.URL
	ingestFiles(context.Background(), newClient(), rep, []autoFile{{Rel: "gone.txt", abs: filepath.Join(dir, "gone.txt")}}, autoindexOptions{lang: "en"})
	if rep.Failed != 1 || !strings.Contains(strings.Join(rep.Errors, " "), "gone.txt") {
		t.Fatalf("read error: %+v", rep)
	}
}

func TestAutoindex_KeepsExistingIncludePaths(t *testing.T) {
	routes := autoindexRoutes()
	routes["GET /v1/collection-config"] = `{"collection":"x","config":{"code":{"includePaths":["src"]}}}`
	fs := newFakeServer(t, routes)
	if _, err := runCLI(t, fs.URL, "autoindex", sampleTree(t)); err != nil {
		t.Fatal(err)
	}
	if len(callsTo(fs, "PUT", "/v1/collection-config")) != 0 {
		t.Fatal("include paths the operator set must be kept")
	}
}

// Without git on PATH there is no repository to describe, and no error.
func TestAutoindex_NoGitOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := runGit(t.TempDir(), "status"); err == nil {
		t.Fatal("git found on an empty PATH")
	}
	if detectGit(t.TempDir()) != nil {
		t.Fatal("no git, no repository")
	}
}
