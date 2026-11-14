//go:build lspreal

package lspindex

// Real-server tests: local only (go test -tags lspreal), never in CI, which
// installs no language servers. Each needs its server on PATH or in
// MDDB_LSP_<LANG>, and the Go one needs the go toolchain this repo's
// go.work asks for first on PATH — exactly the setup the canary catches.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repoFiles lists files under dir (relative to it) with the extension,
// skipping what autoindex skips.
func repoFiles(t *testing.T, dir, ext string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) && p != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ext) {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func implsOf(res *Result, target string) []string {
	var out []string
	for path, ff := range res.Files {
		for _, i := range ff.Impls {
			if i.Target == target {
				out = append(out, path+":"+i.Local)
			}
		}
	}
	return out
}

func realRun(t *testing.T, dir, ext, lang string) *Result {
	t.Helper()
	spec, err := Spec(lang)
	if err != nil {
		t.Fatal(err)
	}
	files := repoFiles(t, dir, ext)
	res, err := Index(context.Background(), dir, files, spec, Options{Concurrency: 8, Timeout: 30 * time.Minute})
	if err != nil {
		t.Fatalf("%v", err)
	}
	st := res.Stats
	t.Logf("%s: %s, %d files listed, %d indexed; %d defs, %d ref lines (%d cross-file locations), %d impls; %d requests, %d skipped (%d timed out), %d unencodable, %d dropped; %s, peak RSS %d MB",
		lang, res.Server, len(files), st.Files, st.Defs, st.Refs, st.CrossFileRefs, st.Impls, st.Requests, st.Skipped, st.TimedOut, st.Unencodable, st.Dropped, st.Elapsed.Round(time.Millisecond), st.PeakRssMb)
	return res
}

func TestRealGopls(t *testing.T) {
	res := realRun(t, "../../../mddbd", ".go", "go")
	for target, want := range map[string]int{"backup_target.go#backupTarget": 3, "embedding_worker.go#chunkIndexer": 4} {
		got := implsOf(res, target)
		t.Logf("%s: %v", target, got)
		if len(got) != want {
			t.Errorf("%s: %d implementations, want %d", target, len(got), want)
		}
	}
}

func TestRealPython(t *testing.T) {
	res := realRun(t, "../../../../clients/python", ".py", "python")
	for _, path := range sortedKeys(res.Files) {
		t.Logf("%s: %+v", path, *res.Files[path])
	}
}

// TestRealDir indexes any tree: LSPREAL_DIR, LSPREAL_LANG, LSPREAL_EXT.
func TestRealDir(t *testing.T) {
	dir, lang, ext := os.Getenv("LSPREAL_DIR"), os.Getenv("LSPREAL_LANG"), os.Getenv("LSPREAL_EXT")
	if dir == "" {
		t.Skip("LSPREAL_DIR not set")
	}
	res := realRun(t, dir, ext, lang)
	for _, path := range sortedKeys(res.Files) {
		t.Logf("%s: %+v", path, *res.Files[path])
	}
}

func TestRealPHP(t *testing.T) {
	res := realRun(t, "../../../php-extension", ".php", "php")
	for _, path := range sortedKeys(res.Files) {
		t.Logf("%s: %+v", path, *res.Files[path])
	}
}
