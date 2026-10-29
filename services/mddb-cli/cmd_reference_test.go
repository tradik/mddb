package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubClone replaces cloning with writing a small project into the target.
func stubClone(t *testing.T, license string) {
	t.Helper()
	origClone, origRefresh := gitClone, gitRefresh
	t.Cleanup(func() { gitClone, gitRefresh = origClone, origRefresh })
	gitClone = func(url, ref, dir string) error {
		files := map[string][]byte{"tree.go": []byte("package tree\n\nfunc Delete() {}\n")}
		if license != "" {
			files["LICENSE"] = []byte(license)
		}
		writeTree(t, dir, files)
		return nil
	}
	gitRefresh = func(dir, ref string) error {
		writeTree(t, dir, map[string][]byte{"new.go": []byte("package tree\n")})
		return nil
	}
}

func referenceRoutes() map[string]interface{} {
	r := autoindexRoutes()
	r["POST /v1/cross-search"] = `{"passages":[{"collection":"ref-acme-tree","path":"tree.go","lines":{"start":3,"end":3},"symbol":"Delete","content":"func Delete() {}\nline2\nline3\nline4"}],"total":1}`
	r["GET /v1/collection-configs"] = `{"configs":[{"collection":"ref-acme-tree","config":{"description":"Reference: x","code":{"role":"reference"}}},{"collection":"mine","config":{}}],"total":2}`
	r["/v1/delete-collection"] = `{"deleted":true}`
	return r
}

func TestReference_AddListSearchUpdateRemove(t *testing.T) {
	t.Setenv("MDDB_REFERENCES_DIR", t.TempDir())
	stubClone(t, "Permission is hereby granted, free of charge, to any person")
	fs := newFakeServer(t, referenceRoutes())

	out, err := runCLI(t, fs.URL, "reference", "add", "github.com/acme/tree", "--ref", "v1")
	if err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	put := callsTo(fs, "PUT", "/v1/collection-config")
	if len(put) == 0 || asMap(put[0].Body["code"])["role"] != "reference" || !strings.Contains(asString(put[0].Body["description"]), "licence: MIT") {
		t.Fatalf("collection marked as reference: %+v", put)
	}
	doc := asMap(callsTo(fs, "POST", "/v1/ingest")[0].Body["documents"].([]interface{})[0])
	meta := asMap(doc["meta"])
	if meta["license"].([]interface{})[0] != "MIT" || meta["sourceRepository"].([]interface{})[0] != "https://github.com/acme/tree" {
		t.Fatalf("meta: %v", meta)
	}
	for _, d := range callsTo(fs, "POST", "/v1/ingest")[0].Body["documents"].([]interface{}) {
		if asMap(d)["key"] == referenceStateFile {
			t.Fatal("the clone's state file must not be indexed")
		}
	}
	if _, err := runCLI(t, fs.URL, "reference", "add", "github.com/acme/tree"); err == nil {
		t.Fatal("adding twice must point at update")
	}

	out, _ = runCLI(t, fs.URL, "reference", "list")
	if !strings.Contains(out, "ref-acme-tree\tReference: x") || strings.Contains(out, "mine") {
		t.Fatalf("list: %s", out)
	}
	out, _ = runCLI(t, fs.URL, "--json", "reference", "list")
	if !strings.Contains(out, `"Collection": "ref-acme-tree"`) {
		t.Fatalf("list json: %s", out)
	}

	out, _ = runCLI(t, fs.URL, "reference", "search", "delete a key", "--top-k", "3")
	if !strings.Contains(out, "ref-acme-tree tree.go:3-3 Delete") || strings.Contains(out, "line4") {
		t.Fatalf("search: %s", out)
	}
	body := callsTo(fs, "POST", "/v1/cross-search")[0].Body
	if body["searchType"] != "hybrid" || body["format"] != "passages" || body["targetCollections"].([]interface{})[0] != "role:reference" {
		t.Fatalf("search request: %v", body)
	}
	if out, _ := runCLI(t, fs.URL, "--json", "reference", "search", "x", "--collection", "ref-acme-tree"); !strings.Contains(out, `"passages"`) {
		t.Fatalf("search json: %s", out)
	}

	if out, err := runCLI(t, fs.URL, "reference", "update"); err != nil || !strings.Contains(out, "ref-acme-tree") {
		t.Fatalf("update all: %v %s", err, out)
	}
	out, err = runCLI(t, fs.URL, "reference", "remove", "ref-acme-tree")
	if err != nil || !strings.Contains(out, "Removed reference ref-acme-tree") {
		t.Fatalf("remove: %v %s", err, out)
	}
	if dir, _ := referenceDir("ref-acme-tree"); fileExists(dir) {
		t.Fatal("the clone must be gone")
	}
}

func TestReference_Failures(t *testing.T) {
	t.Setenv("MDDB_REFERENCES_DIR", t.TempDir())
	stubClone(t, "")
	fs := newFakeServer(t, referenceRoutes())

	if _, err := runCLI(t, fs.URL, "reference", "add", "file:///etc"); err == nil {
		t.Fatal("file:// must be refused")
	}
	gitClone = func(url, ref, dir string) error { return errors.New("network down") }
	if _, err := runCLI(t, fs.URL, "reference", "add", "https://x.org/a/b.git"); err == nil || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("clone failure: %v", err)
	}
	if _, err := runCLI(t, fs.URL, "reference", "update", "missing"); err == nil {
		t.Fatal("updating something that is not a clone")
	}
	if _, err := runCLI(t, fs.URL, "reference", "remove", "../escape"); err == nil {
		t.Fatal("a name must not leave the references root")
	}
	t.Setenv("MDDB_REFERENCES_DIR", filepath.Join(t.TempDir(), "never-created"))
	if _, err := runCLI(t, fs.URL, "reference", "update"); err == nil {
		t.Fatal("no references directory")
	}

	bad := newFakeServer(t, map[string]interface{}{"/v1/collection-config": failure{500, `{"error":"down"}`},
		"/v1/cross-search": failure{500, "{}"}, "/v1/collection-configs": `not json`, "/v1/delete-collection": failure{500, "{}"}})
	t.Setenv("MDDB_REFERENCES_DIR", t.TempDir())
	stubClone(t, "")
	if _, err := runCLI(t, bad.URL, "reference", "add", "https://x.org/a/b"); err == nil || !strings.Contains(err.Error(), "mark") {
		t.Fatalf("config failure: %v", err)
	}
	if _, err := runCLI(t, bad.URL, "reference", "search", "x"); err == nil {
		t.Fatal("search failure")
	}
	if _, err := runCLI(t, bad.URL, "reference", "list"); err == nil {
		t.Fatal("unparsable list")
	}
	if _, err := runCLI(t, bad.URL, "reference", "remove", "ref-a-b"); err == nil {
		t.Fatal("remove failure")
	}
	empty := newFakeServer(t, map[string]interface{}{"/v1/collection-configs": `{"configs":[]}`, "/v1/cross-search": `not json`})
	if out, _ := runCLI(t, empty.URL, "reference", "list"); !strings.Contains(out, "No reference collections") {
		t.Fatalf("empty list: %s", out)
	}
	if _, err := runCLI(t, empty.URL, "reference", "search", "x"); err == nil {
		t.Fatal("unparsable search")
	}
}

func TestReference_Helpers(t *testing.T) {
	ok := map[string]string{
		"https://github.com/a/b":   "https://github.com/a/b",
		"ssh://git@host/a/b.git":   "ssh://git@host/a/b.git",
		"git@github.com:a/b.git":   "git@github.com:a/b.git",
		"github.com/etcd-io/bbolt": "https://github.com/etcd-io/bbolt",
	}
	for in, want := range ok {
		if got, err := normalizeRepoURL(in); err != nil || got != want {
			t.Errorf("normalizeRepoURL(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "-oProxyCommand=x", "file:///etc", "http://x/a/b", "ext::sh -c x", "./local", "go.etcd.io"} {
		if _, err := normalizeRepoURL(in); err == nil {
			t.Errorf("normalizeRepoURL(%q) accepted", in)
		}
	}
	for in, want := range map[string]string{"https://github.com/etcd-io/bbolt": "ref-etcd-io-bbolt", "git@h:a/B.git": "ref-a-b", "https://h/x": "ref-h-x"} {
		if got := referenceName(in); got != want {
			t.Errorf("referenceName(%q) = %q, want %q", in, got, want)
		}
	}
	dir := t.TempDir()
	if detectLicense(dir) != "unknown" {
		t.Fatal("no file")
	}
	writeTree(t, dir, map[string][]byte{"COPYING": []byte("Something bespoke")})
	if detectLicense(dir) != "see COPYING" {
		t.Fatal("unrecognised")
	}
	writeTree(t, dir, map[string][]byte{"LICENSE": []byte("                                 Apache License\n")})
	if detectLicense(dir) != "Apache-2.0" {
		t.Fatal("apache")
	}
	if shortCommit("") != "unknown" || shortCommit("abc") != "abc" || shortCommit(strings.Repeat("a", 40)) != strings.Repeat("a", 12) {
		t.Fatal("shortCommit")
	}
	if _, err := readReferenceState(dir); err == nil {
		t.Fatal("no state file")
	}
	t.Setenv("MDDB_REFERENCES_DIR", "")
	if root, err := referencesRoot(); err != nil || !strings.HasSuffix(root, filepath.Join("mddb", "references")) {
		t.Fatalf("default root: %s %v", root, err)
	}
	if _, err := referenceDir(".."); err == nil {
		t.Fatal("..")
	}
	_ = os.Remove(dir)
}

func TestReference_RealGitCloneAndRefresh(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeTree(t, src, map[string][]byte{"a.go": []byte("package a\n")})
	git(src, "init", "-q", "-b", "main")
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "one")

	dst := filepath.Join(t.TempDir(), "clone")
	// The address check refuses local paths; the clone function itself is
	// what this exercises, with "file://" so git honours --depth.
	if err := gitClone("file://"+src, "main", dst); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(dst, "a.go")) {
		t.Fatal("clone")
	}
	writeTree(t, src, map[string][]byte{"b.go": []byte("package a\n")})
	git(src, "add", ".")
	git(src, "commit", "-q", "-m", "two")
	if err := gitRefresh(dst, ""); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(dst, "b.go")) {
		t.Fatal("refresh")
	}
	if err := gitRefresh(t.TempDir(), "main"); err == nil {
		t.Fatal("refresh outside a clone")
	}
}

func TestReference_MoreFailures(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MDDB_REFERENCES_DIR", root)
	stubClone(t, "")
	fs := newFakeServer(t, referenceRoutes())
	if _, err := runCLI(t, fs.URL, "reference", "add", "https://x.org/a/b", "--name", "../up"); err == nil {
		t.Fatal("--name must stay inside the root")
	}
	// The root is a file: nothing can be created under it.
	blocked := filepath.Join(t.TempDir(), "file")
	writeTree(t, filepath.Dir(blocked), map[string][]byte{"file": []byte("x")})
	t.Setenv("MDDB_REFERENCES_DIR", filepath.Join(blocked, "refs"))
	if _, err := runCLI(t, fs.URL, "reference", "add", "https://x.org/a/b"); err == nil {
		t.Fatal("unwritable root")
	}
	t.Setenv("MDDB_REFERENCES_DIR", root)
	// A clone that leaves no directory cannot record its state.
	gitClone = func(url, ref, dir string) error { return nil }
	if _, err := runCLI(t, fs.URL, "reference", "add", "https://x.org/c/d"); err == nil {
		t.Fatal("state cannot be written into a missing clone")
	}
	stubClone(t, "")
	if _, err := runCLI(t, fs.URL, "reference", "add", "https://x.org/e/f"); err != nil {
		t.Fatal(err)
	}
	gitRefresh = func(dir, ref string) error { return errors.New("offline") }
	if _, err := runCLI(t, fs.URL, "reference", "update", "ref-e-f"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("refresh failure: %v", err)
	}
	gitRefresh = func(dir, ref string) error { return os.Chmod(filepath.Join(dir, referenceStateFile), 0o400) }
	if _, err := runCLI(t, fs.URL, "reference", "update", "ref-e-f"); err == nil {
		t.Fatal("state cannot be rewritten in a read-only clone")
	}
	if _, err := runCLI(t, fs.URL, "reference", "update", "../x"); err == nil {
		t.Fatal("update name outside the root")
	}
	// remove without a state file falls back to the name as the collection.
	if out, err := runCLI(t, fs.URL, "reference", "remove", "plain"); err != nil || !strings.Contains(out, "Removed reference plain") {
		t.Fatalf("remove without state: %v %s", err, out)
	}
	if _, err := runCLI(t, fs.URL, "--json", "reference", "list"); err != nil {
		t.Fatal(err)
	}
}

func TestReference_EdgeBranches(t *testing.T) {
	// No cache directory: HOME and XDG_CACHE_HOME unset.
	t.Setenv("MDDB_REFERENCES_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	if _, err := referencesRoot(); err == nil {
		t.Fatal("no cache directory")
	}
	if _, err := referenceDir("x"); err == nil {
		t.Fatal("no root, no dir")
	}
	fs := newFakeServer(t, map[string]interface{}{"/v1/collection-configs": failure{500, "{}"}})
	if _, err := runCLI(t, fs.URL, "reference", "update"); err == nil {
		t.Fatal("update all without a root")
	}
	if _, err := runCLI(t, fs.URL, "reference", "list"); err == nil {
		t.Fatal("list failure")
	}

	// Several references are listed in name order; an existing code block is kept.
	routes := referenceRoutes()
	routes["GET /v1/collection-configs"] = `{"configs":[{"collection":"ref-z","config":{"code":{"role":"reference"}}},{"collection":"ref-a","config":{"code":{"role":"reference"}}}]}`
	routes["GET /v1/collection-config"] = `{"collection":"x","config":{"code":{"includePaths":["inc"]}}}`
	fs2 := newFakeServer(t, routes)
	if out, _ := runCLI(t, fs2.URL, "reference", "list"); strings.Index(out, "ref-a") > strings.Index(out, "ref-z") {
		t.Fatalf("order: %s", out)
	}
	t.Setenv("MDDB_REFERENCES_DIR", t.TempDir())
	stubClone(t, "")
	if _, err := runCLI(t, fs2.URL, "reference", "add", "https://x.org/a/b"); err != nil {
		t.Fatal(err)
	}
	put := callsTo(fs2, "PUT", "/v1/collection-config")
	code := asMap(put[0].Body["code"])
	if code["role"] != "reference" || len(code["includePaths"].([]interface{})) != 1 {
		t.Fatalf("existing code block kept: %v", code)
	}
}
