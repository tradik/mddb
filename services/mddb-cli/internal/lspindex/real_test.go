//go:build lspreal

package lspindex

// Real-server tests: local only (go test -tags lspreal), never in CI, which
// installs no language servers. Each needs its server on PATH or in
// MDDB_LSP_<LANG>, and the Go one needs the go toolchain this repo's
// go.work asks for first on PATH — exactly the setup the canary catches.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
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
		if d.IsDir() && (d.Name() == "vendor" || d.Name() == "testdata" || d.Name() == "node_modules" || d.Name() == "target" || strings.HasPrefix(d.Name(), ".")) && p != dir {
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
	return realRunFiles(t, dir, repoFiles(t, dir, ext), lang, 8)
}

func realRunFiles(t *testing.T, dir string, files []string, lang string, concurrency int) *Result {
	t.Helper()
	spec, err := Spec(lang)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Index(context.Background(), dir, files, spec, Options{Concurrency: concurrency, Timeout: 30 * time.Minute})
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
//
// It also writes the code_symbol benchmark's ground truth (CODE-013,
// tools/bench/codesymbol): LSPREAL_FILES names a file listing the paths to
// index, one per line, relative to LSPREAL_DIR (the language's files from
// `mddb-cli autoindex --dry-run --json`, so the truth and the collection see
// the same tree), and LSPREAL_OUT the JSON file to write the Result to —
// straight from the language server, never through MDDB.
// LSPREAL_CONCURRENCY sets the requests in flight (default 8).
func TestRealDir(t *testing.T) {
	dir, lang, ext := os.Getenv("LSPREAL_DIR"), os.Getenv("LSPREAL_LANG"), os.Getenv("LSPREAL_EXT")
	if dir == "" {
		t.Skip("LSPREAL_DIR not set")
	}
	files := repoFiles(t, dir, ext)
	if list := os.Getenv("LSPREAL_FILES"); list != "" {
		b, err := os.ReadFile(list) // #nosec G304 -- a local test's input, named by the operator
		if err != nil {
			t.Fatal(err)
		}
		files = strings.Fields(string(b))
	}
	concurrency, _ := strconv.Atoi(os.Getenv("LSPREAL_CONCURRENCY"))
	if concurrency <= 0 {
		concurrency = 8
	}
	res := realRunFiles(t, dir, files, lang, concurrency)
	if out := os.Getenv("LSPREAL_OUT"); out != "" {
		b, err := json.Marshal(map[string]any{"server": res.Server, "stats": res.Stats, "files": res.Files})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
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
