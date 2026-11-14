package main

import (
	"strings"
	"testing"
)

// How the facts sync copes with a server that fails part of the way.

func warningsOf(rep autoindexReport) string { return strings.Join(rep.Semantic.Warnings, "\n") }

func TestSemanticSync_AggregateFails(t *testing.T) {
	dir := semanticTree(t)
	useFakeGopls(t, goScript(3))
	srv := newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) { f.fail["POST /v1/aggregate"] = true })
	rep := runReport(t, srv.URL, "--semantic=go", dir)
	w := warningsOf(rep)
	if !strings.Contains(w, "stored facts unavailable, every file with facts is sent") ||
		!strings.Contains(w, "facts not checked after sending") || rep.Semantic.Sent != 2 {
		t.Fatalf("report: %+v", rep.Semantic)
	}
}

func TestSemanticSync_FactsOnlyUpdateFailures(t *testing.T) {
	dir := semanticTree(t)
	useFakeGopls(t, goScript(3, 4))
	srv := newFakeMddbd(t)
	runReport(t, srv.URL, "--semantic=go", dir)
	useFakeGopls(t, goScript(3))

	srv.set(func(f *fakeMddbd) { f.fail["PATCH /v1/update"] = true })
	rep := runReport(t, srv.URL, "--semantic=go", dir)
	if !strings.Contains(warningsOf(rep), "facts for b.go: ") || rep.Semantic.FactsUpdated != 0 {
		t.Fatalf("a failed update: %+v", rep.Semantic)
	}

	srv.set(func(f *fakeMddbd) { f.fail["POST /v1/get"] = true })
	rep = runReport(t, srv.URL, "--semantic=go", dir)
	if !strings.Contains(warningsOf(rep), "facts for b.go: ") || rep.Semantic.FactsUpdated != 0 {
		t.Fatalf("a failed read: %+v", rep.Semantic)
	}
}

// Facts are only written onto the content they describe: when ingest did not
// store the file, the update is not made.
func TestSemanticSync_StoredContentDiffers(t *testing.T) {
	dir := semanticTree(t)
	useFakeGopls(t, goScript(3))
	srv := newFakeMddbd(t)
	srv.set(func(f *fakeMddbd) {
		f.docs["b.go"] = &storedDoc{Content: "package old\n", Meta: map[string][]string{"path": {"b.go"}}}
		f.fail["POST /v1/ingest"] = true
	})
	rep := runReport(t, srv.URL, "--semantic=go", dir)
	w := warningsOf(rep)
	if !strings.Contains(w, "facts for b.go not stored: the collection holds other content than the file") ||
		!strings.Contains(w, "facts for a.go: ") || writes(srv) != 1 {
		t.Fatalf("warnings: %s (%d writes)", w, writes(srv))
	}
}

func TestFactPair(t *testing.T) {
	for in, want := range map[string]string{
		"v1 aaaaaaaaaaaa bbbbbbbbbbbb gopls/0.24.0":           "aaaaaaaaaaaa bbbbbbbbbbbb",
		"v1 aaaaaaaaaaaa bbbbbbbbbbbb gopls/0.24.0 truncated": "aaaaaaaaaaaa bbbbbbbbbbbb",
		"v2 aaaaaaaaaaaa bbbbbbbbbbbb gopls/0.24.0":           "",
		"v1 aaaaaaaaaaaa": "",
	} {
		if got := factPair(in); got != want {
			t.Errorf("factPair(%q) = %q, want %q", in, got, want)
		}
	}
}
