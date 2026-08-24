package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TEST-001. Database-wide commands. These are the ones an operator runs on a
// live server, so the failure that matters most is a command reporting success
// after doing nothing.

func TestStatsRendersTheCollectionTable(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/stats": map[string]interface{}{
			"databasePath": "/var/lib/mddb/mddb.db", "databaseSize": 5 << 20, "mode": "wr",
			"totalDocuments": 120, "totalRevisions": 300, "totalMetaIndices": 42,
			"collections": []map[string]interface{}{
				{"name": "blog", "documentCount": 100, "revisionCount": 250, "metaIndexCount": 30},
				{"name": "docs", "documentCount": 20, "revisionCount": 50, "metaIndexCount": 12},
			},
		},
	})

	out, err := runCLI(t, fs.URL, "stats")
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}

	for _, want := range []string{"/var/lib/mddb/mddb.db", "wr", "120", "blog", "docs", "5.00 MB"} {
		mustContain(t, out, want)
	}
}

// An empty database is a legitimate state and must be described, not left as a
// blank table the operator has to interpret.
func TestStatsSaysSoWhenThereAreNoCollections(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/stats": map[string]interface{}{"databasePath": "/tmp/x.db", "mode": "wr"},
	})

	out, err := runCLI(t, fs.URL, "stats")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "No collections found")
}

// GO-005: a field arriving as a string where a number was expected used to
// panic the CLI mid-table.
func TestStatsSurvivesWronglyTypedNumbers(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/stats": `{"databaseSize":"big","totalDocuments":null,
			"collections":[{"name":"blog","documentCount":"many"}]}`,
	})

	out, err := runCLI(t, fs.URL, "stats")
	if err != nil {
		t.Fatalf("stats failed on wrongly-typed fields: %v", err)
	}
	mustContain(t, out, "blog")
}

func TestExportWritesToAFileWhenAsked(t *testing.T) {
	const ndjson = `{"key":"a"}` + "\n" + `{"key":"b"}` + "\n"
	fs := newFakeServer(t, map[string]interface{}{"/v1/export": ndjson})

	dest := filepath.Join(t.TempDir(), "out.ndjson")
	out, err := runCLI(t, fs.URL, "export", "blog", "--output", dest, "--filter", "tag=go")
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	assertBodyField(t, fs.lastCall(t).Body, "filterMeta", map[string][]string{"tag": {"go"}})

	written, err := os.ReadFile(dest) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatalf("export reported success but wrote no file: %v", err)
	}
	if string(written) != ndjson {
		t.Errorf("the exported file is %q, want the response verbatim", written)
	}
	mustContain(t, out, dest)
}

// Without --output the export is meant to be piped, so nothing but the data
// may reach stdout.
func TestExportToStdoutIsUndecorated(t *testing.T) {
	const ndjson = `{"key":"a"}` + "\n"
	fs := newFakeServer(t, map[string]interface{}{"/v1/export": ndjson})

	out, err := runCLI(t, fs.URL, "export", "blog")
	if err != nil {
		t.Fatal(err)
	}
	if out != ndjson {
		t.Errorf("export printed %q, want only the data", out)
	}
}

func TestExportFormatReachesTheServer(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{"/v1/export": "PK\x03\x04"})

	if _, err := runCLI(t, fs.URL, "export", "blog", "--format", "zip"); err != nil {
		t.Fatal(err)
	}
	assertBodyField(t, fs.lastCall(t).Body, "format", "zip")
}

func TestBackupNamesTheFileItCreated(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/backup": map[string]string{"backup": "nightly.db"},
	})

	out, err := runCLI(t, fs.URL, "backup", "nightly.db")
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}

	call := fs.lastCall(t)
	if call.Method != http.MethodGet {
		t.Errorf("backup used %s, want GET", call.Method)
	}
	mustContain(t, out, "nightly.db")
}

// Without a name the CLI invents a timestamped one, so an operator running it
// twice does not overwrite yesterday's backup.
func TestBackupWithoutANameStillNamesOne(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/backup": map[string]string{"backup": "backup-1700000000.db"},
	})

	if _, err := runCLI(t, fs.URL, "backup"); err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	call := fs.lastCall(t)
	if !strings.Contains(call.Path+"?"+strings.SplitN(call.Path, "?", 2)[0], "backup") {
		t.Errorf("path = %q", call.Path)
	}
}

func TestRestoreNamesTheSource(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/restore": map[string]string{"restored": "nightly.db"},
	})

	out, err := runCLI(t, fs.URL, "restore", "nightly.db")
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	assertBodyField(t, fs.lastCall(t).Body, "from", "nightly.db")
	mustContain(t, out, "nightly.db")
}

// A refused restore must not print a success line: an operator reading "✓
// Restored" would stop looking.
func TestRestoreFailureIsNotReportedAsSuccess(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/restore": failure{http.StatusForbidden, `{"error":"admin access required"}`},
	})

	out, err := runCLI(t, fs.URL, "restore", "nightly.db")
	if err == nil {
		t.Fatal("a refused restore exited zero")
	}
	if strings.Contains(out, "✓") {
		t.Errorf("a failed restore printed a success mark:\n%s", out)
	}
}

func TestTruncateSendsItsRetention(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/truncate": map[string]interface{}{"ok": true},
	})

	out, err := runCLI(t, fs.URL, "truncate", "blog", "--keep", "3")
	if err != nil {
		t.Fatalf("truncate failed: %v", err)
	}

	body := fs.lastCall(t).Body
	assertBodyField(t, body, "collection", "blog")
	assertBodyField(t, body, "keepRevs", 3)
	// --drop-cache defaults to true, and the default is part of the contract:
	// leaving a stale cache after a truncate would serve deleted revisions.
	assertBodyField(t, body, "dropCache", true)

	mustContain(t, out, "blog")
	mustContain(t, out, "3")
}

func TestVectorReindexReportsItsCounts(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/vector-reindex": map[string]interface{}{
			"embedded": 90, "skipped": 8, "failed": 2,
			"errors": []string{"doc x: model refused"},
		},
	})

	out, err := runCLI(t, fs.URL, "vector-reindex", "blog", "--force")
	if err != nil {
		t.Fatalf("vector-reindex failed: %v", err)
	}

	assertBodyField(t, fs.lastCall(t).Body, "force", true)

	// A partial failure must be visible: 2 failed out of 100 is not "done".
	for _, want := range []string{"90", "8", "2", "model refused"} {
		mustContain(t, out, want)
	}
}

func TestVectorStatsRendersCoverage(t *testing.T) {
	fs := newFakeServer(t, map[string]interface{}{
		"/v1/vector-stats": map[string]interface{}{
			"enabled": true, "model": "text-embedding-3-small",
			"dimensions": 1536, "totalVectors": 900,
		},
	})

	out, err := runCLI(t, fs.URL, "vector-stats")
	if err != nil {
		t.Fatalf("vector-stats failed: %v", err)
	}
	mustContain(t, out, "text-embedding-3-small")
}
