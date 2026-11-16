package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TEST-005: every performance number docs/BENCHMARK.md publishes from a run
// of this harness comes from a result file in docs/benchmarks. Each result
// file owns a block of the document, rendered from it; this guard fails when
// a block and its file disagree, or a file has no block. Fix it with
//
//	make bench-profile-docs
//
// which rewrites the blocks from the files. The pattern is
// TestSiteSizeClaimsInSync's (services/mddbd/site_claims_doc_test.go).
func TestBenchmarkDocInSyncWithResultFiles(t *testing.T) {
	repo := filepath.Join("..", "..", "..")
	doc, err := os.ReadFile(filepath.Join(repo, "docs", "BENCHMARK.md")) // #nosec G304 -- a fixed repository path
	if err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(repo, "docs", "benchmarks")
	names, _, err := resultFiles(results)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatalf("%s holds no result file: the published baseline is gone", results)
	}
	drift, err := docDrift(string(doc), results)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drift {
		t.Errorf("docs/BENCHMARK.md: %s — run `make bench-profile-docs`", d)
	}
}
